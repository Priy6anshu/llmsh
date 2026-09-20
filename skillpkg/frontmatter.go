package skillpkg

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ManifestFields are the frontmatter values a creator can supply after the
// fact, when the package they uploaded did not carry them.
//
// Deliberately only the two the skill standard requires. This is a way to
// finish a manifest that is missing, not a manifest editor: everything else
// belongs in the file, where the author can see it beside their own work.
type ManifestFields struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// FixableFields are those fields in the order they belong in the file.
var FixableFields = []string{"name", "description"}

// fixableCodes maps a validation failure to the field that would answer it.
//
// Every one of these means the value is ABSENT. A value that is present and
// wrong -- a name with capitals in it, a description over the limit -- is the
// author's own text, and a form that quietly replaced it would be editing their
// work rather than completing it. Those they fix in the file.
var fixableCodes = map[string][]string{
	"missing_frontmatter": {"name", "description"},
	"missing_name":        {"name"},
	"empty_name":          {"name"},
	"missing_description": {"description"},
	"empty_description":   {"description"},
}

// MissingManifestFields reports which manifest fields a package did not supply
// — but only when supplying them is the only thing standing between it and a
// valid package.
//
// That qualification is the whole point. Offering to fill in a name for a
// package that is also rejected for something else walks someone through a form
// and into the same refusal, which is worse than not offering.
func MissingManifestFields(res *Result) []string {
	if res == nil {
		return nil
	}
	want := map[string]bool{}
	for _, v := range res.Violations {
		if v.Severity != SeverityError {
			continue
		}
		fields, ok := fixableCodes[v.Code]
		if !ok {
			return nil
		}
		for _, f := range fields {
			want[f] = true
		}
	}
	out := make([]string, 0, len(FixableFields))
	for _, f := range FixableFields {
		if want[f] {
			out = append(out, f)
		}
	}
	return out
}

// ErrUnfixableFrontmatter says the block cannot be edited safely.
var ErrUnfixableFrontmatter = errors.New("this frontmatter has to be fixed by hand")

// PatchFrontmatter writes manifest fields into a SKILL.md that lacks them.
//
// It inserts, and replaces only a key whose value is empty. A field the author
// filled in is left exactly as they wrote it, and so is every other line of the
// file — which is what makes it honest to show someone the block that will be
// added and call that the whole change.
//
// The values are serialised by the YAML encoder rather than pasted in, so a
// description containing a colon, a newline, or a second key's worth of text
// becomes one scalar rather than structure.
func PatchFrontmatter(src []byte, f ManifestFields) ([]byte, error) {
	var bom []byte
	if b := []byte("\xef\xbb\xbf"); bytes.HasPrefix(src, b) {
		bom, src = b, src[len(b):]
	}

	values := map[string]string{
		"name":        strings.TrimSpace(f.Name),
		"description": strings.TrimSpace(f.Description),
	}

	// No frontmatter at all. The block is entirely new, and the file's first
	// line of prose stays its first line of prose.
	if !bytes.HasPrefix(src, []byte("---")) {
		var b bytes.Buffer
		b.WriteString("---\n")
		for _, k := range FixableFields {
			if values[k] == "" {
				continue
			}
			line, err := yamlEntry(k, values[k])
			if err != nil {
				return nil, err
			}
			b.WriteString(line)
		}
		b.WriteString("---\n\n")
		b.Write(bytes.TrimLeft(src, "\r\n"))
		return append(bom, b.Bytes()...), nil
	}

	loc := frontmatterRe.FindSubmatchIndex(src)
	if loc == nil {
		return nil, fmt.Errorf("%w: the block is opened but never closed", ErrUnfixableFrontmatter)
	}
	block := src[loc[2]:loc[3]]

	keys, err := scalarKeys(block)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(block), "\n")
	var inserts []string
	for _, k := range FixableFields {
		if values[k] == "" {
			continue
		}
		entry, err := yamlEntry(k, values[k])
		if err != nil {
			return nil, err
		}
		entry = strings.TrimSuffix(entry, "\n")

		switch info, present := keys[k]; {
		case !present:
			// At the top of the block: name and description are what the file
			// is about, and nobody should scroll past a tool list to find them.
			inserts = append(inserts, entry)
		case info.empty:
			// The key is there with nothing after it. Writing a second one
			// would be a duplicate key, which is its own error.
			lines[info.line-1] = entry
		}
	}
	if len(inserts) == 0 && string(block) == strings.Join(lines, "\n") {
		return append(bom, src...), nil // nothing was missing
	}

	patched := strings.Join(append(inserts, lines...), "\n")
	out := make([]byte, 0, len(src)+len(patched))
	out = append(out, src[:loc[2]]...)
	out = append(out, patched...)
	out = append(out, src[loc[3]:]...)
	return append(bom, out...), nil
}

type keyInfo struct {
	line  int // 1-indexed within the frontmatter block
	empty bool
}

// scalarKeys finds the fields we may fill in and whether they hold anything.
func scalarKeys(block []byte) (map[string]keyInfo, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(block, &root); err != nil {
		return nil, fmt.Errorf("%w: it is not valid YAML", ErrUnfixableFrontmatter)
	}
	out := map[string]keyInfo{}
	if len(root.Content) == 0 {
		return out, nil // empty block: everything is an insert
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%w: it is not a mapping of key: value pairs", ErrUnfixableFrontmatter)
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		k, v := doc.Content[i], doc.Content[i+1]
		for _, want := range FixableFields {
			if k.Value != want {
				continue
			}
			out[want] = keyInfo{
				line:  k.Line,
				empty: v.Kind == yaml.ScalarNode && strings.TrimSpace(v.Value) == "",
			}
		}
	}
	return out, nil
}

// yamlEntry renders one key and value as YAML, quoting or folding as the value
// requires. Going through the encoder is what makes a hostile value inert.
func yamlEntry(key, val string) (string, error) {
	b, err := yaml.Marshal(map[string]string{key: val})
	if err != nil {
		return "", err
	}
	return string(b), nil
}
