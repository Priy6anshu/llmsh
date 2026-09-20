// Package gitinfo reads the repository a skill happens to live in.
//
// alphaQ is not a git server and does not want to be. But a skill almost always
// sits in a repository, and the two knowing nothing about each other is the
// actual gap: git cannot tell you what you published, and the registry cannot
// tell you which commit it came from.
//
// This closes it by reading, never writing. It shells out to git rather than
// linking a git library, because the answer should be whatever the user's own
// git says -- including their config, their worktrees and their submodules.
package gitinfo

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// Info is what the repository says about the directory being published.
type Info struct {
	// Repo is true when the directory is inside a git repository at all.
	Repo bool
	// Commit is the full SHA of HEAD.
	Commit string
	Branch string
	// Dirty means the files being published differ from what is committed, so
	// what lands in the registry exists nowhere in the history.
	Dirty bool
	// DirtyPaths are the changed files under the directory being published,
	// which is narrower and more useful than "the repo is dirty".
	DirtyPaths []string
	// Remote is the origin URL, used to suggest metadata.llmskillhub.repository.
	Remote string
}

// runRaw returns the output untouched. Anything whose format has fixed-width
// columns has to use this: trimming is only safe for single-value answers.
func runRaw(ctx context.Context, dir string, args ...string) (string, bool) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

func run(ctx context.Context, dir string, args ...string) (string, bool) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// Read gathers what git knows about dir. It never fails: git missing, or the
// directory not being a repository, is a normal state and the caller carries on.
func Read(dir string) Info {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var i Info
	if _, ok := run(ctx, dir, "rev-parse", "--is-inside-work-tree"); !ok {
		return i
	}
	i.Repo = true
	i.Commit, _ = run(ctx, dir, "rev-parse", "HEAD")
	i.Branch, _ = run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	i.Remote, _ = run(ctx, dir, "remote", "get-url", "origin")

	// Scoped to this directory: a change in some unrelated corner of a monorepo
	// is not a reason to warn about the skill being published.
	//
	// -z rather than plain --porcelain, for two reasons. Git quotes paths that
	// contain spaces or non-ASCII in the default form, so the plain output needs
	// unquoting to be correct. And the status code is a fixed two columns, which
	// means any trimming of the output as a whole silently eats the leading
	// space of the first entry and takes the first character of its path with
	// it -- which is exactly what this code did before.
	if out, ok := runRaw(ctx, dir, "status", "--porcelain", "-z", "--", "."); ok && out != "" {
		i.Dirty = true
		for _, entry := range strings.Split(out, "\x00") {
			if len(entry) > 3 {
				i.DirtyPaths = append(i.DirtyPaths, entry[3:])
			}
		}
	}
	return i
}

// Short is the abbreviated commit, for display.
func (i Info) Short() string {
	if len(i.Commit) < 8 {
		return i.Commit
	}
	return i.Commit[:8]
}
