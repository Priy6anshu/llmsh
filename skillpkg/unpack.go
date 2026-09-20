package skillpkg

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// InventoryEntry is what the central directory alone tells us about one entry.
// Every field here is the archive's own claim and is re-verified during inflate.
type InventoryEntry struct {
	Name             string      `json:"name"` // as stored in the archive
	Rel              string      `json:"rel"`  // relative to the package root
	UncompressedSize uint64      `json:"uncompressed_size"`
	CompressedSize   uint64      `json:"compressed_size"`
	Mode             os.FileMode `json:"-"`
	IsDir            bool        `json:"is_dir"`
	Stripped         bool        `json:"stripped"`
}

type Inventory struct {
	CompressedSize    int64            `json:"compressed_size"`
	TotalUncompressed uint64           `json:"total_uncompressed"`
	Ratio             float64          `json:"ratio"`
	FileCount         int              `json:"file_count"`
	RootDir           string           `json:"root_dir,omitempty"`
	Entries           []InventoryEntry `json:"entries"`
}

// Inspect reads ONLY the zip central directory and answers every structural
// question without inflating a single byte.
//
// Zip is designed for this: the format puts a complete inventory at the tail of
// the file, so an io.ReaderAt over ranged reads fetches ~10-30 KB for a 5 MB
// archive. Counts, declared sizes, expansion ratio, paths, entry types and
// encryption flags are all answerable here — which is what lets us refuse a bomb
// before it costs us anything.
//
// Everything Inspect learns is the archive's own metadata and is attacker
// controlled. Unpack re-verifies it while inflating.
func Inspect(ra io.ReaderAt, size int64) (*Inventory, *Result) {
	res := &Result{}

	if size <= 0 {
		res.Errorf("empty_archive", "archive is empty")
		return nil, res
	}
	if size > MaxCompressed {
		res.Add(SeverityError, "archive_too_large",
			fmt.Sprintf("archive is %s; the maximum is %s", humanBytes(size), humanBytes(MaxCompressed)))
		return nil, res
	}
	if size > WarnCompressed {
		res.Add(SeverityWarn, "archive_unusually_large",
			fmt.Sprintf("archive is %s; the median published skill is 15 KB", humanBytes(size)))
	}

	if zip64Present(ra, size) {
		res.Add(SeverityError, "zip64_unsupported",
			"archive uses the ZIP64 format, which is only required above 4 GB",
			Hint("Re-create the archive with a standard zip writer."))
		return nil, res
	}

	// The shape of the file around the entries, before anything about the
	// entries themselves. A zip that two readers disagree about is refused
	// here rather than reviewed as whichever archive we happened to see.
	checkArchiveShape(ra, size, res)

	zr, err := zip.NewReader(ra, size)
	if err != nil {
		res.Errorf("corrupt_archive", "archive could not be read as a zip: %v", err)
		return nil, res
	}
	if len(zr.File) == 0 {
		res.Errorf("empty_archive", "archive contains no entries")
		return nil, res
	}
	if len(zr.File) > MaxFiles {
		res.Errorf("too_many_files", "archive has %d entries; the maximum is %d", len(zr.File), MaxFiles)
		return nil, res
	}

	inv := &Inventory{CompressedSize: size}
	seen := map[string]string{} // lowercased path -> original, for case-collision detection

	for _, f := range zr.File {
		name := f.Name
		isDir := strings.HasSuffix(name, "/")

		if err := validatePath(strings.TrimSuffix(name, "/")); err != nil {
			res.Add(SeverityError, "unsafe_path", fmt.Sprintf("%s: %v", name, err), At(name, 0))
			continue
		}

		// Entry type. Rejecting everything but regular files and directories
		// removes symlinks, hardlinks, devices, FIFOs and sockets in one check.
		mode := f.Mode()
		switch {
		case mode&os.ModeSymlink != 0:
			res.Add(SeverityError, "symlink_entry",
				"archive contains a symlink, which cannot appear in a valid skill", At(name, 0))
			continue
		case mode&(os.ModeDevice|os.ModeNamedPipe|os.ModeSocket|os.ModeCharDevice) != 0:
			res.Add(SeverityError, "special_file_entry",
				"archive contains a device, socket or FIFO entry", At(name, 0))
			continue
		case mode&(os.ModeSetuid|os.ModeSetgid) != 0:
			res.Add(SeverityError, "setuid_entry",
				"archive contains an entry with the setuid or setgid bit set", At(name, 0))
			continue
		}

		// General purpose bit 0 marks an encrypted entry. We will not distribute
		// what we cannot read, because we cannot review it either.
		if f.Flags&0x1 != 0 {
			res.Add(SeverityError, "encrypted_entry",
				"archive contains an encrypted entry", At(name, 0))
			continue
		}

		if !isDir && hasArchiveExt(name) {
			res.Add(SeverityError, "nested_archive",
				"archive contains another archive; skills may not bundle archives", At(name, 0),
				Hint("Nested archives cannot be reviewed and are how zip bombs amplify."))
			continue
		}
		if isForbiddenPath(name) {
			res.Add(SeverityError, "forbidden_path",
				fmt.Sprintf("%s must not be included in a package", name), At(name, 0))
			continue
		}

		lower := strings.ToLower(strings.TrimSuffix(name, "/"))
		if prev, dup := seen[lower]; dup {
			code, msg := "duplicate_path", fmt.Sprintf("duplicate entry %q", name)
			if prev != strings.TrimSuffix(name, "/") {
				code = "case_colliding_path"
				msg = fmt.Sprintf("%q collides with %q on case-insensitive filesystems", name, prev)
			}
			res.Add(SeverityError, code, msg, At(name, 0))
			continue
		}
		seen[lower] = strings.TrimSuffix(name, "/")

		e := InventoryEntry{
			Name: name, IsDir: isDir, Mode: mode,
			UncompressedSize: f.UncompressedSize64,
			CompressedSize:   f.CompressedSize64,
		}
		if !isDir {
			if int64(f.UncompressedSize64) > MaxSingleFile {
				res.Add(SeverityError, "file_too_large",
					fmt.Sprintf("%s declares %s; the per-file maximum is %s",
						name, humanBytes(int64(f.UncompressedSize64)), humanBytes(MaxSingleFile)), At(name, 0))
				continue
			}
			if int64(f.UncompressedSize64) > WarnSingleFile {
				res.Add(SeverityWarn, "file_unusually_large",
					fmt.Sprintf("%s is %s", name, humanBytes(int64(f.UncompressedSize64))), At(name, 0))
			}
			inv.TotalUncompressed += f.UncompressedSize64
			if int64(inv.TotalUncompressed) > MaxUncompressed {
				res.Errorf("declared_expansion_too_large",
					"entries declare more than %s of uncompressed data", humanBytes(MaxUncompressed))
				return inv, res
			}
			inv.FileCount++
		}
		inv.Entries = append(inv.Entries, e)
	}

	// Only a fatal finding stops here. Per-entry rejections are collected and we
	// keep going, so one upload surfaces every problem rather than the first one.
	if hasFatal(res) {
		return inv, res
	}
	if inv.FileCount == 0 {
		res.Errorf("no_files", "archive contains only directories")
		return inv, res
	}

	inv.Ratio = float64(inv.TotalUncompressed) / float64(size)
	// Where each entry's bytes actually sit, which the central directory does
	// not say and which is the only way to see a prepended payload or two
	// entries claiming the same bytes.
	spans := make([]entryExtent, 0, len(zr.File))
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		// DataOffset reads the entry's local header to find where its bytes
		// begin, which is the only authority on that: the central directory
		// points at the header, and the header's variable-length fields sit
		// between the two.
		at, err := f.DataOffset()
		if err != nil {
			continue
		}
		spans = append(spans, entryExtent{
			name: f.Name, start: at, end: at + int64(f.CompressedSize64),
		})
	}
	checkEntrySpans(spans, size, res)

	if inv.Ratio > MaxRatio {
		res.Errorf("compression_ratio", "archive expands %.0fx; the maximum is %.0fx", inv.Ratio, MaxRatio)
		return inv, res
	}
	if inv.Ratio > WarnRatio {
		res.Warnf("compression_ratio_high", "archive expands %.0fx", inv.Ratio)
	}
	if inv.FileCount > WarnFiles {
		res.Warnf("many_files", "archive has %d files; the median published skill has 3", inv.FileCount)
	}

	inv.RootDir = detectRoot(inv.Entries)
	for i := range inv.Entries {
		inv.Entries[i].Rel = strings.TrimPrefix(inv.Entries[i].Name, inv.RootDir)
		inv.Entries[i].Stripped = !inv.Entries[i].IsDir && isStripped(inv.Entries[i].Rel)
	}

	if !hasEntry(inv, SkillFile) {
		res.Add(SeverityError, "missing_skill_md",
			fmt.Sprintf("no %s at the package root", SkillFile),
			Hint(rootHint(inv)))
	}
	return inv, res
}

// detectRoot handles both shapes we see in the wild: the official packager writes
// entries as "<skill-name>/SKILL.md", while a hand-made zip often has SKILL.md at
// the top. If every entry shares one first segment, that segment is the root.
func detectRoot(entries []InventoryEntry) string {
	first := ""
	for _, e := range entries {
		seg, _, hasSlash := strings.Cut(e.Name, "/")
		if !hasSlash {
			return "" // a file sits at the top level, so there is no single root dir
		}
		if first == "" {
			first = seg
		} else if seg != first {
			return ""
		}
	}
	if first == "" {
		return ""
	}
	return first + "/"
}

func hasEntry(inv *Inventory, rel string) bool {
	for _, e := range inv.Entries {
		if !e.IsDir && e.Rel == rel {
			return true
		}
	}
	return false
}

func rootHint(inv *Inventory) string {
	for _, e := range inv.Entries {
		if !e.IsDir && strings.HasSuffix(e.Name, "/"+SkillFile) {
			return fmt.Sprintf("Found %s nested deeper than the root. The archive should contain "+
				"either SKILL.md at the top level or a single directory holding it.", e.Name)
		}
	}
	return "Every skill package must contain a SKILL.md file."
}

// fatalInspectCodes stop processing entirely: after one of these the archive
// either cannot be read or must not be touched further.
//
// Everything else — a forbidden path, a symlink, a nested archive — rejects one
// entry without preventing us from reading the rest. That distinction matters
// for the creator: a report that surfaces one problem per upload turns fixing a
// package into whack-a-mole, when we could have listed every problem the first
// time.
var fatalInspectCodes = map[string]bool{
	"empty_archive":                true,
	"archive_too_large":            true,
	"zip64_unsupported":            true,
	"corrupt_archive":              true,
	"too_many_files":               true,
	"declared_expansion_too_large": true,
	"compression_ratio":            true,
	"no_files":                     true,
	"file_too_large":               true,
}

func hasFatal(res *Result) bool {
	for _, v := range res.Errors() {
		if fatalInspectCodes[v.Code] {
			return true
		}
	}
	return false
}

// Package is a validated, extracted skill.
type Package struct {
	Dir       string      `json:"-"` // extracted files; caller owns cleanup
	Files     []FileEntry `json:"files"`
	Digest    string      `json:"digest"`
	Manifest  *Manifest   `json:"manifest"`
	Inventory *Inventory  `json:"inventory"`
	SkillMD   []byte      `json:"-"`
	// Contents holds every file's bytes, populated only when Unpack is called
	// with an empty dir. That mode exists for callers that need to inspect a
	// package without writing it anywhere — api verifying what ingest stored,
	// and the review screen reading scripts in full.
	Contents  map[string][]byte `json:"-"`
	Readme    []byte            `json:"-"`
	Changelog []byte            `json:"-"`
	Stripped  []string          `json:"stripped,omitempty"`
}

// Unpack inspects, extracts and validates an archive into dir.
//
// The central-directory checks in Inspect are the archive's own claims. Here we
// inflate under independent caps, because a crafted zip can declare 1 KB and then
// stream 10 GB. Every entry is read through a limit of its declared size PLUS ONE
// byte: that extra byte is what distinguishes "ended exactly where it said" from
// "kept going", which a plain limit would silently truncate instead of detecting.
func Unpack(ctx context.Context, ra io.ReaderAt, size int64, dir string) (*Package, *Result, error) {
	inv, res := Inspect(ra, size)
	if hasFatal(res) || inv == nil {
		return nil, res, nil
	}

	// A package with per-entry problems is still worth reading: we parse it to
	// complete the report, but nothing reaches disk and the caller will refuse
	// it. dir is cleared so the extraction below is purely in memory.
	reportOnly := !res.OK()
	if reportOnly {
		dir = ""
	}

	zr, err := zip.NewReader(ra, size)
	if err != nil {
		res.Errorf("corrupt_archive", "archive could not be read as a zip: %v", err)
		return nil, res, nil
	}

	pkg := &Package{Dir: dir, Inventory: inv}
	inMemory := dir == ""
	if inMemory {
		pkg.Contents = map[string][]byte{}
	}
	byName := map[string]InventoryEntry{}
	for _, e := range inv.Entries {
		byName[e.Name] = e
	}

	var written uint64
	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return nil, res, err
		}
		e, ok := byName[f.Name]
		if !ok || e.IsDir {
			continue
		}
		if e.Stripped {
			pkg.Stripped = append(pkg.Stripped, e.Rel)
			continue
		}

		data, n, err := readEntry(f)
		if err != nil {
			switch {
			case errors.Is(err, errHeaderLied):
				res.Add(SeverityError, "header_size_mismatch",
					fmt.Sprintf("%s contains more data than its headers declare", e.Rel), At(e.Rel, 0),
					Hint("The archive was crafted to under-report this entry's size."))
			case errors.Is(err, errMetadataMismatch):
				res.Add(SeverityError, "metadata_mismatch",
					fmt.Sprintf("%s: the archive's records disagree with its contents", e.Rel), At(e.Rel, 0),
					Hint("Local header, central directory and data must agree. Re-create the archive."))
			default:
				res.Add(SeverityError, "inflate_failed",
					fmt.Sprintf("%s could not be decompressed: %v", e.Rel, err), At(e.Rel, 0))
			}
			return nil, res, nil
		}

		written += n
		if int64(written) > MaxUncompressed {
			res.Errorf("expansion_too_large",
				"archive inflated past %s", humanBytes(MaxUncompressed))
			return nil, res, nil
		}

		mode := normalizeMode(e.Mode)
		if dir != "" {
			if err := writeFile(dir, e.Rel, data, mode); err != nil {
				return nil, res, fmt.Errorf("writing %s: %w", e.Rel, err)
			}
		}

		sum := sha256.Sum256(data)
		pkg.Files = append(pkg.Files, FileEntry{
			Path: e.Rel, Mode: mode, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]),
		})
		if inMemory {
			pkg.Contents[e.Rel] = data
		}

		switch e.Rel {
		case SkillFile:
			pkg.SkillMD = data
		case "README.md":
			pkg.Readme = data
		case "CHANGELOG.md":
			pkg.Changelog = data
		}
	}

	if len(pkg.SkillMD) == 0 {
		if !res.Has("missing_skill_md") {
			res.Errorf("missing_skill_md", "no %s at the package root", SkillFile)
		}
		return pkg, res, nil
	}
	if int64(len(pkg.SkillMD)) > MaxSkillMD {
		res.Add(SeverityError, "skill_md_too_large",
			fmt.Sprintf("%s is %s; the maximum is %s", SkillFile,
				humanBytes(int64(len(pkg.SkillMD))), humanBytes(MaxSkillMD)), At(SkillFile, 0))
		return nil, res, nil
	}

	man, mres := ParseManifest(pkg.SkillMD)
	res.Merge(mres)
	pkg.Manifest = man
	checkLayout(pkg, res)

	// A digest identifies a publishable version. Computing one for a package we
	// are rejecting would hand the caller an identifier for something that will
	// never exist.
	if res.OK() {
		pkg.Digest = TreeDigest(pkg.Files)
	}
	return pkg, res, nil
}

var (
	// errHeaderLied: the entry inflated past the size its headers declare. Only an
	// independent cap during inflate can detect this, because it means the
	// archive's metadata was crafted to under-report.
	errHeaderLied = errors.New("entry is larger than its header declares")
	// errMetadataMismatch: the archive's own records disagree with each other or
	// with the data (local header vs central directory, or a CRC failure).
	errMetadataMismatch = errors.New("archive metadata does not match its contents")
)

// classifyReadErr maps the zip package's errors onto what they actually mean for
// us. ErrFormat here is not "corrupt file" in the innocent sense: by this point
// the central directory has already parsed, so a failure opening or reading one
// entry means its local header contradicts the directory.
func classifyReadErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, zip.ErrFormat), errors.Is(err, zip.ErrChecksum), errors.Is(err, io.ErrUnexpectedEOF):
		return errMetadataMismatch
	default:
		return err
	}
}

// readEntry inflates one entry under a cap derived from its own declared size.
func readEntry(f *zip.File) ([]byte, uint64, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, 0, classifyReadErr(err)
	}
	defer rc.Close()

	declared := f.UncompressedSize64
	if int64(declared) > MaxSingleFile {
		return nil, 0, errHeaderLied
	}

	var buf bytes.Buffer
	buf.Grow(int(declared))
	// declared+1: reading one byte more than promised is how we detect the lie
	// rather than silently truncating to it.
	n, err := io.Copy(&buf, io.LimitReader(rc, int64(declared)+1))
	// Check the overrun BEFORE the error: a crafted archive can both lie about
	// the size and fail a checksum, and the lie is the more precise finding.
	if uint64(n) > declared {
		return nil, 0, errHeaderLied
	}
	if err != nil {
		return nil, 0, classifyReadErr(err)
	}
	// Draining to EOF is what makes archive/zip verify the CRC32; a mismatch
	// means the bytes are not what the archive claims, so it surfaces here.
	if _, err := io.Copy(io.Discard, rc); err != nil {
		return nil, 0, classifyReadErr(err)
	}
	return buf.Bytes(), uint64(n), nil
}

// normalizeMode collapses permissions to 0644 or 0755 so a creator's umask
// cannot change the tree digest, and so no exotic bits survive extraction.
func normalizeMode(m os.FileMode) uint32 {
	if m.Perm()&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

func writeFile(root, rel string, data []byte, mode uint32) error {
	// rel has already passed validatePath, but this is the step where a mistake
	// escapes the directory, so verify containment against the resolved path too.
	dst := filepath.Join(root, filepath.FromSlash(rel))
	if !strings.HasPrefix(dst, filepath.Clean(root)+string(os.PathSeparator)) {
		return fmt.Errorf("path %q escapes the destination", rel)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, os.FileMode(mode))
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// checkLayout reports files outside the recognised directories. Not an error —
// about a third of published skills are a single SKILL.md, and unusual layouts
// are common enough that blocking them would be stricter than the runtime.
func checkLayout(pkg *Package, res *Result) {
	var unknown []string
	for _, f := range pkg.Files {
		if !strings.Contains(f.Path, "/") {
			continue // top-level files are fine
		}
		known := false
		for _, d := range KnownDirs {
			if strings.HasPrefix(f.Path, d) {
				known = true
				break
			}
		}
		if !known {
			unknown = append(unknown, f.Path)
		}
	}
	if len(unknown) > 0 {
		res.Warnf("unrecognized_layout",
			"%d file(s) outside scripts/, references/, assets/ and examples/: %s",
			len(unknown), strings.Join(truncate(unknown, 3), ", "))
	}
}

func truncate(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(append([]string{}, s[:n]...), "...")
}

// zip64Present looks for the ZIP64 end-of-central-directory locator. ZIP64 is
// only needed above 4 GB, so at a 5 MB cap its presence signals a non-standard
// writer and buys us nothing but parser surface.
func zip64Present(ra io.ReaderAt, size int64) bool {
	const maxTail = 1<<16 + 64
	n := int64(maxTail)
	if size < n {
		n = size
	}
	buf := make([]byte, n)
	if _, err := ra.ReadAt(buf, size-n); err != nil && err != io.EOF {
		return false
	}
	return bytes.Contains(buf, []byte{'P', 'K', 0x06, 0x07})
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
