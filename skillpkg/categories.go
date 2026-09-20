package skillpkg

import "strings"

// Categories are a controlled vocabulary, deliberately short.
//
// A marketplace with forty categories and two hundred skills feels empty in
// every one of them. Twelve is enough to navigate by, and keywords carry the
// long tail. Add one only when roughly fifteen existing skills would move into
// it — a category that exists before its contents do is just a dead link.
type Category struct {
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Blurb string `json:"blurb"`
	Icon  string `json:"icon"`
}

var Categories = []Category{
	{"documents", "Documents & Files", "PDF, docx, xlsx, OCR, parsing, conversion", "📄"},
	{"data", "Data & Analysis", "SQL, dataframes, statistics, ETL, visualisation", "📊"},
	{"coding", "Coding & DevTools", "Code generation, review, refactoring, testing", "⌨️"},
	{"devops", "DevOps & Cloud", "CI/CD, Kubernetes, Terraform, AWS, observability", "☁️"},
	{"web", "Web & Scraping", "Browsing, scraping, crawling, HTTP APIs", "🌐"},
	{"writing", "Writing & Content", "Drafting, editing, style guides, translation", "✍️"},
	{"design", "Design & Media", "Images, video, audio, diagrams, slides", "🎨"},
	{"research", "Research & Knowledge", "Search, summarisation, citations, RAG", "🔍"},
	{"business", "Business & Ops", "CRM, finance, legal, HR, project management", "💼"},
	{"communication", "Communication", "Email, chat, calendar, meetings", "💬"},
	{"security", "Security", "Scanning, secrets, auth, compliance, review", "🔐"},
	{"agents", "Agent Infrastructure", "Memory, planning, orchestration, evals, MCP", "🤖"},
}

var categoryBySlug = func() map[string]Category {
	m := make(map[string]Category, len(Categories))
	for _, c := range Categories {
		m[c.Slug] = c
	}
	return m
}()

func LookupCategory(slug string) (Category, bool) {
	c, ok := categoryBySlug[slug]
	return c, ok
}

func IsCategory(slug string) bool {
	_, ok := categoryBySlug[slug]
	return ok
}

// MaxCategories is how many a skill may carry.
//
// Three, because a skill filed under nine categories is discoverable under
// none of them: it dilutes every list it appears in and tells a browser
// nothing about what the skill is for. The validator warns past this and the
// catalogue refuses it, which is the same limit applied at the severity each
// place can afford -- a warning cannot be the only enforcement, and a hard
// refusal at publish time would block work over taxonomy.
const MaxCategories = 3

// NormaliseCategories trims, lowercases and de-duplicates, preserving the
// order given. It does not judge whether a slug is in the vocabulary; callers
// that care use IsCategory, because dropping an unrecognised category silently
// is how a typo becomes an uncategorised skill nobody can explain.
func NormaliseCategories(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, c := range in {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}
