package mcp

import "testing"

func TestSplitName(t *testing.T) {
	for _, c := range []struct{ in, owner, slug string }{
		{"anthropics/mcp-builder", "anthropics", "mcp-builder"},
		{"mcp-builder", "", "mcp-builder"},
		{"  anthropics / mcp-builder ", "anthropics", "mcp-builder"},
		{"@anthropics/mcp-builder", "anthropics", "mcp-builder"},
		{"", "", ""},
	} {
		owner, slug := splitName(c.in)
		if owner != c.owner || slug != c.slug {
			t.Errorf("splitName(%q) = %q, %q; want %q, %q", c.in, owner, slug, c.owner, c.slug)
		}
	}
}

// The descriptions are the only thing a model reads before choosing, so the
// two searching-shaped tools have to say which is which. This is a contract
// with the model, and the kind that rots quietly: nothing fails when a
// description stops distinguishing them, the model just picks worse.
func TestTheTwoLookupToolsSayWhenToUseEachOther(t *testing.T) {
	desc := map[string]string{}
	schema := map[string]map[string]any{}
	for _, tl := range Tools {
		desc[tl["name"].(string)] = tl["description"].(string)
		if in, ok := tl["inputSchema"].(map[string]any); ok {
			if props, ok := in["properties"].(map[string]any); ok {
				schema[tl["name"].(string)] = props
			}
		}
	}

	if !contains(desc["search_skills"], "get_skill") {
		t.Error("search_skills does not point at get_skill for a known name")
	}
	if !contains(desc["get_skill"], "search") {
		t.Error("get_skill does not say how it differs from searching")
	}
	// Both ways of naming a skill stay offered. Dropping owner/slug would
	// break every caller written against the older shape.
	for _, tool := range []string{"get_skill", "read_skill_file"} {
		for _, field := range []string{"name", "owner", "slug"} {
			if _, ok := schema[tool][field]; !ok {
				t.Errorf("%s no longer accepts %q", tool, field)
			}
		}
	}
	// Nothing may require owner and slug separately any more, or a caller
	// holding only a name is forced to search for the half it lacks.
	for _, tl := range Tools {
		in, _ := tl["inputSchema"].(map[string]any)
		req, _ := in["required"].([]string)
		for _, r := range req {
			if r == "owner" || r == "slug" {
				t.Errorf("%s still requires %q", tl["name"], r)
			}
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
