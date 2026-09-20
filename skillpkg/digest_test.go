package skillpkg

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTreeDigestIsOrderIndependent(t *testing.T) {
	a := []FileEntry{
		{Path: "SKILL.md", Mode: 0o644, SHA256: hashBytes([]byte("a"))},
		{Path: "scripts/x.py", Mode: 0o755, SHA256: hashBytes([]byte("b"))},
	}
	b := []FileEntry{a[1], a[0]}
	if TreeDigest(a) != TreeDigest(b) {
		t.Error("digest must not depend on input order")
	}
}

func TestTreeDigestSensitivity(t *testing.T) {
	base := []FileEntry{{Path: "SKILL.md", Mode: 0o644, SHA256: hashBytes([]byte("a"))}}
	d := TreeDigest(base)

	content := []FileEntry{{Path: "SKILL.md", Mode: 0o644, SHA256: hashBytes([]byte("b"))}}
	if TreeDigest(content) == d {
		t.Error("digest must change with content")
	}
	path := []FileEntry{{Path: "OTHER.md", Mode: 0o644, SHA256: hashBytes([]byte("a"))}}
	if TreeDigest(path) == d {
		t.Error("digest must change with path")
	}
	mode := []FileEntry{{Path: "SKILL.md", Mode: 0o755, SHA256: hashBytes([]byte("a"))}}
	if TreeDigest(mode) == d {
		t.Error("digest must change with mode")
	}
}

// TestDigestSurvivesRepack is the property that makes the tree digest the right
// identity: it is derived from content, so it is stable across zip writers,
// compression settings and timestamps, none of which are reproducible.
func TestDigestSurvivesRepack(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string, mode os.FileMode) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("SKILL.md", goodSkillMD, 0o644)
	write("scripts/fill.py", "print('hi')\n", 0o755)

	fromDir, entries, err := PackDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d", len(entries))
	}

	var buf bytes.Buffer
	res, _, err := Pack(dir, &buf, PackOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Fatalf("pack failed: %s", res)
	}

	out := t.TempDir()
	pkg, ures, err := Unpack(context.Background(), bytes.NewReader(buf.Bytes()), int64(buf.Len()), out)
	if err != nil {
		t.Fatal(err)
	}
	if !ures.OK() {
		t.Fatalf("round trip rejected: %s", ures)
	}
	if pkg.Digest != fromDir {
		t.Errorf("digest changed across pack/unpack:\n  dir  %s\n  zip  %s", fromDir, pkg.Digest)
	}
	if got, err := os.ReadFile(filepath.Join(out, "scripts", "fill.py")); err != nil {
		t.Errorf("script missing after round trip: %v", err)
	} else if string(got) != "print('hi')\n" {
		t.Errorf("content changed: %q", got)
	}
}

func TestPackIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, SkillFile), []byte(goodSkillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	var a, b bytes.Buffer
	if _, _, err := Pack(dir, &a, PackOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Pack(dir, &b, PackOptions{}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Error("two packs of the same directory produced different bytes")
	}
}

func TestShortDigest(t *testing.T) {
	if got := ShortDigest("sha256:e3b0c44298fc1c14"); got != "e3b0c44" {
		t.Errorf("ShortDigest = %q, want e3b0c44", got)
	}
}
