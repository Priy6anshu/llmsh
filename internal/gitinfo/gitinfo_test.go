package gitinfo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestDirtyPathsSurviveParsing is the bug this parser was written around.
//
// git status --porcelain pads the status to a fixed two columns, so the first
// entry begins with a space when the change is unstaged. Trimming the output as
// a whole eats that space and takes the first character of the path with it,
// which produced "estdata/..." in a warning that is supposed to be a list of
// files the reader can go and look at.
func TestDirtyPathsSurviveParsing(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q")

	for _, name := range []string{"alpha.md", "beta.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("one\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "first")

	if got := Read(dir); got.Dirty {
		t.Fatalf("a clean tree reported dirty: %+v", got)
	}

	// Modify both, leaving them unstaged: the shape that triggered the bug.
	for _, name := range []string{"alpha.md", "beta.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("two\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := Read(dir)
	if !got.Dirty {
		t.Fatal("modified files did not report dirty")
	}
	want := map[string]bool{"alpha.md": true, "beta.md": true}
	if len(got.DirtyPaths) != 2 {
		t.Fatalf("paths = %v, want two", got.DirtyPaths)
	}
	for _, p := range got.DirtyPaths {
		if !want[p] {
			t.Errorf("path came back as %q, which is not a file in the repository", p)
		}
	}
}

// TestPathsWithSpacesSurvive: the default porcelain format quotes such paths,
// which is why this reads the NUL-separated form instead.
func TestPathsWithSpacesSurvive(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	name := "a file with spaces.md"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "first")
	if err := os.WriteFile(filepath.Join(dir, name), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := Read(dir)
	if len(got.DirtyPaths) != 1 || got.DirtyPaths[0] != name {
		t.Errorf("paths = %q, want [%q]", got.DirtyPaths, name)
	}
}

func TestNotARepositoryIsNotAnError(t *testing.T) {
	got := Read(t.TempDir())
	if got.Repo || got.Dirty || got.Commit != "" {
		t.Errorf("a plain directory reported %+v", got)
	}
}
