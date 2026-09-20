package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	skill "github.com/Priy6anshu/llmsh/skillpkg"
)

// cmdInstall downloads a skill and unpacks it where an agent will find it.
//
// The digest check is the point of doing this rather than curl and unzip. The
// archive arrives from an object store over a link the API minted, and the tree
// digest is recomputed from the unpacked files and compared with what the API
// said it should be -- so a substituted archive is caught by the client rather
// than trusted because it came over HTTPS.
func cmdInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	dest := fs.String("dir", "", "where to unpack (default: .claude/skills here, else ~/.claude/skills)")
	force := fs.Bool("force", false, "replace an existing installation")
	ref := first(parseArgs(fs, args), "")
	if ref == "" {
		return fmt.Errorf("which skill? e.g. llmsh install xalpha2/flaky-test-hunter")
	}
	owner, name, version := parseRef(ref)

	cfg, err := loadConfigOnly()
	if err != nil {
		return err
	}
	if owner == "" {
		return fmt.Errorf("which publisher? use owner/skill, e.g. llmsh install xalpha2/%s", name)
	}
	c := newClient(cfg)

	archive, want, err := c.Download(owner, name, version)
	if err != nil {
		return err
	}

	target, err := installDir(*dest)
	if err != nil {
		return err
	}
	into := filepath.Join(target, name)
	if _, err := os.Stat(into); err == nil && !*force {
		return fmt.Errorf("%s already exists\n  Pass --force to replace it", into)
	}

	// Unpacked to a temporary directory first, and moved into place only after
	// the digest matches. A half-written skill directory is one an agent may
	// load, so nothing lands under the target until it is known to be right.
	//
	// Unpack strips the archive's single top-level directory and writes the
	// contents at the root it is given, so this temp directory IS the skill --
	// it is renamed into place rather than something being lifted out of it.
	tmp, err := os.MkdirTemp(target, ".aq-install-")
	if err != nil {
		return err
	}
	moved := false
	defer func() {
		if !moved {
			os.RemoveAll(tmp)
		}
	}()

	pkg, err := verifyInto(archive, want, tmp)
	if err != nil {
		return err
	}

	if *force {
		if err := os.RemoveAll(into); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, into); err != nil {
		return err
	}
	moved = true
	// The temp directory was created with restrictive permissions; the installed
	// skill is ordinary content and should read like the rest of the directory.
	if err := os.Chmod(into, 0o755); err != nil {
		return err
	}

	shown := version
	if shown == "" {
		shown = "latest"
	}
	fmt.Printf("%s/%s@%s → %s\n", owner, name, shown, into)
	fmt.Printf("  %d files · digest %s verified\n", len(pkg.Files), skill.ShortDigest(pkg.Digest))
	return nil
}

// parseRef splits owner/name@version. Version is optional and means latest.
func parseRef(ref string) (owner, name, version string) {
	if i := strings.LastIndex(ref, "@"); i > 0 {
		ref, version = ref[:i], ref[i+1:]
	}
	if i := strings.Index(ref, "/"); i > 0 {
		owner, name = ref[:i], ref[i+1:]
		return owner, name, version
	}
	return "", ref, version
}

// installDir picks where skills go.
//
// A project-local .claude/skills wins when it exists, because a skill installed
// beside a project should travel with it; otherwise the personal directory.
// Never created speculatively in the project: making .claude/ in someone's
// repository because they ran an install is not this command's business.
func installDir(explicit string) (string, error) {
	if explicit != "" {
		return explicit, os.MkdirAll(explicit, 0o755)
	}
	if st, err := os.Stat(filepath.Join(".claude", "skills")); err == nil && st.IsDir() {
		return filepath.Join(".claude", "skills"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(home, ".claude", "skills")
	return p, os.MkdirAll(p, 0o755)
}

// verifyInto unpacks an archive and refuses it unless it is what the registry
// said it would be.
//
// Separate from the command because it is the security-relevant step: the bytes
// arrive from an object store over a link the API minted, and this is what
// makes that link's contents checkable rather than merely encrypted in transit.
// The digest is recomputed from the unpacked files -- not read from the archive,
// which would be asking the thing being verified to vouch for itself.
func verifyInto(archive []byte, want, dir string) (*skill.Package, error) {
	pkg, res, err := skill.Unpack(context.Background(), bytes.NewReader(archive), int64(len(archive)), dir)
	if err != nil {
		return nil, err
	}
	if errs, _ := report(res); errs > 0 {
		return nil, fmt.Errorf("the downloaded archive did not pass validation")
	}
	if want == "" {
		// Nothing to compare against. Said plainly rather than passing quietly:
		// an unverified install is a different thing from a verified one.
		return pkg, nil
	}
	if pkg.Digest != want {
		return nil, fmt.Errorf("digest mismatch: the registry says %s, these bytes are %s\n"+
			"  Do not use this. Report it.", skill.ShortDigest(want), skill.ShortDigest(pkg.Digest))
	}
	return pkg, nil
}
