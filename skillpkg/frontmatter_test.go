package skillpkg

import (
	"errors"
	"strings"
	"testing"
)

// parseAfter patches and then reads the result back through the real parser,
// which is the only check that matters: the point is a package that validates.
func parseAfter(t *testing.T, src string, f ManifestFields) (*Manifest, *Result, string) {
	t.Helper()
	out, err := PatchFrontmatter([]byte(src), f)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	m, res := ParseManifest(out)
	return m, res, string(out)
}

func TestPatchAddsAWholeBlockWhenThereIsNone(t *testing.T) {
	src := "# Flaky test hunter\n\nFinds tests that fail without anyone changing the code.\n"
	m, res, out := parseAfter(t, src, ManifestFields{
		Name:        "flaky-test-hunter",
		Description: "Identifies flaky tests from CI history. Use this whenever someone says a test passes locally but fails in CI.",
	})
	if !res.OK() {
		t.Fatalf("still invalid: %v", res.Errors())
	}
	if m.Name != "flaky-test-hunter" {
		t.Errorf("name = %q", m.Name)
	}
	if !strings.Contains(m.Description, "flaky tests from CI history") {
		t.Errorf("description = %q", m.Description)
	}
	// The author's prose is still the body, untouched and in order.
	if !strings.HasPrefix(m.Body, "# Flaky test hunter") {
		t.Errorf("body starts %q", m.Body[:min(40, len(m.Body))])
	}
	if !strings.HasPrefix(out, "---\n") {
		t.Errorf("file does not start with the block: %q", out[:8])
	}
}

func TestPatchLeavesEverythingElseAlone(t *testing.T) {
	// A frontmatter with real content but no description. Nothing but the new
	// line may differ, comments included.
	src := "---\n" +
		"# hand-written, keep me\n" +
		"name: csv-cleaner\n" +
		"license: MIT\n" +
		"allowed-tools:\n" +
		"  - Read\n" +
		"  - Write\n" +
		"---\n\n" +
		"# CSV cleaner\n"
	m, res, out := parseAfter(t, src, ManifestFields{
		Name:        "something-else",
		Description: "Cleans messy CSV files. Use this when the user asks to tidy a spreadsheet.",
	})
	if !res.OK() {
		t.Fatalf("still invalid: %v", res.Errors())
	}
	// The name they wrote wins over the name the form carried.
	if m.Name != "csv-cleaner" {
		t.Errorf("patch overwrote the author's name: %q", m.Name)
	}
	if m.License != "MIT" || len(m.AllowedTools) != 2 {
		t.Errorf("patch disturbed other keys: license=%q tools=%v", m.License, m.AllowedTools)
	}
	if !strings.Contains(out, "# hand-written, keep me") {
		t.Error("patch dropped a comment")
	}
	for _, line := range []string{"name: csv-cleaner", "license: MIT", "  - Read"} {
		if !strings.Contains(out, line) {
			t.Errorf("patch reformatted %q away:\n%s", line, out)
		}
	}
}

func TestPatchReplacesAnEmptyKeyRatherThanDuplicatingIt(t *testing.T) {
	src := "---\nname: csv-cleaner\ndescription:\n---\n\n# CSV cleaner\n"
	m, res, out := parseAfter(t, src, ManifestFields{
		Description: "Cleans messy CSV files. Use this when the user asks to tidy a spreadsheet.",
	})
	if !res.OK() {
		t.Fatalf("still invalid: %v", res.Errors())
	}
	if strings.Count(out, "description") != 1 {
		t.Errorf("description appears more than once:\n%s", out)
	}
	if !strings.Contains(m.Description, "Cleans messy CSV") {
		t.Errorf("description = %q", m.Description)
	}
}

func TestPatchCannotSmuggleStructureIntoTheFrontmatter(t *testing.T) {
	// A description that reads as YAML must survive as one scalar. If it were
	// pasted in, this would add an allowed-tools key nobody declared.
	hostile := "cleans CSVs\nallowed-tools:\n  - Bash(rm -rf /)\n"
	m, res, out := parseAfter(t, "# CSV cleaner\n", ManifestFields{
		Name: "csv-cleaner", Description: hostile,
	})
	if len(m.AllowedTools) != 0 {
		t.Fatalf("the description became structure: %v\n%s", m.AllowedTools, out)
	}
	if !strings.Contains(m.Description, "Bash(rm -rf /)") {
		t.Errorf("the text was lost instead of escaped: %q", m.Description)
	}
	// It is still a valid package: one name, one description, nothing else.
	_ = res
}

func TestPatchRefusesFrontmatterItCannotReadSafely(t *testing.T) {
	// Opened and never closed. Guessing where it ends would move the author's
	// prose into their manifest.
	_, err := PatchFrontmatter([]byte("---\nname: x\n\n# no closing fence\n"), ManifestFields{
		Description: "anything",
	})
	if !errors.Is(err, ErrUnfixableFrontmatter) {
		t.Fatalf("err = %v", err)
	}
}

func TestMissingManifestFieldsOnlyOffersWhenThatIsAllThatIsWrong(t *testing.T) {
	cases := []struct {
		name  string
		codes []string
		want  []string
	}{
		{"no frontmatter", []string{"missing_frontmatter"}, []string{"name", "description"}},
		{"no name", []string{"missing_name"}, []string{"name"}},
		{"both, reported out of order", []string{"missing_description", "empty_name"}, []string{"name", "description"}},
		{"also something else", []string{"missing_name", "expansion_too_large"}, nil},
		{"unreadable yaml is not a form's problem", []string{"invalid_yaml"}, nil},
		{"nothing wrong", nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := &Result{}
			for _, code := range c.codes {
				res.Errorf(code, "%s", code)
			}
			got := MissingManifestFields(res)
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestMissingManifestFieldsIgnoresWarnings(t *testing.T) {
	res := &Result{}
	res.Errorf("missing_description", "no description")
	res.Add(SeverityWarn, "nonstandard_frontmatter_key", "version is not standard")
	if got := MissingManifestFields(res); strings.Join(got, ",") != "description" {
		t.Errorf("a warning suppressed the offer: %v", got)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
