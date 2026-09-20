package skillpkg

// Limits are sized from measurement, not intuition: across 31 published skills in the
// official marketplace the median packaged size is 15 KB, p90 is 32 KB and the largest
// is 74 KB across 18 files. The hard caps below leave ~68x headroom over the largest
// real skill while keeping the worst case an attacker can inflict small and bounded.
//
// See docs/11-zip-ingest.md.
const (
	// Hard caps. Exceeding any of these fails ingest.
	MaxCompressed   int64 = 5 << 20  // 5 MiB — the .skill zip on the wire
	MaxUncompressed int64 = 25 << 20 // 25 MiB — sum of all inflated entries
	MaxRatio              = 50.0     // uncompressed / compressed
	MaxFiles              = 500
	MaxSingleFile   int64 = 4 << 20
	MaxSkillMD      int64 = 256 << 10
	MaxPathLen            = 255
	MaxPathDepth          = 12

	// Soft thresholds. These never block; they flag a package for a closer look
	// in the review queue, because at p90 = 32 KB anything near them is anomalous.
	WarnCompressed int64 = 1 << 20
	WarnFiles            = 100
	WarnSingleFile int64 = 1 << 20
	WarnRatio            = 20.0

	// Manifest limits, taken verbatim from the official skill validator.
	MaxNameLen        = 64
	MaxDescriptionLen = 1024
	MaxCompatLen      = 500

	// Ours: below this a description cannot route an agent or rank in search.
	MinDescriptionLen = 40
	// Soft budget. The cap is 1024, but a description is resident in context for
	// every installed skill in every conversation, so spending it is not free.
	SoftDescriptionLen = 500
)

// SkillFile is the one required member of every package.
const SkillFile = "SKILL.md"

// Directories the spec recognises. Files outside these are allowed but reported.
var KnownDirs = []string{"scripts/", "references/", "assets/", "examples/"}

// Stripped silently during packing, matching the official package_skill.py behaviour
// so that a package built by our CLI and one built by theirs agree.
// .aq is the CLI's record of where a working copy came from. Client-side
// bookkeeping, so it is stripped for the same reason __pycache__ is: it says
// something about one machine, not about the skill.
var StripDirs = []string{"__pycache__", "node_modules", ".aq"}
var StripFiles = []string{".DS_Store", "Thumbs.db"}
var StripGlobs = []string{"*.pyc", "*.pyo"}

// StripRootDirs are dropped only at the package root, not when nested deeper —
// again matching the official packager, which excludes a top-level evals/ directory.
var StripRootDirs = []string{"evals"}

// Rejected outright: these are either secrets or repository plumbing that has no
// business in a distributed package.
var ForbiddenPaths = []string{".git", ".env", ".ssh", ".aws", ".npmrc", ".pypirc", ".netrc"}
var ForbiddenGlobs = []string{"*.pem", "*.key", "*.p12", "*.pfx", "id_rsa*", "id_ed25519*", "*.env"}

// Nested archives are refused. A skill has no reason to bundle one, they defeat
// review (we will not open what we cannot scan), and recursive decompression is
// where zip bombs get their amplification.
var ArchiveExts = []string{
	".zip", ".gz", ".tgz", ".bz2", ".xz", ".7z", ".rar", ".tar", ".lz", ".lzma", ".zst", ".skill",
}
