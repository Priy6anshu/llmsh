package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Tools is what a client sees. Descriptions are written for the model that
// reads them -- they say when to reach for the tool, not only what it does,
// because that is what the choice is actually made on.
var Tools = []map[string]any{
	{
		"name":        "search_skills",
		"title":       "Search skills",
		"description": "Search the LLM SkillHub catalogue for published agent skills — reusable instructions, scripts and references an agent can load. Use when the user asks for a capability you do not have, mentions finding or installing a skill, or describes a task (\"fill a PDF form\", \"clean this CSV\") that a published skill might already do.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":    map[string]any{"type": "string", "description": "What the skill should do, in the words someone would use."},
				"category": map[string]any{"type": "string", "description": "Restrict to one category slug; see list_categories."},
				"limit":    map[string]any{"type": "integer", "description": "How many to return. Default 10, maximum 50."},
			},
		},
	},
	{
		"name":        "get_skill",
		"title":       "Get a skill",
		"description": "Full detail for one skill: description, categories, licence, and every published version with its digest and size. Use after search_skills to decide whether a candidate fits.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"owner": map[string]any{"type": "string"},
				"slug":  map[string]any{"type": "string"},
			},
			"required": []string{"owner", "slug"},
		},
	},
	{
		"name":        "read_skill_file",
		"title":       "Read a file from a skill",
		"description": "Read one file out of a published version — SKILL.md for the instructions themselves, or any script or reference it ships. Use to check what a skill actually does before recommending it.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"owner":   map[string]any{"type": "string"},
				"slug":    map[string]any{"type": "string"},
				"path":    map[string]any{"type": "string", "description": "Path inside the package, e.g. SKILL.md."},
				"version": map[string]any{"type": "string", "description": "Defaults to the latest published version."},
			},
			"required": []string{"owner", "slug", "path"},
		},
	},
	{
		"name":        "list_categories",
		"title":       "List categories",
		"description": "The catalogue's category vocabulary, with how many skills are in each. Use to browse when a search term is not obvious.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	},
	{
		"name":  "install_command",
		"title": "How to install a skill",
		// Deliberately returns the command instead of running it.
		//
		// Installing writes a stranger's instructions into a directory an
		// agent loads from, which is the decision a person should be making.
		// The specification asks for a human in the loop on tool calls; a tool
		// that hands back a command someone can read and run IS that loop,
		// rather than a confirmation dialog bolted onto a side effect. It also
		// happens to be the only honest answer over HTTP, where the server has
		// no access to the caller's disk at all.
		"description": "The exact command to install a skill. Show it to the user and let them run it — this does not install anything itself.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"owner":   map[string]any{"type": "string"},
				"slug":    map[string]any{"type": "string"},
				"version": map[string]any{"type": "string", "description": "Pin an exact version. Recommended."},
			},
			"required": []string{"owner", "slug"},
		},
	},
}

// call runs one tool and renders its answer as text.
//
// Text rather than JSON, because the consumer is a language model: a readable
// list costs fewer tokens than the same data wrapped in braces, and the model
// does not have to be told a schema first. The fields a decision turns on --
// the digest, the licence, the command -- are spelled out rather than implied.
func (s *Server) call(ctx context.Context, name string, raw json.RawMessage) (string, error) {
	var a struct {
		Query    string `json:"query"`
		Category string `json:"category"`
		Limit    int    `json:"limit"`
		Owner    string `json:"owner"`
		Slug     string `json:"slug"`
		Path     string `json:"path"`
		Version  string `json:"version"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", fmt.Errorf("could not read the arguments: %v", err)
		}
	}

	switch name {
	case "search_skills":
		limit := a.Limit
		if limit <= 0 {
			limit = 10
		}
		if limit > 50 {
			limit = 50
		}
		skills, count, err := s.Catalogue.Search(ctx, a.Query, a.Category, limit)
		if err != nil {
			return "", err
		}
		if len(skills) == 0 {
			return fmt.Sprintf("No skills match %q. Try a broader term, or list_categories to browse.", a.Query), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d skill(s) match %q, showing %d:\n\n", count, a.Query, len(skills))
		for _, sk := range skills {
			fmt.Fprintf(&b, "%s/%s\n  %s\n", sk.Owner, sk.Slug, sk.Description)
			var bits []string
			if sk.Latest != nil {
				bits = append(bits, "v"+sk.Latest.Version,
					fmt.Sprintf("%d files", sk.Latest.FileCount), humanBytes(sk.Latest.Size))
			}
			if len(sk.Categories) > 0 {
				bits = append(bits, strings.Join(sk.Categories, ", "))
			}
			bits = append(bits, fmt.Sprintf("%d installs", sk.Downloads))
			fmt.Fprintf(&b, "  %s\n\n", joinNonEmpty(bits, " · "))
		}
		b.WriteString("Use get_skill for detail, read_skill_file to see what one actually does.")
		return b.String(), nil

	case "get_skill":
		if a.Owner == "" || a.Slug == "" {
			return "", fmt.Errorf("owner and slug are both required")
		}
		sk, err := s.Catalogue.Skill(ctx, a.Owner, a.Slug)
		if err != nil {
			return "", err
		}
		versions, _ := s.Catalogue.Versions(ctx, a.Owner, a.Slug)

		var b strings.Builder
		fmt.Fprintf(&b, "%s/%s\n\n%s\n\n", sk.Owner, sk.Slug, sk.Description)
		if len(sk.Categories) > 0 {
			fmt.Fprintf(&b, "Categories: %s\n", strings.Join(sk.Categories, ", "))
		}
		if len(sk.Keywords) > 0 {
			fmt.Fprintf(&b, "Keywords:   %s\n", strings.Join(sk.Keywords, ", "))
		}
		if sk.License != "" {
			fmt.Fprintf(&b, "Licence:    %s\n", sk.License)
		}
		if sk.Repository != "" {
			fmt.Fprintf(&b, "Source:     %s\n", sk.Repository)
		}
		fmt.Fprintf(&b, "Installs:   %d\n", sk.Downloads)
		if len(versions) > 0 {
			b.WriteString("\nVersions:\n")
			for _, v := range versions {
				fmt.Fprintf(&b, "  %-10s %s  %d files  %s\n",
					v.Version, v.ShortDigest, v.FileCount, humanBytes(v.Size))
			}
		}
		if u := s.Catalogue.WebURL(sk.Owner, sk.Slug); u != "" {
			fmt.Fprintf(&b, "\nPage: %s\n", u)
		}
		return b.String(), nil

	case "read_skill_file":
		if a.Owner == "" || a.Slug == "" || a.Path == "" {
			return "", fmt.Errorf("owner, slug and path are all required")
		}
		version := a.Version
		if version == "" {
			vs, err := s.Catalogue.Versions(ctx, a.Owner, a.Slug)
			if err != nil {
				return "", err
			}
			if len(vs) == 0 {
				return "", fmt.Errorf("%s/%s has no published versions", a.Owner, a.Slug)
			}
			version = vs[0].Version
		}
		f, err := s.Catalogue.File(ctx, a.Owner, a.Slug, version, a.Path)
		if err != nil {
			return "", err
		}
		if !f.IsText {
			return fmt.Sprintf("%s is not a text file (%s).", f.Path, humanBytes(f.Size)), nil
		}
		return fmt.Sprintf("%s/%s@%s — %s\n\n%s", a.Owner, a.Slug, version, f.Path, f.Content), nil

	case "list_categories":
		cats, err := s.Catalogue.Categories(ctx)
		if err != nil {
			return "", err
		}
		// Empty shelves are left out, the same way the website leaves them out.
		// An agent reading a category with nothing in it will either search it
		// and come back empty or tell someone the catalogue has none of what
		// they want; neither is true, and both cost a round trip to find out.
		var b strings.Builder
		var shown int
		for _, c := range cats {
			if c.Count == 0 {
				continue
			}
			fmt.Fprintf(&b, "  %-14s %-24s %d skill(s)\n    %s\n", c.Slug, c.Name, c.Count, c.Blurb)
			shown++
		}
		if shown == 0 {
			// Saying so beats printing a bare heading over nothing, which reads
			// as a failure rather than as an empty catalogue.
			return "No skills are filed under a category yet. Use search_skills instead.", nil
		}
		return "Categories:\n\n" + b.String(), nil

	case "install_command":
		if a.Owner == "" || a.Slug == "" {
			return "", fmt.Errorf("owner and slug are both required")
		}
		ref := a.Owner + "/" + a.Slug
		if a.Version != "" {
			ref += "@" + a.Version
		}
		return fmt.Sprintf(
			"To install %s, the user runs:\n\n  %s %s\n\n"+
				"This unpacks into .claude/skills/ in the project, or ~/.claude/skills/ "+
				"otherwise, and verifies the package digest before writing anything.\n\n"+
				"Show them this command rather than running it: installing puts a third "+
				"party's instructions somewhere an agent will load them, which is their "+
				"decision to make.",
			ref, s.InstallCommand, ref), nil
	}
	return "", fmt.Errorf("no such tool: %s", name)
}
