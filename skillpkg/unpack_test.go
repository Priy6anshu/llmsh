package skillpkg

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"testing"
)

const goodSkillMD = `---
name: pdf-form-filler
description: >
  Fills, flattens and validates AcroForm and XFA PDF forms. Use this skill whenever the
  user mentions "fill a PDF form", "flatten a PDF" or extracting field values, even if
  they do not say the word form.
license: MIT
metadata:
  skillhub:
    version: 1.2.0
    categories: [documents]
    keywords: [pdf, forms, acroform]
---

# PDF Form Filler

Fill and flatten PDF forms.
`

type zentry struct {
	name    string
	data    []byte
	mode    os.FileMode
	flags   uint16
	setMode bool
}

func buildZip(t *testing.T, entries []zentry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Deflate, Flags: e.flags}
		if e.setMode {
			hdr.SetMode(e.mode)
		} else {
			hdr.SetMode(0o644)
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("create %s: %v", e.name, err)
		}
		if _, err := w.Write(e.data); err != nil {
			t.Fatalf("write %s: %v", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func validZip(t *testing.T) []byte {
	return buildZip(t, []zentry{
		{name: "pdf-form-filler/SKILL.md", data: []byte(goodSkillMD)},
		{name: "pdf-form-filler/README.md", data: []byte("# readme\n" + strings.Repeat("word ", 250))},
		{name: "pdf-form-filler/scripts/fill.py", data: []byte("print('hi')\n")},
	})
}

func inspect(t *testing.T, z []byte) *Result {
	t.Helper()
	_, res := Inspect(bytes.NewReader(z), int64(len(z)))
	return res
}

// wantCode asserts a specific rejection. Asserting on the code rather than on
// "some error happened" is what stops a test passing for the wrong reason — a
// path-traversal fixture that gets rejected for being too large proves nothing.
func wantCode(t *testing.T, res *Result, code string) {
	t.Helper()
	if res.OK() {
		t.Fatalf("expected rejection %q, but the archive was accepted", code)
	}
	for _, v := range res.Errors() {
		if v.Code == code {
			return
		}
	}
	var got []string
	for _, v := range res.Errors() {
		got = append(got, v.Code)
	}
	t.Fatalf("expected rejection %q, got %v", code, got)
}

func TestValidArchives(t *testing.T) {
	t.Run("single root directory", func(t *testing.T) {
		if res := inspect(t, validZip(t)); !res.OK() {
			t.Fatalf("valid archive rejected: %s", res)
		}
	})

	t.Run("flat layout, no root directory", func(t *testing.T) {
		z := buildZip(t, []zentry{
			{name: "SKILL.md", data: []byte(goodSkillMD)},
			{name: "scripts/fill.py", data: []byte("x")},
		})
		if res := inspect(t, z); !res.OK() {
			t.Fatalf("flat archive rejected: %s", res)
		}
	})

	t.Run("single SKILL.md only", func(t *testing.T) {
		// About a third of published skills are exactly this. It must be a
		// first-class shape, not a degenerate case.
		z := buildZip(t, []zentry{{name: "SKILL.md", data: []byte(goodSkillMD)}})
		if res := inspect(t, z); !res.OK() {
			t.Fatalf("single-file skill rejected: %s", res)
		}
	})
}

func TestHostileArchives(t *testing.T) {
	cases := []struct {
		name    string
		entries []zentry
		code    string
	}{
		{
			name: "path traversal",
			entries: []zentry{
				{name: "s/SKILL.md", data: []byte(goodSkillMD)},
				{name: "s/../../../etc/passwd", data: []byte("root:x:0:0")},
			},
			code: "unsafe_path",
		},
		{
			name: "absolute path",
			entries: []zentry{
				{name: "s/SKILL.md", data: []byte(goodSkillMD)},
				{name: "/etc/shadow", data: []byte("x")},
			},
			code: "unsafe_path",
		},
		{
			name: "windows separator",
			entries: []zentry{
				{name: "s/SKILL.md", data: []byte(goodSkillMD)},
				{name: `s\..\..\evil.txt`, data: []byte("x")},
			},
			code: "unsafe_path",
		},
		{
			name: "bidi override in filename",
			entries: []zentry{
				{name: "s/SKILL.md", data: []byte(goodSkillMD)},
				// renders as "report.txt.kmd" in any file listing
				{name: "s/report.dp‮mk.txt", data: []byte("x")},
			},
			code: "unsafe_path",
		},
		{
			name: "control character in filename",
			entries: []zentry{
				{name: "s/SKILL.md", data: []byte(goodSkillMD)},
				{name: "s/evil\x07.md", data: []byte("x")},
			},
			code: "unsafe_path",
		},
		{
			name: "path too deep",
			entries: []zentry{
				{name: "s/SKILL.md", data: []byte(goodSkillMD)},
				{name: "s/" + strings.Repeat("a/", 14) + "deep.md", data: []byte("x")},
			},
			code: "unsafe_path",
		},
		{
			name: "symlink entry",
			entries: []zentry{
				{name: "s/SKILL.md", data: []byte(goodSkillMD)},
				{name: "s/link", data: []byte("/etc/passwd"), mode: os.ModeSymlink | 0o777, setMode: true},
			},
			code: "symlink_entry",
		},
		{
			name: "setuid bit",
			entries: []zentry{
				{name: "s/SKILL.md", data: []byte(goodSkillMD)},
				{name: "s/scripts/root.sh", data: []byte("#!/bin/sh\n"), mode: os.ModeSetuid | 0o755, setMode: true},
			},
			code: "setuid_entry",
		},
		{
			name: "encrypted entry",
			entries: []zentry{
				{name: "s/SKILL.md", data: []byte(goodSkillMD)},
				{name: "s/secret.md", data: []byte("x"), flags: 0x1},
			},
			code: "encrypted_entry",
		},
		{
			name: "nested archive",
			entries: []zentry{
				{name: "s/SKILL.md", data: []byte(goodSkillMD)},
				{name: "s/assets/bundle.zip", data: []byte("PK\x03\x04")},
			},
			code: "nested_archive",
		},
		{
			name: "duplicate path",
			entries: []zentry{
				{name: "s/SKILL.md", data: []byte(goodSkillMD)},
				{name: "s/a.md", data: []byte("first")},
				{name: "s/a.md", data: []byte("second")},
			},
			code: "duplicate_path",
		},
		{
			name: "case-colliding paths",
			entries: []zentry{
				{name: "s/SKILL.md", data: []byte(goodSkillMD)},
				{name: "s/Notes.md", data: []byte("a")},
				{name: "s/notes.md", data: []byte("b")},
			},
			code: "case_colliding_path",
		},
		{
			name: "secret material",
			entries: []zentry{
				{name: "s/SKILL.md", data: []byte(goodSkillMD)},
				{name: "s/id_rsa", data: []byte("-----BEGIN OPENSSH PRIVATE KEY-----")},
			},
			code: "forbidden_path",
		},
		{
			name: "git directory",
			entries: []zentry{
				{name: "s/SKILL.md", data: []byte(goodSkillMD)},
				{name: "s/.git/config", data: []byte("[core]")},
			},
			code: "forbidden_path",
		},
		{
			name:    "no SKILL.md",
			entries: []zentry{{name: "s/README.md", data: []byte("# hi")}},
			code:    "missing_skill_md",
		},
		{
			name: "SKILL.md nested too deep",
			entries: []zentry{
				{name: "s/inner/SKILL.md", data: []byte(goodSkillMD)},
				{name: "s/inner/README.md", data: []byte("# hi")},
			},
			code: "missing_skill_md",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantCode(t, inspect(t, buildZip(t, tc.entries)), tc.code)
		})
	}
}

func TestTooManyFiles(t *testing.T) {
	entries := []zentry{{name: "s/SKILL.md", data: []byte(goodSkillMD)}}
	for i := 0; i < MaxFiles+1; i++ {
		entries = append(entries, zentry{name: fmt.Sprintf("s/assets/f%04d.txt", i), data: []byte("x")})
	}
	wantCode(t, inspect(t, buildZip(t, entries)), "too_many_files")
}

func TestSingleFileTooLarge(t *testing.T) {
	z := buildZip(t, []zentry{
		{name: "s/SKILL.md", data: []byte(goodSkillMD)},
		{name: "s/assets/big.bin", data: make([]byte, MaxSingleFile+1)},
	})
	wantCode(t, inspect(t, z), "file_too_large")
}

// TestDeclaredExpansionBomb is the case the central-directory pass exists for:
// the archive is tiny, but its own inventory admits it expands past the cap. We
// refuse it having inflated nothing.
func TestDeclaredExpansionBomb(t *testing.T) {
	entries := []zentry{{name: "s/SKILL.md", data: []byte(goodSkillMD)}}
	for i := 0; i < 8; i++ {
		entries = append(entries, zentry{
			name: fmt.Sprintf("s/assets/pad%d.bin", i),
			data: make([]byte, 3_500_000), // 8 x 3.5 MB = 28 MB > 25 MB cap
		})
	}
	z := buildZip(t, entries)
	if int64(len(z)) > MaxCompressed {
		t.Fatalf("fixture is %d compressed bytes, over the cap — test would pass for the wrong reason", len(z))
	}
	wantCode(t, inspect(t, z), "declared_expansion_too_large")
}

func TestCompressionRatio(t *testing.T) {
	entries := []zentry{{name: "s/SKILL.md", data: []byte(goodSkillMD)}}
	for i := 0; i < 5; i++ {
		entries = append(entries, zentry{
			name: fmt.Sprintf("s/assets/z%d.bin", i),
			data: make([]byte, 4_000_000), // 20 MB total, under the cap; ratio is the problem
		})
	}
	wantCode(t, inspect(t, buildZip(t, entries)), "compression_ratio")
}

// TestLyingCentralDirectory is the reason Layer 3 exists. Everything Inspect
// learns is the archive's own metadata, and metadata is attacker-controlled. Here
// the central directory under-reports a file's size; only inflating under an
// independent cap catches it.
func TestLyingCentralDirectory(t *testing.T) {
	payload := bytes.Repeat([]byte("A"), 100_000)
	z := buildZip(t, []zentry{
		{name: "s/SKILL.md", data: []byte(goodSkillMD)},
		{name: "s/assets/liar.bin", data: payload},
	})

	patched := patchCDUncompressedSize(t, z, 16)

	// The lie is invisible to the inventory pass, by construction: the central
	// directory is exactly what that pass reads.
	if _, res := Inspect(bytes.NewReader(patched), int64(len(patched))); !res.OK() {
		t.Fatalf("expected the central-directory pass to be fooled: %s", res)
	}

	// archive/zip cross-checks the local file header against the directory, so a
	// CD-only patch is caught as an internal contradiction.
	dir := t.TempDir()
	_, res, err := Unpack(context.Background(), bytes.NewReader(patched), int64(len(patched)), dir)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	wantCode(t, res, "metadata_mismatch")
}

// TestConsistentlyLyingHeaders covers an archive that is internally consistent
// about a false size — the case a cross-check between records cannot catch.
//
// Worth knowing for anyone maintaining this: archive/zip's checksumReader tracks
// bytes read and returns ErrFormat as soon as they exceed UncompressedSize64, so
// the standard library already refuses to hand us more data than an entry
// declares. Our own declared+1 limit in readEntry is therefore a second line of
// defense rather than the first, and it stays: it costs nothing, it keeps the
// guarantee if the reader is ever swapped, and relying on an implementation
// detail of a dependency for a security property is not a plan.
//
// The assertion is on the property, not the message: a lying archive is refused
// and nothing oversized reaches disk. Which of the two mismatch codes fires is
// an implementation detail of whoever notices first.
func TestConsistentlyLyingHeaders(t *testing.T) {
	payload := bytes.Repeat([]byte("A"), 100_000)
	z := buildZip(t, []zentry{
		{name: "s/SKILL.md", data: []byte(goodSkillMD)},
		{name: "s/assets/liar.bin", data: payload},
	})

	const lie = 16
	patched := patchDeclaredSize(t, z, lie)

	if _, res := Inspect(bytes.NewReader(patched), int64(len(patched))); !res.OK() {
		t.Fatalf("a self-consistent archive should pass the inventory pass: %s", res)
	}

	dir := t.TempDir()
	pkg, res, err := Unpack(context.Background(), bytes.NewReader(patched), int64(len(patched)), dir)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	if res.OK() {
		t.Fatal("an archive that lies about its size was accepted")
	}
	if !res.Has("header_size_mismatch") && !res.Has("metadata_mismatch") {
		t.Fatalf("expected a size/metadata rejection, got: %s", res)
	}
	if pkg != nil {
		t.Error("a rejected archive must not yield a package")
	}
	// Nothing oversized may reach disk. The liar declared 16 bytes and holds
	// 100 KB; if either number had been written out, the defense did not work.
	if data, err := os.ReadFile(dir + "/assets/liar.bin"); err == nil {
		t.Errorf("rejected entry was written to disk (%d bytes)", len(data))
	}
}

// patchDeclaredSize makes an archive internally consistent about a FALSE
// uncompressed size for its last entry.
//
// Go's zip.Writer streams, so it sets general-purpose flag bit 3 and writes the
// real sizes in a data descriptor that trails the compressed data, with the
// local header's size fields left at zero. The reader cross-checks the central
// directory against that descriptor — which is why patching one record alone is
// detected as a contradiction. Patching both leaves an archive that is perfectly
// self-consistent and simply lying, which is the case our independent inflate cap
// exists to catch.
func patchDeclaredSize(t *testing.T, z []byte, size uint32) []byte {
	t.Helper()
	cd := bytes.LastIndex(z, []byte{'P', 'K', 0x01, 0x02})
	if cd < 0 {
		t.Fatal("no central directory record found")
	}
	csize := binary.LittleEndian.Uint32(z[cd+20:])
	local := int(binary.LittleEndian.Uint32(z[cd+42:]))
	if !bytes.Equal(z[local:local+4], []byte{'P', 'K', 0x03, 0x04}) {
		t.Fatalf("no local file header at offset %d", local)
	}
	fnLen := int(binary.LittleEndian.Uint16(z[local+26:]))
	exLen := int(binary.LittleEndian.Uint16(z[local+28:]))

	out := append([]byte(nil), z...)
	binary.LittleEndian.PutUint32(out[cd+24:], size) // central directory

	// Data descriptor: PK\x07\x08, crc32(4), compressed(4), uncompressed(4).
	dd := local + 30 + fnLen + exLen + int(csize)
	if dd+16 > len(out) || !bytes.Equal(out[dd:dd+4], []byte{'P', 'K', 0x07, 0x08}) {
		t.Fatalf("no data descriptor at offset %d", dd)
	}
	binary.LittleEndian.PutUint32(out[dd+12:], size)
	return out
}

// patchCDUncompressedSize rewrites the uncompressed-size field of the LAST
// central-directory record. CD layout: signature(4) ... crc32 at 16,
// compressed size at 20, uncompressed size at 24.
func patchCDUncompressedSize(t *testing.T, z []byte, size uint32) []byte {
	t.Helper()
	sig := []byte{'P', 'K', 0x01, 0x02}
	idx := bytes.LastIndex(z, sig)
	if idx < 0 {
		t.Fatal("no central directory record found")
	}
	out := append([]byte(nil), z...)
	binary.LittleEndian.PutUint32(out[idx+24:idx+28], size)
	return out
}

func TestUnpackValid(t *testing.T) {
	dir := t.TempDir()
	z := validZip(t)
	pkg, res, err := Unpack(context.Background(), bytes.NewReader(z), int64(len(z)), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Fatalf("valid package rejected: %s", res)
	}
	if pkg.Manifest.Name != "pdf-form-filler" {
		t.Errorf("name = %q", pkg.Manifest.Name)
	}
	if pkg.Manifest.Hub.Version != "1.2.0" {
		t.Errorf("hub version = %q", pkg.Manifest.Hub.Version)
	}
	if len(pkg.Files) != 3 {
		t.Errorf("files = %d, want 3", len(pkg.Files))
	}
	if !strings.HasPrefix(pkg.Digest, "sha256:") {
		t.Errorf("digest = %q", pkg.Digest)
	}
	if _, err := os.Stat(dir + "/scripts/fill.py"); err != nil {
		t.Errorf("extracted file missing: %v", err)
	}
	// The root directory must be stripped on extraction.
	if _, err := os.Stat(dir + "/pdf-form-filler"); err == nil {
		t.Error("root directory was not stripped")
	}
}

func TestStrippedFiles(t *testing.T) {
	z := buildZip(t, []zentry{
		{name: "s/SKILL.md", data: []byte(goodSkillMD)},
		{name: "s/.DS_Store", data: []byte("junk")},
		{name: "s/__pycache__/x.pyc", data: []byte("junk")},
		{name: "s/evals/cases.json", data: []byte("[]")},
	})
	dir := t.TempDir()
	pkg, res, err := Unpack(context.Background(), bytes.NewReader(z), int64(len(z)), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Fatalf("rejected: %s", res)
	}
	if len(pkg.Files) != 1 {
		t.Errorf("kept %d files, want 1 (SKILL.md); stripped=%v", len(pkg.Files), pkg.Stripped)
	}
	if len(pkg.Stripped) != 3 {
		t.Errorf("stripped %v, want 3 entries", pkg.Stripped)
	}
}

// TestReportsAllProblemsAtOnce pins the creator-facing behaviour: a package with
// both a rejected entry and a broken manifest must surface both in one pass.
// Reporting one class of problem per upload turns fixing a package into
// whack-a-mole, which is exactly how a publish flow loses people.
func TestReportsAllProblemsAtOnce(t *testing.T) {
	z := buildZip(t, []zentry{
		{name: "s/SKILL.md", data: []byte("---\nname: Bad_Name\ndescription: does pdf stuff\nbogus: 1\n---\nbody")},
		{name: "s/id_rsa", data: []byte("PRIVATE KEY")},
	})
	dir := t.TempDir()
	pkg, res, err := Unpack(context.Background(), bytes.NewReader(z), int64(len(z)), dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK() {
		t.Fatal("package should be rejected")
	}

	codes := map[string]bool{}
	for _, v := range res.Errors() {
		codes[v.Code] = true
	}
	for _, want := range []string{"forbidden_path", "invalid_name", "unknown_frontmatter_key"} {
		if !codes[want] {
			t.Errorf("missing %q; a single upload should surface every problem, got %v", want, codes)
		}
	}

	// Reading far enough to report must not write anything, and must not hand
	// back a digest for a version that will never exist.
	if pkg != nil && pkg.Digest != "" {
		t.Errorf("rejected package was given a digest: %s", pkg.Digest)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("rejected package wrote %d entries to disk", len(entries))
	}
}

// TestFatalStopsEarly is the other half: some problems mean the archive cannot
// be read further, and we must not try.
func TestFatalStopsEarly(t *testing.T) {
	entries := []zentry{{name: "s/SKILL.md", data: []byte(goodSkillMD)}}
	for i := 0; i < 5; i++ {
		entries = append(entries, zentry{
			name: fmt.Sprintf("s/assets/z%d.bin", i), data: make([]byte, 4_000_000)})
	}
	z := buildZip(t, entries)
	pkg, res, err := Unpack(context.Background(), bytes.NewReader(z), int64(len(z)), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if pkg != nil {
		t.Error("a fatal rejection must not return a package")
	}
	wantCode(t, res, "compression_ratio")
}
