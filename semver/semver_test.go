package semver

import "testing"

func TestParseRejectsJunk(t *testing.T) {
	for _, s := range []string{"1", "1.2", "v1.2.3", "1.2.3.4", "01.2.3", "", "latest", "1.2.x"} {
		if IsValid(s) {
			t.Errorf("%q should not be valid semver", s)
		}
	}
	for _, s := range []string{"0.0.1", "1.2.3", "1.2.3-beta.1", "1.2.3+build.5", "10.20.30"} {
		if !IsValid(s) {
			t.Errorf("%q should be valid semver", s)
		}
	}
}

func TestOrdering(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "2.0.0", -1},
		{"2.0.0", "2.1.0", -1},
		{"2.1.0", "2.1.1", -1},
		{"1.0.0", "1.0.0", 0},
		// Build metadata is excluded from ordering and equality.
		{"1.0.0+a", "1.0.0+b", 0},
		// A prerelease sorts before its release.
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"1.0.0-alpha.1", "1.0.0-alpha.2", -1},
		{"1.0.0-alpha.9", "1.0.0-alpha.10", -1}, // numeric, not lexical
		{"1.0.0-alpha", "1.0.0-alpha.1", -1},    // fewer identifiers rank lower
		{"1.0.0-1", "1.0.0-alpha", -1},          // numeric ranks below alphanumeric
	}
	for _, c := range cases {
		a, _ := Parse(c.a)
		b, _ := Parse(c.b)
		if got := Compare(a, b); got != c.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := Compare(b, a); got != -c.want {
			t.Errorf("Compare(%s, %s) = %d, want %d (asymmetry)", c.b, c.a, got, -c.want)
		}
	}
}

// TestLatestIgnoresRecency is the rule that makes backporting safe: shipping
// 1.4.2 after 2.0.0 is legitimate and must not move what `install` returns.
func TestLatestIgnoresRecency(t *testing.T) {
	got, ok := Latest([]string{"1.0.0", "2.0.0", "1.4.2"})
	if !ok || got != "2.0.0" {
		t.Errorf("Latest = %q %v, want 2.0.0", got, ok)
	}
}

func TestLatestExcludesPrereleases(t *testing.T) {
	got, ok := Latest([]string{"1.0.0", "2.0.0-beta.1"})
	if !ok || got != "1.0.0" {
		t.Errorf("Latest = %q, want 1.0.0 — a prerelease must never resolve as latest", got)
	}
	if _, ok := Latest([]string{"1.0.0-beta.1"}); ok {
		t.Error("a set of only prereleases has no latest")
	}
}

func TestLatestEmpty(t *testing.T) {
	if _, ok := Latest(nil); ok {
		t.Error("no versions means no latest")
	}
}

func TestNextPatch(t *testing.T) {
	if got := NextPatch("1.2.3"); got != "1.2.4" {
		t.Errorf("NextPatch = %q", got)
	}
	if got := NextPatch(""); got != "0.1.0" {
		t.Errorf("NextPatch of nothing = %q, want 0.1.0", got)
	}
}
