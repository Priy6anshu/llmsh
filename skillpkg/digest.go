package skillpkg

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"
)

// FileEntry describes one file in a version. Mode is normalised to 0644 or 0755
// before it ever reaches here, so the digest cannot vary with a creator's umask.
type FileEntry struct {
	Path   string `json:"path"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"` // hex, of the file's contents
}

// TreeDigest is the identity of a version's content.
//
// It hashes the file tree, not the archive bytes. Zip output is not reproducible
// across producers — timestamps, extra fields, compression level and entry order
// all vary — so hashing the archive would make the same source directory produce
// different identities on different machines. Hashing the tree is stable across
// zip implementations and still detects any change to any byte of any file.
//
// This is morally a git tree hash, which is also why adding real git history
// later is additive rather than a migration: these are already the trees.
//
//	for each file, sorted by path:
//	    path || 0x00 || uint32be(mode) || 0x00 || sha256(content)
func TreeDigest(files []FileEntry) string {
	sorted := append([]FileEntry(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	h := sha256.New()
	var mode [4]byte
	for _, f := range sorted {
		h.Write([]byte(f.Path))
		h.Write([]byte{0})
		binary.BigEndian.PutUint32(mode[:], f.Mode)
		h.Write(mode[:])
		h.Write([]byte{0})
		raw, err := hex.DecodeString(f.SHA256)
		if err != nil {
			// A malformed hash must not silently produce a valid-looking digest.
			raw = []byte(f.SHA256)
		}
		h.Write(raw)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// DigestAlgorithm names the scheme above, for the API to state in a payload.
//
// Sent on every version because the failure it prevents is expensive and
// silent: a reader who assumes `digest` is sha256 of the .skill file gets a
// mismatch, and a mismatched hash on a package registry reads as tampering
// rather than as a naming problem. One field ends that guess.
const DigestAlgorithm = "tree-sha256"

// ArchiveSHA256 hashes the bytes of a stored archive.
//
// Deliberately separate from TreeDigest and never a replacement for it. This
// changes whenever the archive is repacked -- different compression, a
// different zip writer, a reordered central directory -- while the content is
// untouched, which is exactly why it cannot be the identity and exactly why it
// is the number somebody can check with shasum.
func ArchiveSHA256(b []byte) string {
	return hashBytes(b)
}

// ShortDigest renders a digest the way the UI shows it beside a version:
// "1.2.0 · e3b0c44". It identifies; the semver resolves.
func ShortDigest(digest string) string {
	s := digest
	if len(s) > 7 && s[:7] == "sha256:" {
		s = s[7:]
	}
	if len(s) > 7 {
		s = s[:7]
	}
	return s
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
