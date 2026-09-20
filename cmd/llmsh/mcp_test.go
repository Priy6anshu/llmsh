package main

import (
	"encoding/json"
	"testing"
)

// The protocol moved and the clients did not: 2026-07-28 replaced the
// initialize handshake with a per-request version and server/discover, while
// everything shipping today still opens with initialize. Answering only one
// era means not working with half the clients, so both are pinned here.
func TestDiscoverAndInitializeBothAnswer(t *testing.T) {
	t.Run("server/discover", func(t *testing.T) {
		res, rpcErr := mcpDispatch(nil, nil, "server/discover", nil)
		if rpcErr != nil {
			t.Fatalf("error: %v", rpcErr)
		}
		m := res.(map[string]any)
		// Every modern result carries resultType; a client that requires it
		// cannot parse a response without one.
		if m["resultType"] != "complete" {
			t.Errorf("resultType = %v", m["resultType"])
		}
		vs, _ := m["supportedVersions"].([]string)
		if len(vs) == 0 || vs[0] != mcpModern {
			t.Errorf("supportedVersions = %v, want the modern one first", vs)
		}
		if _, ok := m["capabilities"].(map[string]any)["tools"]; !ok {
			t.Error("tools capability not declared")
		}
	})

	t.Run("initialize echoes the client's version", func(t *testing.T) {
		// A client that opened with an older revision is telling us what it
		// can parse; answering with ours would be a version it did not ask for.
		params := json.RawMessage(`{"protocolVersion":"2024-11-05"}`)
		res, rpcErr := mcpDispatch(nil, nil, "initialize", params)
		if rpcErr != nil {
			t.Fatalf("error: %v", rpcErr)
		}
		if got := res.(map[string]any)["protocolVersion"]; got != "2024-11-05" {
			t.Errorf("protocolVersion = %v, want the one the client asked for", got)
		}
	})

	t.Run("initialize with no version", func(t *testing.T) {
		res, _ := mcpDispatch(nil, nil, "initialize", json.RawMessage(`{}`))
		if got := res.(map[string]any)["protocolVersion"]; got != mcpLegacy {
			t.Errorf("protocolVersion = %v, want the legacy default", got)
		}
	})
}

// Every tool needs a name, a description and a schema. A tool the model cannot
// tell apart from the others is one it will not reach for.
func TestToolsAreDescribed(t *testing.T) {
	res, _ := mcpDispatch(nil, nil, "tools/list", nil)
	tools := res.(map[string]any)["tools"].([]map[string]any)
	if len(tools) == 0 {
		t.Fatal("no tools")
	}
	seen := map[string]bool{}
	for _, tl := range tools {
		name, _ := tl["name"].(string)
		if name == "" {
			t.Fatalf("a tool has no name: %v", tl)
		}
		if seen[name] {
			t.Errorf("duplicate tool %q", name)
		}
		seen[name] = true

		if d, _ := tl["description"].(string); len(d) < 40 {
			t.Errorf("%s: description is too thin for a model to choose on", name)
		}
		sch, ok := tl["inputSchema"].(map[string]any)
		if !ok || sch["type"] != "object" {
			t.Errorf("%s: inputSchema must be an object schema", name)
		}
		if _, ok := sch["properties"]; !ok {
			t.Errorf("%s: inputSchema has no properties", name)
		}
		// The schema has to survive the trip; a client parses it as JSON.
		if _, err := json.Marshal(tl); err != nil {
			t.Errorf("%s: not serialisable: %v", name, err)
		}
	}
	for _, want := range []string{"search_skills", "get_skill", "read_skill_file", "list_categories", "install_command"} {
		if !seen[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

// An unknown method is an RPC error; a failing tool is not. The difference
// matters: one is the client's problem and the other is something the model
// should see and work around.
func TestUnknownMethodIsAnRPCError(t *testing.T) {
	res, rpcErr := mcpDispatch(nil, nil, "nope", nil)
	if rpcErr == nil {
		t.Fatalf("no error for an unknown method; got %v", res)
	}
	if rpcErr["code"] != -32601 {
		t.Errorf("code = %v, want -32601", rpcErr["code"])
	}
}

// The install tool hands back a command rather than running one. If that ever
// changes, it should be because somebody decided to, not because a refactor
// made it convenient -- installing writes a stranger's instructions into a
// directory an agent loads from.
func TestInstallToolOnlyDescribes(t *testing.T) {
	var found map[string]any
	res, _ := mcpDispatch(nil, nil, "tools/list", nil)
	for _, tl := range res.(map[string]any)["tools"].([]map[string]any) {
		if tl["name"] == "install_command" {
			found = tl
		}
	}
	if found == nil {
		t.Fatal("install_command is gone")
	}
	desc, _ := found["description"].(string)
	if !contains(desc, "does not install") {
		t.Errorf("the description no longer says it does not install: %q", desc)
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
