package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Priy6anshu/llmsh/internal/client"
	"github.com/Priy6anshu/llmsh/internal/gitinfo"
	"github.com/Priy6anshu/llmsh/internal/workdir"
	skill "github.com/Priy6anshu/llmsh/skillpkg"
)

// report prints validation findings the same way the web uploader does, using
// the same codes, because the CLI and the browser run the same checks and a
// person who sees different words in each has to learn the format twice.
func report(res *skill.Result) (errors, warnings int) {
	for _, v := range res.Violations {
		switch v.Severity {
		case "error":
			errors++
		case "warn":
			warnings++
		}
		mark := map[string]string{"error": "✗", "warn": "!", "info": "·"}[string(v.Severity)]
		where := v.Path
		if v.Line > 0 {
			where = fmt.Sprintf("%s:%d", v.Path, v.Line)
		}
		if where != "" {
			where = " " + where
		}
		fmt.Printf("  %s %s%s  %s\n", mark, v.Code, where, v.Message)
		if v.Hint != "" {
			fmt.Printf("      %s\n", v.Hint)
		}
	}
	return errors, warnings
}

func cmdValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	dir := first(parseArgs(fs, args), ".")
	man, res, err := skill.ValidateDir(dir)
	if err != nil {
		return err
	}

	digest, files, derr := skill.PackDigest(dir)
	if derr != nil {
		return derr
	}

	var size int64
	for _, f := range files {
		size += f.Size
	}
	name := "(no name)"
	if man != nil {
		name = man.Name
	}
	fmt.Printf("%s — %d files, %s\n", name, len(files), humanBytes(size))
	fmt.Printf("  digest %s\n", skill.ShortDigest(digest))

	errs, warns := report(res)
	if errs == 0 && warns == 0 {
		fmt.Println("  no problems found")
	}
	if errs > 0 {
		return fmt.Errorf("%d error(s) — this would be refused", errs)
	}
	return nil
}

// cmdDiff answers "what am I about to change", against the registry.
//
// This is the question git cannot answer on its own: your working tree knows
// what you changed since your last commit, and nothing local knows what you
// changed since the version other people are installing.
func cmdDiff(args []string) error {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	against := fs.String("version", "", "compare against this published version (default: latest)")
	dir := first(parseArgs(fs, args), ".")

	c, cfg, err := clientFromConfig()
	if err != nil {
		return err
	}
	man, _, err := skill.ValidateDir(dir)
	if err != nil {
		return err
	}
	if man == nil || man.Name == "" {
		return fmt.Errorf("%s has no name in SKILL.md", dir)
	}
	owner := cfg.Handle
	if owner == "" {
		me, err := c.Me()
		if err != nil {
			return err
		}
		owner = me.Handle
	}

	versions, err := c.Versions(owner, man.Name)
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		fmt.Printf("%s/%s has no published versions — everything here is new.\n", owner, man.Name)
		return nil
	}
	target := *against
	if target == "" {
		target = versions[0].Version
	}

	published, err := c.Files(owner, man.Name, target)
	if err != nil {
		return err
	}
	_, local, err := skill.PackDigest(dir)
	if err != nil {
		return err
	}

	// Compared by content hash, not by reading both sides: the registry already
	// stores a hash per file, so an unchanged file needs no download at all.
	pub := map[string]string{}
	for _, f := range published {
		pub[f.Path] = f.SHA256
	}
	loc := map[string]string{}
	for _, f := range local {
		loc[f.Path] = f.SHA256
	}

	var added, changed, removed []string
	for p, h := range loc {
		switch prev, ok := pub[p]; {
		case !ok:
			added = append(added, p)
		case prev != h:
			changed = append(changed, p)
		}
	}
	for p := range pub {
		if _, ok := loc[p]; !ok {
			removed = append(removed, p)
		}
	}
	sort.Strings(added)
	sort.Strings(changed)
	sort.Strings(removed)

	fmt.Printf("%s/%s — working directory against %s\n", owner, man.Name, target)
	if len(added)+len(changed)+len(removed) == 0 {
		fmt.Println("  identical: there is nothing to publish")
		return nil
	}
	for _, p := range added {
		fmt.Printf("  + %s\n", p)
	}
	for _, p := range changed {
		fmt.Printf("  ~ %s\n", p)
	}
	for _, p := range removed {
		fmt.Printf("  - %s\n", p)
	}
	fmt.Printf("\n  %d added, %d changed, %d removed\n", len(added), len(changed), len(removed))

	if g := gitinfo.Read(dir); g.Repo {
		fmt.Printf("  git: %s on %s%s\n", g.Short(), g.Branch, dirtySuffix(g))
	}
	return nil
}

func dirtySuffix(g gitinfo.Info) string {
	if !g.Dirty {
		return ""
	}
	return fmt.Sprintf(", %d uncommitted change(s)", len(g.DirtyPaths))
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func cmdPublish(args []string) error {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	version := fs.String("version", "", "version to publish (default: the one in SKILL.md)")
	allowDirty := fs.Bool("allow-dirty", false, "publish even with uncommitted changes")
	dryRun := fs.Bool("dry-run", false, "validate on the server without storing anything")
	dir := first(parseArgs(fs, args), ".")

	c, _, err := clientFromConfig()
	if err != nil {
		return err
	}

	man, res, err := skill.ValidateDir(dir)
	if err != nil {
		return err
	}
	if man == nil || man.Name == "" {
		return fmt.Errorf("%s has no name in SKILL.md", dir)
	}
	if errs, _ := report(res); errs > 0 {
		return fmt.Errorf("%d error(s) — fix these first", errs)
	}

	// The git check. Publishing something that exists in no commit means the
	// bytes in the registry cannot be reproduced from the repository, and the
	// person who wrote them is the only one who will ever have them.
	g := gitinfo.Read(dir)
	if g.Repo && g.Dirty && !*allowDirty {
		fmt.Fprintf(os.Stderr, "Uncommitted changes in %s:\n", dir)
		for _, p := range g.DirtyPaths {
			fmt.Fprintf(os.Stderr, "  %s\n", p)
		}
		return fmt.Errorf("publishing now would ship bytes that exist in no commit\n" +
			"  Commit them, or pass --allow-dirty if that is what you meant")
	}

	ver := *version
	if ver == "" && man.Hub.Version != "" {
		ver = man.Hub.Version
	}
	if ver == "" {
		return fmt.Errorf("no version: set metadata.skillhub.version in SKILL.md, or pass --version")
	}

	var buf bytes.Buffer
	packRes, files, err := skill.Pack(dir, &buf, skill.PackOptions{RootName: man.Name})
	if err != nil {
		return err
	}
	if errs, _ := report(packRes); errs > 0 {
		return fmt.Errorf("packaging refused")
	}

	fmt.Printf("%s@%s — %d files, %s\n", man.Name, ver, len(files), humanBytes(int64(buf.Len())))
	if g.Repo {
		fmt.Printf("  from %s on %s\n", g.Short(), g.Branch)
	}

	if *dryRun {
		fmt.Println("  --dry-run: checking on the server, storing nothing")
	}
	out, err := c.Publish(man.Name, ver, buf.Bytes(), *dryRun)
	if err != nil {
		return err
	}
	if errs, warns := reportUpload(out); errs > 0 {
		return fmt.Errorf("the server refused this package")
	} else if warns > 0 {
		fmt.Printf("  %d warning(s) — published anyway\n", warns)
	}

	// A working copy that just published is now at the version it published.
	// Leaving the record at whatever was cloned is what made pull and diff
	// contradict each other: one compared against a stale version, the other
	// against the latest.
	if out.OK && !*dryRun {
		if origin, rerr := workdir.Read(dir); rerr == nil && origin != nil {
			origin.Version, origin.Digest = out.Version, out.Digest
			_ = workdir.Write(dir, origin)
		}
	}

	switch {
	case *dryRun:
		fmt.Printf("\n  would publish %s@%s · digest %s\n", man.Name, ver, skill.ShortDigest(digestOf(files)))
		fmt.Printf("  Nothing was stored.\n")
	case out.OK:
		fmt.Printf("\n  %s@%s submitted · digest %s · %s\n",
			out.Slug, out.Version, out.ShortDigest, out.ReviewState)
		fmt.Printf("  A person reads every version before it appears in the catalogue.\n")
	}
	return nil
}

// reportUpload prints what the server found. Its verdict, not ours: the CLI
// runs the same checks locally for speed, but ingest re-reads the archive it
// actually received and that is the answer that counts.
func reportUpload(out *client.UploadResponse) (errors, warnings int) {
	for _, v := range out.Violations {
		switch v.Severity {
		case "error":
			errors++
		case "warn":
			warnings++
		}
		mark := map[string]string{"error": "✗", "warn": "!", "info": "·"}[v.Severity]
		where := v.Path
		if v.Line > 0 {
			where = fmt.Sprintf("%s:%d", v.Path, v.Line)
		}
		if where != "" {
			where = " " + where
		}
		fmt.Printf("  %s %s%s  %s\n", mark, v.Code, where, v.Message)
		if v.Hint != "" {
			fmt.Printf("      %s\n", v.Hint)
		}
	}
	if out.Scores.Description.Total > 0 || out.Scores.Completeness.Total > 0 {
		fmt.Printf("  description %d/100 · completeness %d/100\n",
			out.Scores.Description.Total, out.Scores.Completeness.Total)
	}
	return errors, warnings
}

func cmdStatus(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("which skill? e.g. llmsh status flaky-test-hunter")
	}
	name := args[0]

	c, cfg, err := clientFromConfig()
	if err != nil {
		return err
	}
	owner := cfg.Handle
	if i := strings.Index(name, "/"); i > 0 {
		owner, name = name[:i], name[i+1:]
	}
	if owner == "" {
		me, err := c.Me()
		if err != nil {
			return err
		}
		owner = me.Handle
	}

	versions, err := c.Versions(owner, name)
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		return fmt.Errorf("no versions of %s/%s that you can see", owner, name)
	}
	fmt.Printf("%s/%s\n", owner, name)
	for _, v := range versions {
		state := v.ReviewState
		if v.Yanked {
			state += ", withdrawn"
		}
		fmt.Printf("  %-10s %-18s %s  %s\n", v.Version, state, v.ShortDigest, humanBytes(v.Size))
		if v.ReviewNotes != "" {
			fmt.Printf("      %s\n", strings.TrimSpace(v.ReviewNotes))
		}
	}
	return nil
}

// digestOf is the identity these files would publish under, computed from the
// entries Pack already produced rather than by walking the directory twice.
func digestOf(files []skill.FileEntry) string { return skill.TreeDigest(files) }
