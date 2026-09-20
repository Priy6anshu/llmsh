package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testServer() *Server {
	return &Server{Name: "test", Version: "0", InstallCommand: "llmsh install"}
}

// The specification moved and the clients did not: 2026-07-28 replaced the
// initialize handshake with a per-request version and server/discover, while
// everything shipping today still opens with initialize. Answering only one
// era means not working with half the clients.
func TestBothProtocolErasAnswer(t *testing.T) {
	s := testServer()

	t.Run("server/discover", func(t *testing.T) {
		res, rpcErr := s.Handle(context.Background(), "server/discover", nil)
		if rpcErr != nil {
			t.Fatalf("error: %v", rpcErr)
		}
		m := res.(map[string]any)
		// A modern result carries resultType; a client that requires it
		// cannot parse a response without one.
		if m["resultType"] != "complete" {
			t.Errorf("resultType = %v", m["resultType"])
		}
		if vs := m["supportedVersions"].([]string); len(vs) == 0 || vs[0] != VersionModern {
			t.Errorf("supportedVersions = %v", vs)
		}
	})

	t.Run("initialize echoes the client's version", func(t *testing.T) {
		// A client that opened on an older revision is telling us what it can
		// parse; answering with ours would be a version it did not ask for.
		res, _ := s.Handle(context.Background(), "initialize",
			json.RawMessage(`{"protocolVersion":"2024-11-05"}`))
		if got := res.(map[string]any)["protocolVersion"]; got != "2024-11-05" {
			t.Errorf("protocolVersion = %v", got)
		}
	})

	t.Run("initialize with no version", func(t *testing.T) {
		res, _ := s.Handle(context.Background(), "initialize", json.RawMessage(`{}`))
		if got := res.(map[string]any)["protocolVersion"]; got != VersionLegacy {
			t.Errorf("protocolVersion = %v, want the legacy default", got)
		}
	})
}

// Every tool needs a name, a description a model can choose on, and a schema
// that survives being serialised.
func TestToolsAreUsable(t *testing.T) {
	seen := map[string]bool{}
	for _, tl := range Tools {
		name, _ := tl["name"].(string)
		if name == "" || seen[name] {
			t.Fatalf("missing or duplicate tool name: %v", tl)
		}
		seen[name] = true
		if d, _ := tl["description"].(string); len(d) < 40 {
			t.Errorf("%s: description too thin for a model to choose on", name)
		}
		sch, ok := tl["inputSchema"].(map[string]any)
		if !ok || sch["type"] != "object" {
			t.Errorf("%s: inputSchema must be an object schema", name)
		}
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

func TestUnknownMethodIsAnRPCError(t *testing.T) {
	_, rpcErr := testServer().Handle(context.Background(), "nope", nil)
	if rpcErr == nil || rpcErr["code"] != -32601 {
		t.Fatalf("got %v, want -32601", rpcErr)
	}
}

// The install tool hands back a command rather than running one. If that ever
// changes it should be a decision, not a refactor: installing writes a
// stranger's instructions into a directory an agent loads from.
func TestInstallToolOnlyDescribes(t *testing.T) {
	for _, tl := range Tools {
		if tl["name"] == "install_command" {
			if d, _ := tl["description"].(string); !strings.Contains(d, "does not install") {
				t.Errorf("description no longer says it does not install: %q", d)
			}
			return
		}
	}
	t.Fatal("install_command is gone")
}

// ------------------------------------------------------------------ HTTP

func post(t *testing.T, s *Server, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestHTTPAnswersJSONRPC(t *testing.T) {
	w := post(t, testServer(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	var out struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Result.Tools) != len(Tools) {
		t.Errorf("got %d tools", len(out.Result.Tools))
	}
}

// A notification has no id and must get 202 with no body. Answering one is a
// protocol violation some clients treat as fatal.
func TestHTTPNotificationGets202(t *testing.T) {
	w := post(t, testServer(), `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("a notification was answered with a body: %q", w.Body.String())
	}
}

// Origin is validated when present, which the specification requires: a page
// in someone's browser must not be able to drive the server through DNS
// rebinding. An ordinary MCP client sends none at all.
func TestHTTPOriginIsValidatedWhenPresent(t *testing.T) {
	s := testServer()
	s.AllowedOrigins = []string{"https://llmskillhub.com"}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

	if w := post(t, s, body, nil); w.Code != 200 {
		t.Errorf("no Origin should be fine, got %d", w.Code)
	}
	if w := post(t, s, body, map[string]string{"Origin": "https://llmskillhub.com"}); w.Code != 200 {
		t.Errorf("an allowed Origin got %d", w.Code)
	}
	if w := post(t, s, body, map[string]string{"Origin": "https://evil.example"}); w.Code != 403 {
		t.Errorf("an unknown Origin got %d, want 403", w.Code)
	}
}

// With no list configured, every Origin is refused -- a client sends none, so
// the ones that do are pages.
func TestHTTPRefusesEveryOriginByDefault(t *testing.T) {
	w := post(t, testServer(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		map[string]string{"Origin": "https://llmskillhub.com"})
	if w.Code != 403 {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestHTTPRefusesGET(t *testing.T) {
	r := httptest.NewRequest("GET", "/mcp", nil)
	w := httptest.NewRecorder()
	testServer().ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
	if w.Header().Get("Allow") != "POST" {
		t.Errorf("Allow = %q", w.Header().Get("Allow"))
	}
}

// Cacheable results carry ttlMs and cacheScope. They are specified on
// server/discover and tools/list, and a client that validates the result shape
// rejects the entire tool list without them -- which is exactly how this was
// found: the server connected, and every tool was missing.
func TestCacheableResultsCarryTheirCacheFields(t *testing.T) {
	s := testServer()
	for _, method := range []string{"server/discover", "tools/list"} {
		t.Run(method, func(t *testing.T) {
			res, rpcErr := s.Handle(context.Background(), method, nil)
			if rpcErr != nil {
				t.Fatalf("error: %v", rpcErr)
			}
			// Through JSON, because that is what a client validates -- an int
			// in a map is not proof it survives as a number on the wire.
			var m map[string]any
			b, _ := json.Marshal(res)
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			ttl, ok := m["ttlMs"].(float64)
			if !ok || ttl <= 0 {
				t.Errorf("ttlMs = %v, want a positive number", m["ttlMs"])
			}
			if sc := m["cacheScope"]; sc != "public" && sc != "private" {
				t.Errorf("cacheScope = %v, want public or private", sc)
			}
		})
	}
}

// A tool result is not cacheable and must not claim to be: the answer depends
// on the arguments, and a client sharing one across calls would be wrong.
func TestToolResultsAreNotCacheable(t *testing.T) {
	res := textResult("anything")
	if _, ok := res["ttlMs"]; ok {
		t.Error("a tool result carries ttlMs")
	}
	if _, ok := res["cacheScope"]; ok {
		t.Error("a tool result carries cacheScope")
	}
}
