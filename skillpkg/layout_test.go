package skillpkg

import (
	"bytes"
	"testing"
)

// These three checks are about the file around the entries rather than the
// entries themselves. Each is a way to build one file that two readers
// disagree about — and disagreement is the attack: the scanner reads one
// archive, the reviewer reads another, the agent installs a third.
//
// docs/11 promised all three long before any of them existed, which is the
// worst state for a security control to be in: documented, believed, absent.

func goodArchive(t *testing.T) []byte {
	t.Helper()
	return buildZip(t, []zentry{
		{name: "s/SKILL.md", data: []byte(goodSkillMD)},
		{name: "s/scripts/run.sh", data: []byte("#!/bin/sh\necho hi\n")},
	})
}

func TestTrailingDataIsRefused(t *testing.T) {
	archive := goodArchive(t)
	if res := inspect(t, archive); !res.OK() {
		t.Fatalf("the base archive is not clean: %v", res.Errors())
	}

	// A second file concatenated onto the first. A reader scanning backwards
	// for the end-of-central-directory finds one archive; a reader scanning
	// forwards finds the other.
	poisoned := append(append([]byte{}, archive...), []byte("PK\x03\x04 and then some")...)
	wantCode(t, inspect(t, poisoned), "trailing_data")
}

func TestPrependedDataIsRefused(t *testing.T) {
	// A file that is a GIF and a zip at the same time. Every entry in it is
	// honest; the file is not.
	poisoned := append([]byte("GIF89a\x01\x00\x01\x00\x00\x00\x00,"), goodArchive(t)...)
	wantCode(t, inspect(t, poisoned), "prepended_data")
}

// Two entries whose data occupies the same bytes. Every per-entry limit is
// satisfied because each entry really is small; the archive as a whole is not.
func TestOverlappingEntriesAreRefused(t *testing.T) {
	archive := goodArchive(t)

	// Overstate the first entry's compressed size in the central directory so
	// its span runs into the next entry's. The local headers are untouched, so
	// each entry still resolves to a real data offset.
	patched := overstateFirstCompressedSize(t, archive, 5000)
	wantCode(t, inspect(t, patched), "overlapping_entries")
}

// And the checks must not fire on an ordinary archive, or every publish stops.
func TestOrdinaryArchivePassesTheLayoutChecks(t *testing.T) {
	res := inspect(t, goodArchive(t))
	for _, v := range res.Violations {
		switch v.Code {
		case "trailing_data", "prepended_data", "overlapping_entries":
			t.Errorf("a normal archive was refused: %s — %s", v.Code, v.Message)
		}
	}
}

// overstateFirstCompressedSize rewrites the compressed-size field of the first
// central-directory record.
//
// Layout of a central directory file header: signature(4) version(2)
// versionNeeded(2) flags(2) method(2) modTime(2) modDate(2) crc32(4)
// compressedSize(4) — so the field begins 20 bytes in.
func overstateFirstCompressedSize(t *testing.T, archive []byte, to uint32) []byte {
	t.Helper()
	const cdSignature = "PK\x01\x02"
	i := bytes.Index(archive, []byte(cdSignature))
	if i < 0 {
		t.Fatal("no central directory in the test archive")
	}
	out := append([]byte{}, archive...)
	binaryPutUint32(out[i+20:i+24], to)
	return out
}

func binaryPutUint32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}
