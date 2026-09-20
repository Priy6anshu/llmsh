package skillpkg

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// This file exists for the human reviewer.
//
// In v1 a person is the only judgement layer, and the attack that matters most
// is text crafted to read as one thing while instructing an agent to do another.
// A viewer that renders such text faithfully hides exactly what needs seeing, so
// everything here makes the invisible visible rather than prettier.

// HiddenRune is one character that does not render as itself.
type HiddenRune struct {
	Rune   string `json:"rune"`   // "U+200B"
	Name   string `json:"name"`   // human label
	Count  int    `json:"count"`  // occurrences
	Danger string `json:"danger"` // why it matters here
}

var hiddenNames = map[rune][2]string{
	0x200B: {"ZERO WIDTH SPACE", "invisible; can split words to evade literal matching"},
	0x200C: {"ZERO WIDTH NON-JOINER", "invisible"},
	0x200D: {"ZERO WIDTH JOINER", "invisible"},
	0x200E: {"LEFT-TO-RIGHT MARK", "invisible; affects rendered order"},
	0x200F: {"RIGHT-TO-LEFT MARK", "invisible; affects rendered order"},
	0x202A: {"LEFT-TO-RIGHT EMBEDDING", "reverses displayed text order"},
	0x202B: {"RIGHT-TO-LEFT EMBEDDING", "reverses displayed text order"},
	0x202C: {"POP DIRECTIONAL FORMATTING", "ends a reordering span"},
	0x202D: {"LEFT-TO-RIGHT OVERRIDE", "forces displayed order; hides real content"},
	0x202E: {"RIGHT-TO-LEFT OVERRIDE", "forces displayed order; hides real content"},
	0x2066: {"LEFT-TO-RIGHT ISOLATE", "reorders displayed text"},
	0x2067: {"RIGHT-TO-LEFT ISOLATE", "reorders displayed text"},
	0x2068: {"FIRST STRONG ISOLATE", "reorders displayed text"},
	0x2069: {"POP DIRECTIONAL ISOLATE", "ends a reordering span"},
	0xFEFF: {"ZERO WIDTH NO-BREAK SPACE", "invisible"},
	0x00AD: {"SOFT HYPHEN", "usually invisible"},
	0x2060: {"WORD JOINER", "invisible"},
	0x180E: {"MONGOLIAN VOWEL SEPARATOR", "invisible"},
	// Characters that render as nothing without being classified as format
	// characters, which is how they get past filters that check the category.
	0x3164: {"HANGUL FILLER", "renders as blank; not a space to any filter"},
	0x115F: {"HANGUL CHOSEONG FILLER", "renders as blank"},
	0x1160: {"HANGUL JUNGSEONG FILLER", "renders as blank"},
	0x2800: {"BRAILLE PATTERN BLANK", "renders as blank"},
	0xFFA0: {"HALFWIDTH HANGUL FILLER", "renders as blank"},
}

// The Unicode tag block carries a full copy of ASCII that almost nothing
// renders — U+E0041 is a TAG LATIN CAPITAL LETTER A and displays as nothing at
// all. It was deprecated for language tagging and has since become the standard
// way to hide a paragraph of instructions inside what looks like one word.
//
// It is handled as a range rather than a table because the whole block is the
// same problem, and because naming all 96 of them would suggest some are fine.
const (
	tagBlockStart = 0xE0000
	tagBlockEnd   = 0xE007F
)

// Variation selectors modify the glyph before them and have no glyph of their
// own. A long run of them is not typography.
func isInvisibleModifier(r rune) bool {
	return (r >= 0xFE00 && r <= 0xFE0F) || (r >= 0xE0100 && r <= 0xE01EF)
}

func isTagChar(r rune) bool { return r >= tagBlockStart && r <= tagBlockEnd }

// describeExotic names a character from the ranges that are too large to
// tabulate, so a reader sees what it is rather than a bare code point.
func describeExotic(r rune) ([2]string, bool) {
	switch {
	case isTagChar(r):
		shown := ""
		if r >= 0xE0020 && r <= 0xE007E {
			shown = string(rune(r - 0xE0000))
		}
		why := "Unicode tag character: renders as nothing and carries hidden text"
		if shown != "" {
			why = "Unicode tag character for " + shown + ": renders as nothing and carries hidden text"
		}
		return [2]string{"TAG CHARACTER", why}, true
	case isInvisibleModifier(r):
		return [2]string{"VARIATION SELECTOR", "has no glyph of its own; a run of these carries hidden data"}, true
	}
	return [2]string{}, false
}

// describe names a character that a reader will not see, from the table or
// from one of the ranges too large to tabulate.
//
// One function so that the two callers cannot drift: a character that
// RevealHidden escapes but HiddenRunes does not count is a character the viewer
// marks and the warning never mentions, and vice versa.
func describe(r rune) ([2]string, bool) {
	if meta, ok := hiddenNames[r]; ok {
		return meta, true
	}
	return describeExotic(r)
}

// HiddenRunes reports characters that will not appear to a reader. Legitimate
// skill instructions do not contain these, so any hit is worth a hard look.
func HiddenRunes(s string) []HiddenRune {
	counts := map[rune]int{}
	for _, r := range s {
		if _, ok := describe(r); ok {
			counts[r]++
		}
	}
	out := make([]HiddenRune, 0, len(counts))
	for r, n := range counts {
		meta, _ := describe(r)
		out = append(out, HiddenRune{
			Rune: fmt.Sprintf("U+%04X", r), Name: meta[0], Count: n, Danger: meta[1],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rune < out[j].Rune })
	return out
}

// RevealHidden replaces invisible characters with a visible token, so what the
// reviewer reads is what the agent will receive.
func RevealHidden(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if meta, ok := describe(r); ok {
			fmt.Fprintf(&b, "‹%s U+%04X›", meta[0], r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

var urlRe = regexp.MustCompile(`(?i)\b(?:https?://|ftp://)[^\s"'` + "`" + `<>)\]}]+`)

// ExtractURLs lists every endpoint mentioned anywhere in the package.
//
// A skill that reaches somewhere undisclosed is the concrete form of the worst
// case here, and a reviewer should not have to find that by reading. Collecting
// them into one list turns a search into a glance.
func ExtractURLs(pkg *Package) []string {
	seen := map[string]bool{}
	scan := func(s string) {
		for _, m := range urlRe.FindAllString(s, -1) {
			m = strings.TrimRight(m, ".,;:")
			seen[m] = true
		}
	}
	if pkg == nil {
		return nil
	}
	scan(string(pkg.SkillMD))
	scan(string(pkg.Readme))
	for _, data := range pkg.Contents {
		scan(string(data))
	}
	out := make([]string, 0, len(seen))
	for u := range seen {
		out = append(out, u)
	}
	sort.Strings(out)
	return out
}

// IsText reports whether a blob should be shown as text.
//
// A NUL byte is the reliable marker: valid UTF-8 text never contains one, and
// almost every binary format does within its first few kilobytes.
func IsText(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	probe := b
	if len(probe) > 8000 {
		probe = probe[:8000]
	}
	if !utf8.Valid(probe) {
		return false
	}
	return !bytes.ContainsRune(probe, 0)
}
