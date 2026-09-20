package skillpkg

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// corpusDir points at the installed official plugin marketplace. The corpus is
// the whole point of this test: a validator that rejects real, working, published
// skills is worse than no validator, because creators will simply publish
// elsewhere. Skipped when the marketplace is not installed.
func corpusDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	d := filepath.Join(home, ".claude", "plugins", "marketplaces", "claude-plugins-official")
	if _, err := os.Stat(d); err != nil {
		t.Skip("official marketplace not installed; skipping corpus test")
	}
	return d
}

func findSkills(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !fi.IsDir() && fi.Name() == SkillFile {
			out = append(out, filepath.Dir(p))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// TestCorpusNoFalseRejections is the guard on our central promise: if a package
// passes the official validator, we must never reject it. Anything we dislike is
// a warning.
func TestCorpusNoFalseRejections(t *testing.T) {
	root := corpusDir(t)
	skills := findSkills(t, root)
	if len(skills) == 0 {
		t.Skip("no skills found in corpus")
	}
	t.Logf("corpus: %d published skills", len(skills))

	var rejected []string
	for _, dir := range skills {
		name := filepath.Base(dir)
		man, res, err := ValidateDir(dir)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if errs := res.Errors(); len(errs) > 0 {
			// A rejection that mirrors an official rule is correct — that package
			// would fail the official validator too. Anything else means we are
			// stricter than the runtime, which is the failure this test exists to
			// catch.
			var ours []string
			for _, e := range errs {
				if !OfficialRuleCodes[e.Code] {
					ours = append(ours, e.Code+": "+e.Message)
				}
			}
			if len(ours) > 0 {
				rejected = append(rejected, name+" -> "+strings.Join(ours, "; "))
			} else {
				t.Logf("%s: correctly rejected by an official rule (%s)", name, errs[0].Code)
			}
			continue
		}
		if man.Name == "" {
			t.Errorf("%s: parsed with no name", name)
		}
	}
	if len(rejected) > 0 {
		t.Errorf("rejected %d/%d real published skills:\n  %s",
			len(rejected), len(skills), strings.Join(rejected, "\n  "))
	}
}

// TestCorpusDriftKeysWarnOnly pins the decision that `version` and friends warn
// rather than fail. The official validator errors on them, but 13 of 31 published
// skills carry `version`, so enforcing the closed allowlist verbatim would reject
// nearly half the ecosystem.
func TestCorpusDriftKeysWarnOnly(t *testing.T) {
	root := corpusDir(t)
	withDrift := 0
	for _, dir := range findSkills(t, root) {
		_, res, err := ValidateDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Has("nonstandard_frontmatter_key") {
			continue
		}
		withDrift++
		for _, e := range res.Errors() {
			if e.Code == "nonstandard_frontmatter_key" || e.Code == "unknown_frontmatter_key" {
				t.Errorf("%s: a non-standard key must warn, not block (got error %s: %s)",
					filepath.Base(dir), e.Code, e.Message)
			}
		}
	}
	t.Logf("%d skills carry non-standard frontmatter keys (warned, not blocked)", withDrift)
	if withDrift == 0 {
		t.Log("note: corpus has no drift keys; the warn path is untested here")
	}
}

// TestCorpusQualityDistribution is informational. It reports how the real
// ecosystem scores so the thresholds stay calibrated against reality rather than
// against what we wish creators would write.
func TestCorpusQualityDistribution(t *testing.T) {
	root := corpusDir(t)
	skills := findSkills(t, root)

	var descScores, compScores []int
	var noWhen, secondPerson int
	for _, dir := range skills {
		man, _, err := ValidateDir(dir)
		if err != nil || man.Description == "" {
			continue
		}
		files, _ := collect(dir)
		var fes []FileEntry
		for _, f := range files {
			fes = append(fes, FileEntry{Path: f})
		}
		ds := ScoreDescription(man.Description)
		cs := ScoreCompleteness(man, fes, ds)
		descScores = append(descScores, ds.Total)
		compScores = append(compScores, cs.Total)
		if !ds.HasWhen {
			noWhen++
		}
		if !ds.ThirdPerson {
			secondPerson++
		}
	}
	sort.Ints(descScores)
	sort.Ints(compScores)
	if len(descScores) == 0 {
		t.Skip("no scorable skills")
	}
	p := func(v []int, q float64) int { return v[int(float64(len(v)-1)*q)] }
	t.Logf("description score: min=%d p50=%d p90=%d max=%d",
		descScores[0], p(descScores, 0.5), p(descScores, 0.9), descScores[len(descScores)-1])
	t.Logf("completeness score: min=%d p50=%d p90=%d max=%d",
		compScores[0], p(compScores, 0.5), p(compScores, 0.9), compScores[len(compScores)-1])
	t.Logf("missing a 'when' clause: %d/%d · not third person: %d/%d",
		noWhen, len(descScores), secondPerson, len(descScores))
}
