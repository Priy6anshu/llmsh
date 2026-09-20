package skillpkg

import (
	"fmt"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// validatePath rejects archive entry names that are unsafe to extract or
// misleading to read. Both matter: the first protects the filesystem, the second
// protects the human reviewer, who is the only safety gate in v1.
func validatePath(name string) error {
	if name == "" {
		return fmt.Errorf("empty path")
	}
	if !utf8.ValidString(name) {
		return fmt.Errorf("path is not valid UTF-8")
	}
	if len(name) > MaxPathLen {
		return fmt.Errorf("path is %d bytes; the maximum is %d", len(name), MaxPathLen)
	}
	if strings.ContainsRune(name, 0) {
		return fmt.Errorf("path contains a NUL byte")
	}
	if strings.Contains(name, `\`) {
		return fmt.Errorf(`path contains a backslash; use / as the separator`)
	}
	if strings.HasPrefix(name, "/") {
		return fmt.Errorf("absolute path")
	}
	// Windows drive letters and UNC paths.
	if len(name) >= 2 && name[1] == ':' {
		return fmt.Errorf("path contains a drive letter")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("path contains a control character (U+%04X)", r)
		}
		if isDeceptiveRune(r) {
			return fmt.Errorf("path contains a bidirectional or zero-width character (U+%04X) "+
				"which makes the filename display differently from what it is", r)
		}
		if unicode.Is(unicode.Cf, r) {
			return fmt.Errorf("path contains a Unicode format character (U+%04X)", r)
		}
	}
	// `..` in any position, before or after cleaning.
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return fmt.Errorf("path escapes the package root")
		}
	}
	cleaned := path.Clean(name)
	if cleaned != name && cleaned+"/" != name {
		return fmt.Errorf("path is not in canonical form (want %q)", cleaned)
	}
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return fmt.Errorf("path escapes the package root")
	}
	if depth := strings.Count(strings.TrimSuffix(cleaned, "/"), "/") + 1; depth > MaxPathDepth {
		return fmt.Errorf("path is %d levels deep; the maximum is %d", depth, MaxPathDepth)
	}
	return nil
}

// isDeceptiveRune covers the characters that let a filename render as something
// other than what it is: RTL/LTR overrides and isolates, and zero-width joiners.
// "report.dp‮mk.txt" displays as "report.txt.kmd". Legitimate skills do not
// contain these, so their presence is close to a smoking gun.
func isDeceptiveRune(r rune) bool {
	switch {
	case r >= 0x200B && r <= 0x200F: // ZWSP, ZWNJ, ZWJ, LRM, RLM
		return true
	case r >= 0x202A && r <= 0x202E: // LRE, RLE, PDF, LRO, RLO
		return true
	case r >= 0x2066 && r <= 0x2069: // LRI, RLI, FSI, PDI
		return true
	case r == 0xFEFF: // BOM / zero-width no-break space
		return true
	}
	return false
}

func hasArchiveExt(name string) bool {
	lower := strings.ToLower(name)
	for _, e := range ArchiveExts {
		if strings.HasSuffix(lower, e) {
			return true
		}
	}
	return false
}

func isForbiddenPath(name string) bool {
	segs := strings.Split(name, "/")
	for _, seg := range segs {
		for _, f := range ForbiddenPaths {
			if strings.EqualFold(seg, f) {
				return true
			}
		}
	}
	base := segs[len(segs)-1]
	for _, g := range ForbiddenGlobs {
		if ok, _ := path.Match(g, strings.ToLower(base)); ok {
			return true
		}
	}
	return false
}

// isStripped reports paths the official packager also drops, so a package built
// by our CLI and one built by theirs agree. rel is relative to the package root.
func isStripped(rel string) bool {
	segs := strings.Split(rel, "/")
	for _, seg := range segs {
		for _, d := range StripDirs {
			if seg == d {
				return true
			}
		}
	}
	if len(segs) > 1 {
		for _, d := range StripRootDirs {
			if segs[0] == d {
				return true
			}
		}
	}
	base := segs[len(segs)-1]
	for _, f := range StripFiles {
		if base == f {
			return true
		}
	}
	for _, g := range StripGlobs {
		if ok, _ := path.Match(g, base); ok {
			return true
		}
	}
	return false
}
