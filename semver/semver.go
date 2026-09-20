// Package semver implements the version ordering a catalogue needs.
//
// Ordering is the reason versions are semver rather than content hashes: a
// consumer has to be able to ask "is this newer" and "is this a safe upgrade",
// and a hash answers neither. See docs/04.
package semver

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Strict semver 2.0.0. Build metadata is captured but, per the spec, ignored for
// both ordering and equality.
var re = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)` +
	`(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?` +
	`(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)

type Version struct {
	Major, Minor, Patch int
	Prerelease          string // "" for a stable release
	Build               string
	Raw                 string
}

func Parse(s string) (Version, error) {
	m := re.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, fmt.Errorf("%q is not valid semver (expected MAJOR.MINOR.PATCH)", s)
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	return Version{Major: maj, Minor: min, Patch: pat, Prerelease: m[4], Build: m[5], Raw: s}, nil
}

func IsValid(s string) bool { _, err := Parse(s); return err == nil }

// IsStable reports whether this version may be resolved as `latest`. A
// prerelease never can: publishing 2.0.0-beta.1 must not move what `install`
// gives everyone.
func (v Version) IsStable() bool { return v.Prerelease == "" }

// Compare returns -1, 0 or +1. Build metadata is excluded, as the spec requires.
func Compare(a, b Version) int {
	if c := cmpInt(a.Major, b.Major); c != 0 {
		return c
	}
	if c := cmpInt(a.Minor, b.Minor); c != 0 {
		return c
	}
	if c := cmpInt(a.Patch, b.Patch); c != 0 {
		return c
	}
	// A prerelease sorts BEFORE its matching release: 1.0.0-rc.1 < 1.0.0.
	switch {
	case a.Prerelease == "" && b.Prerelease == "":
		return 0
	case a.Prerelease == "":
		return 1
	case b.Prerelease == "":
		return -1
	}
	return comparePrerelease(a.Prerelease, b.Prerelease)
}

// comparePrerelease follows the spec's dot-separated identifier rules: numeric
// identifiers compare numerically and rank below alphanumeric ones, and a larger
// set of identifiers outranks a smaller one when all preceding are equal.
func comparePrerelease(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, y := as[i], bs[i]
		if x == y {
			continue
		}
		xn, xIsNum := strconv.Atoi(x)
		yn, yIsNum := strconv.Atoi(y)
		switch {
		case xIsNum == nil && yIsNum == nil:
			return cmpInt(xn, yn)
		case xIsNum == nil:
			return -1 // numeric identifiers rank lower than alphanumeric
		case yIsNum == nil:
			return 1
		default:
			return strings.Compare(x, y)
		}
	}
	return cmpInt(len(as), len(bs))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Latest picks what `latest` resolves to from a set of candidate versions.
//
// It is computed by semver, never by recency: publishing a 1.4.2 backport after
// 2.0.0 is legitimate and must not move `latest`. Prereleases are excluded
// entirely. Callers pass only versions that are approved and not yanked.
func Latest(versions []string) (string, bool) {
	var best Version
	found := false
	for _, s := range versions {
		v, err := Parse(s)
		if err != nil || !v.IsStable() {
			continue
		}
		if !found || Compare(v, best) > 0 {
			best, found = v, true
		}
	}
	if !found {
		return "", false
	}
	return best.Raw, true
}

// NextPatch suggests the next version for a creator who did not name one.
func NextPatch(current string) string {
	v, err := Parse(current)
	if err != nil {
		return "0.1.0"
	}
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch+1)
}
