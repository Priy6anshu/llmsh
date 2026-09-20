package mcp

import (
	"encoding/json"
	"io"
	"net/http"
)

// MaxBody is the largest request this endpoint will read. Every message here
// is a short JSON-RPC envelope; anything larger is a mistake or an attempt.
const MaxBody = 1 << 20

// ServeHTTP implements the Streamable HTTP transport.
//
// One endpoint, POST only. Every reply is a single JSON object rather than an
// event stream: each tool here is one synchronous read of the catalogue, so
// there is nothing to stream and a client that opened an SSE connection would
// be holding it open for no reason. The specification allows either, and a
// client must accept both.
//
// Revision 2026-07-28 removed the GET stream and protocol-level sessions, so
// there is no session state to keep -- which is what lets this run behind any
// number of processes without anything shared between them.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Origin is validated when present, which the specification requires: a
	// page in someone's browser must not be able to drive an MCP server
	// through DNS rebinding. An ordinary MCP client sends no Origin at all,
	// so absence is normal and allowed -- what is refused is a browser origin
	// we do not know.
	if origin := r.Header.Get("Origin"); origin != "" && !s.originAllowed(origin) {
		writeRPCError(w, http.StatusForbidden, nil, -32000, "origin not allowed")
		return
	}

	if r.Method != http.MethodPost {
		// The GET stream is gone in the current revision, and a server that
		// does not offer one answers 405 rather than leaving a client waiting.
		w.Header().Set("Allow", "POST")
		writeRPCError(w, http.StatusMethodNotAllowed, nil, -32000, "this endpoint accepts POST")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBody))
	if err != nil {
		writeRPCError(w, http.StatusBadRequest, nil, -32700, "could not read the request")
		return
	}

	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPCError(w, http.StatusBadRequest, nil, -32700, "parse error")
		return
	}

	// A notification carries no id and gets no body back, only 202. Answering
	// one is a protocol violation that some clients treat as fatal.
	if len(req.ID) == 0 || string(req.ID) == "null" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	result, rpcErr := s.Handle(r.Context(), req.Method, req.Params)
	out := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	if rpcErr != nil {
		out["error"] = rpcErr
	} else {
		out["result"] = result
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Mirrored so an intermediary can route on it without parsing the body.
	w.Header().Set("MCP-Protocol-Version", VersionModern)
	// An unknown method is a JSON-RPC error carried by a 200: the HTTP request
	// itself succeeded, and a client that branches on status would otherwise
	// treat a typo in a method name as the server being broken.
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) originAllowed(origin string) bool {
	for _, a := range s.AllowedOrigins {
		if a == origin {
			return true
		}
	}
	return false
}

func writeRPCError(w http.ResponseWriter, status int, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	out := map[string]any{"jsonrpc": "2.0", "error": map[string]any{"code": code, "message": msg}}
	if len(id) > 0 {
		out["id"] = id
	} else {
		out["id"] = nil
	}
	_ = json.NewEncoder(w).Encode(out)
}
