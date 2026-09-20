package skillpkg

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PackOptions controls how a directory becomes a .skill archive.
type PackOptions struct {
	// RootName is the single top-level directory written into the archive. The
	// official packager uses the skill's folder name; we default to the manifest
	// name so the archive is self-describing regardless of local folder naming.
	RootName string
}

// Pack writes dir as a .skill zip.
//
// The archive is deterministic: entries sorted by path, timestamps pinned, modes
// normalised. That is not what gives a version its identity — the tree digest
// does that, precisely because zip output is not reproducible across producers —
// but a stable archive still makes local diffing and caching behave.
func Pack(dir string, w io.Writer, opts PackOptions) (*Result, []FileEntry, error) {
	res := &Result{}

	files, err := collect(dir)
	if err != nil {
		return res, nil, err
	}
	if len(files) == 0 {
		res.Errorf("empty_package", "no files found in %s", dir)
		return res, nil, nil
	}

	root := opts.RootName
	if root == "" {
		root = filepath.Base(filepath.Clean(dir))
	}
	if man, _, err := readManifestFrom(dir); err == nil && man.Name != "" {
		root = man.Name
	}

	zw := zip.NewWriter(w)
	var entries []FileEntry
	var total int64

	// A fixed timestamp. Zip stores local time with no zone, so anything derived
	// from the clock makes byte-identical inputs produce different archives.
	epoch := time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

	for _, rel := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		st, err := os.Lstat(abs)
		if err != nil {
			return res, nil, err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			res.Add(SeverityError, "symlink_entry",
				fmt.Sprintf("%s is a symlink; skills may not contain symlinks", rel), At(rel, 0))
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return res, nil, err
		}
		total += int64(len(data))

		mode := normalizeMode(st.Mode())
		hdr := &zip.FileHeader{
			Name:     root + "/" + rel,
			Method:   zip.Deflate,
			Modified: epoch,
		}
		hdr.SetMode(os.FileMode(mode))

		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			return res, nil, err
		}
		if _, err := fw.Write(data); err != nil {
			return res, nil, err
		}

		sum := sha256.Sum256(data)
		entries = append(entries, FileEntry{
			Path: rel, Mode: mode, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]),
		})
	}
	if err := zw.Close(); err != nil {
		return res, nil, err
	}

	if total > MaxUncompressed {
		res.Errorf("expansion_too_large", "package is %s uncompressed; the maximum is %s",
			humanBytes(total), humanBytes(MaxUncompressed))
	}
	if len(entries) > MaxFiles {
		res.Errorf("too_many_files", "package has %d files; the maximum is %d", len(entries), MaxFiles)
	}
	return res, entries, nil
}

// collect walks dir and returns slash-separated relative paths, sorted, with the
// same exclusions the official packager applies.
func collect(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if isStripped(rel + "/x") {
				return fs.SkipDir
			}
			return nil
		}
		if isStripped(rel) || isForbiddenPath(rel) {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// PackDigest is the identity a local directory would publish under. It lets the
// CLI print the digest before uploading and lets the server verify the client's
// claim, without either side agreeing on zip bytes.
func PackDigest(dir string) (string, []FileEntry, error) {
	files, err := collect(dir)
	if err != nil {
		return "", nil, err
	}
	var entries []FileEntry
	for _, rel := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		st, err := os.Lstat(abs)
		if err != nil {
			return "", nil, err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return "", nil, fmt.Errorf("%s is a symlink", rel)
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return "", nil, err
		}
		sum := sha256.Sum256(data)
		entries = append(entries, FileEntry{
			Path: rel, Mode: normalizeMode(st.Mode()),
			Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]),
		})
	}
	return TreeDigest(entries), entries, nil
}

// ValidateDir runs the manifest and layout checks against a working directory,
// without building an archive. This is what `skillhub validate` calls.
func ValidateDir(dir string) (*Manifest, *Result, error) {
	man, res, err := readManifestFrom(dir)
	if err != nil {
		return nil, res, err
	}
	files, err := collect(dir)
	if err != nil {
		return man, res, err
	}
	pkg := &Package{Manifest: man}
	for _, rel := range files {
		pkg.Files = append(pkg.Files, FileEntry{Path: rel})
	}
	checkLayout(pkg, res)
	Quality(man, pkg.Files, res)
	return man, res, nil
}

func readManifestFrom(dir string) (*Manifest, *Result, error) {
	res := &Result{}
	data, err := os.ReadFile(filepath.Join(dir, SkillFile))
	if err != nil {
		if os.IsNotExist(err) {
			res.Add(SeverityError, "missing_skill_md",
				fmt.Sprintf("no %s in %s", SkillFile, dir))
			return &Manifest{}, res, nil
		}
		return nil, res, err
	}
	if int64(len(data)) > MaxSkillMD {
		res.Errorf("skill_md_too_large", "%s is %s; the maximum is %s",
			SkillFile, humanBytes(int64(len(data))), humanBytes(MaxSkillMD))
	}
	man, mres := ParseManifest(data)
	res.Merge(mres)
	return man, res, nil
}

func hasPrefixAny(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
