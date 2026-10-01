package skillpkg

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Quality scores a package on the two things that decide whether a skill is
// usable and findable. Nothing here ever blocks a publish: if a package passes
// the official validator we do not reject it for style, because the moment a
// marketplace is stricter than the runtime, creators route around it.
//
// These are heuristics over prose, so they are deliberately generous — every
// check awards points for a signal being present and never penalises its
// absence beyond withholding them.

func categoryList() string {
	out := make([]string, 0, len(Categories))
	for _, c := range Categories {
		out = append(out, c.Slug)
	}
	return strings.Join(out, ", ")
}

var (
	// The single highest-value signal. Official guidance is explicit that Claude
	// tends to UNDER-trigger skills, so a description must say when to use it,
	// not just what it does.
	whenRe = regexp.MustCompile(`(?i)\b(use (this |it )?(skill )?(when|whenever|for)|should be used when|triggers? (on|when)|invoke when|when the user|for when|whenever the user)\b`)

	// Second person. The standard wants third person: "This skill should be used
	// when the user asks to..." rather than "Use this when you want to...".
	secondPersonRe = regexp.MustCompile(`(?i)\b(you|your|you're|yours|yourself)\b`)

	// Concrete trigger phrases, the pattern the best published skills follow:
	// listing the literal words a user would type, in quotes.
	quotedRe = regexp.MustCompile(`["'` + "`" + `]([^"'` + "`" + `]{3,60})["'` + "`" + `]`)

	// The "pushy" pattern: covering cases where the user does not name the tool.
	pushyRe = regexp.MustCompile(`(?i)(even if|even when|without (explicitly|naming|saying)|don't (explicitly|say)|doesn't (explicitly|say)|regardless of)`)

	marketingRe = regexp.MustCompile(`(?i)\b(best|powerful|revolutionary|amazing|awesome|ultimate|blazing|seamless|cutting.edge)\b`)
)

type DescriptionScore struct {
	Total       int      `json:"total"` // 0-100
	HasWhat     bool     `json:"has_what"`
	HasWhen     bool     `json:"has_when"`
	Phrases     int      `json:"trigger_phrases"`
	ThirdPerson bool     `json:"third_person"`
	Pushy       bool     `json:"pushy"`
	WithinSoft  bool     `json:"within_soft_budget"`
	Notes       []string `json:"notes,omitempty"`
}

// ScoreDescription rates the one field that is resident in context for every
// installed skill in every conversation, and that also drives keyword ranking
// and the embedding used for semantic search.
func ScoreDescription(d string) DescriptionScore {
	s := DescriptionScore{}
	d = strings.TrimSpace(d)
	if d == "" {
		return s
	}

	loc := whenRe.FindStringIndex(d)
	s.HasWhen = loc != nil

	// "What" is whatever meaningful prose precedes the when-clause; if there is
	// no when-clause, the whole description is treated as the what.
	whatLen := len(d)
	if loc != nil {
		whatLen = loc[0]
	}
	s.HasWhat = whatLen >= 25

	s.Phrases = len(quotedRe.FindAllString(d, -1))
	s.ThirdPerson = !secondPersonRe.MatchString(d)
	s.Pushy = pushyRe.MatchString(d)
	s.WithinSoft = len(d) <= SoftDescriptionLen

	if s.HasWhat {
		s.Total += 20
	} else {
		s.Notes = append(s.Notes, "say what the skill does before saying when to use it")
	}
	if s.HasWhen {
		s.Total += 30
	} else {
		s.Notes = append(s.Notes,
			`add an explicit "use when ..." clause — this is the largest single factor in whether an agent triggers the skill`)
	}
	switch {
	case s.Phrases >= 2:
		s.Total += 20
	case s.Phrases == 1:
		s.Total += 10
		s.Notes = append(s.Notes, "name a second concrete phrase a user would actually type")
	default:
		s.Notes = append(s.Notes, `name 2+ concrete phrases a user would type, e.g. "fill a PDF form"`)
	}
	if s.ThirdPerson {
		s.Total += 10
	} else {
		s.Notes = append(s.Notes, `write in third person ("This skill should be used when the user...") rather than addressing the reader`)
	}
	if s.Pushy {
		s.Total += 10
	} else {
		s.Notes = append(s.Notes, "cover the case where the user needs this but doesn't name it")
	}
	if s.WithinSoft {
		s.Total += 10
	} else {
		s.Notes = append(s.Notes, fmt.Sprintf(
			"at %d characters this is under the %d cap but over the %d soft budget; every description is resident in context for every installed skill",
			len(d), MaxDescriptionLen, SoftDescriptionLen))
	}
	return s
}

type CompletenessScore struct {
	Total int      `json:"total"` // 0-100
	Notes []string `json:"notes,omitempty"`
}

// ScoreCompleteness weights what actually makes a skill findable and trustworthy.
// It deliberately does NOT demand examples/ or a CHANGELOG: empirically only 6 of
// 31 published skills have examples/ and none have a changelog, so scoring those
// heavily would rate most of the ecosystem's best skills as incomplete.
func ScoreCompleteness(m *Manifest, files []FileEntry, desc DescriptionScore) CompletenessScore {
	c := CompletenessScore{}
	add := func(pts int, ok bool, note string) {
		if ok {
			c.Total += pts
		} else {
			c.Notes = append(c.Notes, note)
		}
	}

	hasDir := func(prefix string) bool {
		for _, f := range files {
			if strings.HasPrefix(f.Path, prefix) {
				return true
			}
		}
		return false
	}

	add(30, desc.Total >= 70, "improve the description score to 70 or above")
	add(10, len(m.Hub.Keywords) >= 3, "add at least 3 keywords under metadata.llmskillhub.keywords")
	add(5, len(m.Hub.Categories) >= 1, "set a category under metadata.llmskillhub.categories")
	add(15, m.Compatibility != "" || m.Hub.Capabilities.Filesystem != "" ||
		m.Hub.Capabilities.Network || m.Hub.Capabilities.Shell,
		"declare what the skill needs and touches, via compatibility or metadata.llmskillhub.capabilities")
	add(10, m.License != "", "declare a license")
	add(10, m.BodyWords >= 200 && m.BodyWords <= 5000,
		fmt.Sprintf("the instructions are %d words; aim for 200-5000", m.BodyWords))

	// Either split detail into references/, or be genuinely short. A 4,000-word
	// monolithic SKILL.md is the actual anti-pattern, and this is the only check
	// that catches it — the body is loaded in full every time the skill fires.
	add(10, hasDir("references/") || m.BodyWords < 2000,
		"move detail into references/ so SKILL.md stays lean, or shorten the body")

	add(5, m.Hub.Repository != "", "link a repository")
	add(5, hasDir("examples/") || hasDir("scripts/"), "add examples/ or scripts/")
	return c
}

// Quality appends advisory findings. Called by both the CLI and the server so the
// creator sees the same report locally that the publish endpoint will produce.
// referenceRe finds a path the instructions tell the reader to use.
//
// Three shapes, because those are the three ways a skill actually names one:
// a command that runs it (python scripts/x.py, bash ./setup.sh), a Markdown
// link or inline-code path, and a bare mention of a file under one of the
// directories the spec gives meaning to.
//
// Deliberately anchored on those directory names rather than on "anything with
// a slash". Prose is full of things that look like paths and are not -- an API
// route, a JSON pointer, and/or -- and a check that cries wolf on those is one
// a reviewer learns to scroll past, which costs more than it catches.
var referenceRe = regexp.MustCompile(
	`(?:^|[\s"'` + "`" + `(\[])\.?/?((?:scripts|references|assets|examples|templates|data)/[A-Za-z0-9._\-/]+\.[A-Za-z0-9]+)`)

// missingReferences reports files the instructions point at that were not
// published.
//
// This is the defect that matters most in a catalogue of instructions an agent
// will follow: the model reads "run python scripts/rice_prioritizer.py",
// there is no such file, and the failure surfaces to somebody who will blame
// their agent or this catalogue rather than the package. It is also invisible
// to every other check here -- the manifest can be perfect, the description
// can score 100, and the skill still cannot do what it says.
//
// A warning rather than an error. Some skills reference a path the user is
// expected to create, and refusing those outright would be stricter than the
// runtime for no gain; what this is for is putting the discrepancy in front of
// the reviewer, who can see in one line what the package claims and what it
// contains.
func missingReferences(m *Manifest, files []FileEntry) []string {
	if m == nil || m.Body == "" {
		return nil
	}
	have := make(map[string]bool, len(files))
	for _, f := range files {
		have[f.Path] = true
	}
	seen := map[string]bool{}
	var missing []string
	for _, match := range referenceRe.FindAllStringSubmatch(m.Body, -1) {
		p := strings.TrimPrefix(match[1], "./")
		if have[p] || seen[p] {
			continue
		}
		seen[p] = true
		missing = append(missing, p)
	}
	sort.Strings(missing)
	return missing
}

func Quality(m *Manifest, files []FileEntry, res *Result) {
	if m == nil || m.Description == "" {
		return
	}
	ds := ScoreDescription(m.Description)
	cs := ScoreCompleteness(m, files, ds)

	if ds.Total < 70 {
		res.Add(SeverityWarn, "weak_description",
			fmt.Sprintf("description score %d/100", ds.Total),
			At(SkillFile, 0), Hint(strings.Join(ds.Notes, "; ")))
	}
	if len(m.Description) < MinDescriptionLen {
		res.Add(SeverityError, "description_too_short",
			fmt.Sprintf("description is %d characters; at least %d are needed for an agent to route on it",
				len(m.Description), MinDescriptionLen), At(SkillFile, 0))
	}
	if marketingRe.MatchString(m.Description) {
		res.Add(SeverityWarn, "marketing_language",
			"description contains marketing language; state capability and triggers instead",
			At(SkillFile, 0))
	}
	if missing := missingReferences(m, files); len(missing) > 0 {
		shown := missing
		if len(shown) > 5 {
			shown = shown[:5]
		}
		res.Add(SeverityWarn, "missing_reference",
			fmt.Sprintf("the instructions use %s, which %s not in this package",
				strings.Join(shown, ", "), plural(len(shown), "is", "are")),
			At(SkillFile, 0),
			Hint("Ship the file, or drop the instruction that needs it — an agent that follows this will fail on that line."))
	}
	// An unrecognised category warns rather than blocks. Blocking on taxonomy is
	// how a marketplace ends up stricter than the runtime, and creators route
	// around strictness rather than complying with it.
	if len(m.Hub.Categories) > MaxCategories {
		res.Add(SeverityWarn, "too_many_categories",
			fmt.Sprintf("%d categories; a skill filed under everything is found under nothing",
				len(m.Hub.Categories)),
			At(SkillFile, 0),
			Hint(fmt.Sprintf("Keep the %d that describe it best.", MaxCategories)))
	}
	for _, c := range m.Hub.Categories {
		if !IsCategory(c) {
			// Not an error, and no longer a dead end either. The catalogue
			// creates a category it has not seen, matching it first against the
			// existing ones by spelling -- so "Dev Ops" joins devops rather
			// than founding a rival. What this warning is for is the case where
			// somebody meant one of the twelve and typed something near it, and
			// would rather know now than discover their skill filed on its own.
			res.Add(SeverityWarn, "unknown_category",
				fmt.Sprintf("%q is not one of the catalogue's twelve categories; publishing will create it", c),
				At(SkillFile, 0), Hint("The built-in ones are: "+categoryList()))
		}
	}
	if cs.Total < 60 {
		res.Add(SeverityWarn, "incomplete_package",
			fmt.Sprintf("completeness score %d/100", cs.Total),
			Hint(strings.Join(cs.Notes, "; ")))
	}
	if m.BodyWords > 5000 {
		res.Add(SeverityWarn, "body_too_long",
			fmt.Sprintf("instructions are %d words; the body is loaded in full every time the skill triggers", m.BodyWords),
			At(SkillFile, 0), Hint("Move detail into references/ and point at it from SKILL.md."))
	}
}

// plural picks the verb, so a one-file finding does not read as a typo.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
