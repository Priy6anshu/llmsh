package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Priy6anshu/llmsh/internal/client"
	"github.com/Priy6anshu/llmsh/internal/config"
)

// An MCP server over stdio, so an agent can search the catalogue mid-task.
//
// Hand-written rather than pulled from an SDK. The whole surface is four
// methods of JSON-RPC over a pipe, and this binary is one people install and
// run against their own machine -- a dependency tree behind that is a cost
// paid by every user for code that would not be much shorter.
//
// Two protocol eras, because the specification moved and the clients did not.
// 2026-07-28 dropped the initialize handshake and carries the version in each
// request's _meta, with server/discover to ask up front; everything shipping
// today still opens with initialize. Answering both is a dozen lines and the
// difference between working and not.
const (
	mcpModern = "2026-07-28"
	mcpLegacy = "2025-06-18"
)

// The tools. Descriptions are written for the model that reads them: they say
// when to reach for the tool, not only what it does, because that is what the
// choice is actually made on.
var mcpTools = []map[string]any{
	{
		"name":        "search_skills",
		"title":       "Search skills",
		"description": "Search the LLM SkillHub catalogue for published agent skills — reusable instructions, scripts and references an agent can load. Use when the user asks for a capability you do not have, mentions installing or finding a skill, or describes a task (\"fill a PDF form\", \"clean this CSV\") that a published skill might already do.",
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
		"description": "Full detail for one skill: description, categories, licence, every published version with its digest and size. Use after search_skills to decide whether a candidate fits.",
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
		"description": "The catalogue's category vocabulary, with how many skills are in each.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	},
	{
		"name":  "install_command",
		"title": "How to install a skill",
		// Deliberately returns the command instead of running it.
		//
		// Installing writes a stranger's instructions into a directory an agent
		// loads from, which is exactly the decision a person should be making.
		// The specification asks for a human in the loop on tool calls; a tool
		// that hands back a command someone can read and run IS that loop,
		// rather than a confirmation dialog bolted onto a side effect.
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

func cmdMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	_ = fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// No token needed: everything here reads the public catalogue. One is sent
	// if the user happens to have signed in, which is what lets them see their
	// own unlisted versions and nothing more.
	c := client.New(cfg.API, cfg.Ingest, cfg.Token)

	// stdout is the transport. Anything else written there is a protocol error
	// on the wire, so every diagnostic in this command goes to stderr.
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64<<10), 8<<20)
	out := json.NewEncoder(os.Stdout)

	fmt.Fprintf(os.Stderr, "llmsh mcp — %s\n", cfg.API)

	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue // not our message to answer; a reply needs an id
		}
		// A notification has no id and takes no response. Replying to one is
		// a protocol violation that some clients treat as fatal.
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue
		}

		result, rpcErr := mcpDispatch(c, cfg, req.Method, req.Params)
		msg := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if rpcErr != nil {
			msg["error"] = rpcErr
		} else {
			msg["result"] = result
		}
		if err := out.Encode(msg); err != nil {
			return err
		}
	}
	if err := in.Err(); err != nil && err != io.EOF {
		return err
	}
	return nil
}

func mcpDispatch(c *client.Client, cfg *config.Config, method string, params json.RawMessage) (any, map[string]any) {
	switch method {
	case "server/discover":
		return map[string]any{
			"resultType":        "complete",
			"supportedVersions": []string{mcpModern, mcpLegacy},
			"capabilities":      map[string]any{"tools": map[string]any{}},
			"instructions": "Search LLM SkillHub for published agent skills. " +
				"Every version listed here was reviewed by a person before it was listed.",
			"_meta": map[string]any{
				"io.modelcontextprotocol/serverInfo": map[string]any{
					"name": "llmskillhub", "version": buildVersion(),
				},
			},
		}, nil

	case "initialize":
		// The legacy handshake. Echo the version the client asked for when we
		// know it, rather than announcing ours: a client that opened with an
		// older revision is telling us what it can parse.
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(params, &p)
		version := p.ProtocolVersion
		if version == "" {
			version = mcpLegacy
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "llmskillhub", "version": buildVersion()},
			"instructions": "Search LLM SkillHub for published agent skills. " +
				"Every version listed here was reviewed by a person before it was listed.",
		}, nil

	case "tools/list":
		return map[string]any{"resultType": "complete", "tools": mcpTools}, nil

	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, map[string]any{"code": -32602, "message": "invalid params"}
		}
		text, err := mcpCall(c, cfg, p.Name, p.Arguments)
		if err != nil {
			// A failed tool is reported through the result, not as an RPC
			// error: the model should see what went wrong and be able to try
			// something else, rather than the client treating it as a
			// transport failure.
			return map[string]any{
				"resultType": "complete",
				"content":    []any{map[string]any{"type": "text", "text": err.Error()}},
				"isError":    true,
			}, nil
		}
		return map[string]any{
			"resultType": "complete",
			"content":    []any{map[string]any{"type": "text", "text": text}},
			"isError":    false,
		}, nil

	case "ping":
		return map[string]any{"resultType": "complete"}, nil
	}
	return nil, map[string]any{"code": -32601, "message": "method not found: " + method}
}

// mcpCall runs one tool and renders its answer as text.
//
// Text rather than JSON, because the consumer is a language model: a readable
// list costs fewer tokens than the same data wrapped in braces, and the model
// does not have to be told the schema first. The fields that matter for a
// decision -- the digest, the licence, the install command -- are spelled out
// rather than implied.
func mcpCall(c *client.Client, cfg *config.Config, name string, raw json.RawMessage) (string, error) {
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
		// The catalogue's own endpoint, with the catalogue's own ranking.
		skills, count, err := c.Search(a.Query, a.Category, "", limit)
		if err != nil {
			return "", err
		}
		if len(skills) == 0 {
			return fmt.Sprintf("No skills match %q. Try a broader term, or list_categories to browse.", a.Query), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d skill(s) match %q, showing %d:\n\n", count, a.Query, len(skills))
		for _, s := range skills {
			fmt.Fprintf(&b, "%s/%s\n", s.Owner, s.Slug)
			fmt.Fprintf(&b, "  %s\n", s.Description)
			if s.Latest != nil {
				fmt.Fprintf(&b, "  v%s · %d files · %s", s.Latest.Version, s.Latest.FileCount, humanBytes(s.Latest.Size))
			}
			if len(s.Categories) > 0 {
				fmt.Fprintf(&b, " · %s", strings.Join(s.Categories, ", "))
			}
			fmt.Fprintf(&b, " · %d installs\n\n", s.Downloads)
		}
		b.WriteString("Use get_skill for detail, read_skill_file to see what one actually does.")
		return b.String(), nil

	case "get_skill":
		if a.Owner == "" || a.Slug == "" {
			return "", fmt.Errorf("owner and slug are both required")
		}
		s, err := c.Skill(a.Owner, a.Slug)
		if err != nil {
			return "", err
		}
		versions, _ := c.Versions(a.Owner, a.Slug)

		var b strings.Builder
		fmt.Fprintf(&b, "%s/%s\n\n%s\n\n", s.Owner, s.Slug, s.Description)
		if len(s.Categories) > 0 {
			fmt.Fprintf(&b, "Categories: %s\n", strings.Join(s.Categories, ", "))
		}
		if len(s.Keywords) > 0 {
			fmt.Fprintf(&b, "Keywords:   %s\n", strings.Join(s.Keywords, ", "))
		}
		if s.License != "" {
			fmt.Fprintf(&b, "Licence:    %s\n", s.License)
		}
		if s.Repository != "" {
			fmt.Fprintf(&b, "Source:     %s\n", s.Repository)
		}
		fmt.Fprintf(&b, "Installs:   %d\n", s.Downloads)
		if len(versions) > 0 {
			b.WriteString("\nVersions:\n")
			for _, v := range versions {
				fmt.Fprintf(&b, "  %-10s %s  %d files  %s\n",
					v.Version, v.ShortDigest, v.FileCount, humanBytes(v.Size))
			}
		}
		fmt.Fprintf(&b, "\nPage: %s/%s/%s\n", webOrigin(cfg), s.Owner, s.Slug)
		return b.String(), nil

	case "read_skill_file":
		if a.Owner == "" || a.Slug == "" || a.Path == "" {
			return "", fmt.Errorf("owner, slug and path are all required")
		}
		version := a.Version
		if version == "" {
			vs, err := c.Versions(a.Owner, a.Slug)
			if err != nil {
				return "", err
			}
			if len(vs) == 0 {
				return "", fmt.Errorf("%s/%s has no published versions", a.Owner, a.Slug)
			}
			version = vs[0].Version
		}
		f, err := c.File(a.Owner, a.Slug, version, a.Path)
		if err != nil {
			return "", err
		}
		if !f.IsText {
			return fmt.Sprintf("%s is not a text file (%s).", f.Path, humanBytes(f.Size)), nil
		}
		return fmt.Sprintf("%s/%s@%s — %s\n\n%s", a.Owner, a.Slug, version, f.Path, f.Content), nil

	case "list_categories":
		cats, err := c.Categories()
		if err != nil {
			return "", err
		}
		var b strings.Builder
		b.WriteString("Categories:\n\n")
		for _, cat := range cats {
			fmt.Fprintf(&b, "  %-14s %-24s %d skill(s)\n    %s\n", cat.Slug, cat.Name, cat.Count, cat.Blurb)
		}
		return b.String(), nil

	case "install_command":
		if a.Owner == "" || a.Slug == "" {
			return "", fmt.Errorf("owner and slug are both required")
		}
		ref := a.Owner + "/" + a.Slug
		if a.Version != "" {
			ref += "@" + a.Version
		}
		return fmt.Sprintf(
			"To install %s, the user runs:\n\n  llmsh install %s\n\n"+
				"This unpacks into .claude/skills/ in the project, or ~/.claude/skills/ otherwise, "+
				"and verifies the package digest before writing anything.\n\n"+
				"Show them this command rather than running it: installing puts a third party's "+
				"instructions somewhere an agent will load them, which is their decision to make.",
			ref, ref), nil
	}
	return "", fmt.Errorf("no such tool: %s", name)
}
