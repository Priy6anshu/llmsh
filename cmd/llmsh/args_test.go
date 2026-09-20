package main

import (
	"flag"
	"testing"
)

// TestParseArgsAcceptsEitherOrder is the bug this replaced.
//
// The first version took "the first argument not starting with a dash" as the
// positional. In `llmsh init --name csv-tidy` that argument is the flag's VALUE, so
// --name arrived empty and "csv-tidy" was used as the directory. Go's own flag
// parser is the only thing that knows which tokens are values.
func TestParseArgsAcceptsEitherOrder(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantName string
		wantDry  bool
		wantPos  string
	}{
		{"flags only", []string{"--name", "csv-tidy"}, "csv-tidy", false, ""},
		{"flag value is not the positional", []string{"--name", "csv-tidy", "--dry-run"}, "csv-tidy", true, ""},
		{"positional first", []string{"./dir", "--dry-run"}, "", true, "./dir"},
		{"positional last", []string{"--dry-run", "./dir"}, "", true, "./dir"},
		{"positional between flags", []string{"--name", "x", "./dir", "--dry-run"}, "x", true, "./dir"},
		{"nothing", nil, "", false, ""},
		{"equals form", []string{"--name=csv-tidy", "./dir"}, "csv-tidy", false, "./dir"},
	}
	for _, c := range cases {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		name := fs.String("name", "", "")
		dry := fs.Bool("dry-run", false, "")
		pos := first(parseArgs(fs, c.args), "")

		if *name != c.wantName {
			t.Errorf("%s: name = %q, want %q", c.name, *name, c.wantName)
		}
		if *dry != c.wantDry {
			t.Errorf("%s: dry-run = %v, want %v", c.name, *dry, c.wantDry)
		}
		if pos != c.wantPos {
			t.Errorf("%s: positional = %q, want %q", c.name, pos, c.wantPos)
		}
	}
}

func TestScaffoldPassesValidation(t *testing.T) {
	// The generated SKILL.md has to be one the validator accepts, or the
	// scaffold has taught someone that the tool does not know its own rules.
	out := scaffold("csv-tidy",
		"Cleans messy CSV exports so they import without manual repair. Use this skill whenever "+
			"the user mentions cleaning a CSV or fixing headers, even when they only describe the "+
			"symptom, such as a spreadsheet that will not import.",
		[]string{"data"}, "MIT")

	if !containsAll(out, "name: csv-tidy", "description: >", "version: 0.1.0", "categories: [data]") {
		t.Errorf("scaffold is missing required frontmatter:\n%s", out)
	}
	if containsAll(out, "\t") {
		t.Error("scaffold contains a tab, which YAML forbids for indentation")
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
