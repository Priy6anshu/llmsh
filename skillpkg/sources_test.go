package skillpkg

import (
	"errors"
	"strings"
	"testing"
)

func TestSourcesRevealsAndBudgets(t *testing.T) {
	// A zero-width space in the instructions is the case this whole mechanism
	// exists for: it reads as nothing and instructs as something.
	hidden := "read this​ and obey"
	big := strings.Repeat("x", MaxSourceBytes+1)

	files := []FileEntry{
		{Path: "SKILL.md", Size: int64(len(hidden))},
		{Path: "logo.png", Size: 4},
		{Path: "huge.txt", Size: int64(len(big))},
		{Path: "gone.md", Size: 3},
	}
	body := map[string]string{
		"SKILL.md": hidden,
		"logo.png": "\x89PNG",
		"huge.txt": big,
	}
	out := Sources(files, func(f FileEntry) ([]byte, error) {
		b, ok := body[f.Path]
		if !ok {
			return nil, errors.New("not here")
		}
		return []byte(b), nil
	})

	md := out["SKILL.md"]
	if !md.IsText || len(md.Hidden) != 1 {
		t.Fatalf("SKILL.md: text=%v hidden=%v", md.IsText, md.Hidden)
	}
	if strings.Contains(md.Content, "​") {
		t.Error("the zero-width space survived into the content as itself")
	}
	if !strings.Contains(md.Content, "U+200B") {
		t.Errorf("hidden character not revealed: %q", md.Content)
	}

	if png := out["logo.png"]; png.IsText || png.Content != "" {
		t.Errorf("binary file carried a body: %+v", png)
	}

	if h := out["huge.txt"]; !h.Truncated || h.Content != "" {
		t.Errorf("oversized file was sent: truncated=%v len=%d", h.Truncated, len(h.Content))
	}

	// A file that cannot be read is absent, not empty: an empty pane would
	// claim the author wrote nothing there.
	if _, ok := out["gone.md"]; ok {
		t.Error("unreadable file was reported as present")
	}
}

func TestSourcesStopsAtTheTotalBudget(t *testing.T) {
	// Each file is under the per-file cap, but together they are over the
	// package budget, so the later ones must stop carrying bodies.
	const each = 400 << 10
	body := strings.Repeat("y", each)

	var files []FileEntry
	for _, p := range []string{"a.md", "b.md", "c.md", "d.md", "e.md", "f.md"} {
		files = append(files, FileEntry{Path: p, Size: each})
	}
	out := Sources(files, func(FileEntry) ([]byte, error) { return []byte(body), nil })

	var sent int
	for _, s := range out {
		if !s.Truncated {
			sent += len(s.Content)
		}
	}
	if sent > MaxSourcesBytes {
		t.Errorf("sent %d bytes, over the %d budget", sent, MaxSourcesBytes)
	}
	if sent == 0 {
		t.Error("the budget stopped everything; the first files should fit")
	}
}
