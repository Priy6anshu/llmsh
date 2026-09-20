package skillpkg

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
