package client

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const pat = "skh_live_testtoken"

// fakeAPI exchanges pat for access tokens and serves /v1/me to whichever
// bearer it currently considers valid.
type fakeAPI struct {
	srv        *httptest.Server
	exchanges  atomic.Int32
	accepted   atomic.Value // string: the access token /v1/me accepts
	sawBearers []string
	noExchange bool // behave like an API from before /v1/auth/token
	attempts   atomic.Int32
	lastBody   atomic.Value
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{}
	f.accepted.Store("")
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		switch r.URL.Path {
		case "/v1/auth/token":
			f.attempts.Add(1)
			if f.noExchange {
				http.NotFound(w, r)
				return
			}
			if bearer != pat {
				w.WriteHeader(401)
				return
			}
			n := f.exchanges.Add(1)
			tok := "eyJ.access." + string(rune('0'+n))
			f.accepted.Store(tok)
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": tok, "expires_at": time.Now().Add(15 * time.Minute),
			})
		case "/v1/me", "/v1/echo":
			f.sawBearers = append(f.sawBearers, bearer)
			ok := bearer == f.accepted.Load().(string) || (f.noExchange && bearer == pat)
			if !ok {
				w.WriteHeader(401)
				return
			}
			b, _ := io.ReadAll(r.Body)
			f.lastBody.Store(string(b))
			json.NewEncoder(w).Encode(map[string]any{"handle": "alice"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) client(cache string) *Client {
	c := New(f.srv.URL, f.srv.URL, pat)
	c.CachePath = cache
	return c
}

func get(t *testing.T, c *Client) {
	t.Helper()
	var out map[string]any
	if err := c.getJSON("/v1/me", &out); err != nil {
		t.Fatal(err)
	}
}

func TestAPersonalTokenIsExchangedOnceAndNeverSentAgain(t *testing.T) {
	f := newFakeAPI(t)
	c := f.client("")
	for i := 0; i < 5; i++ {
		get(t, c)
	}
	if n := f.exchanges.Load(); n != 1 {
		t.Fatalf("%d exchanges for five requests, want 1", n)
	}
	for _, b := range f.sawBearers {
		if b == pat {
			t.Fatal("the personal token was sent to an ordinary endpoint")
		}
	}
}

func TestTheNextCommandReusesTheCachedAccessToken(t *testing.T) {
	f := newFakeAPI(t)
	cache := filepath.Join(t.TempDir(), "access.json")
	get(t, f.client(cache))
	get(t, f.client(cache)) // a second process, in effect
	if n := f.exchanges.Load(); n != 1 {
		t.Fatalf("%d exchanges across two commands, want 1", n)
	}
	st, err := os.Stat(cache)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("cache is %v, want 0600", st.Mode().Perm())
	}
	raw, _ := os.ReadFile(cache)
	if strings.Contains(string(raw), pat) {
		t.Fatal("the personal token was written into the cache")
	}
}

func TestACacheForAnotherTokenOrServerIsIgnored(t *testing.T) {
	f := newFakeAPI(t)
	cache := filepath.Join(t.TempDir(), "access.json")
	get(t, f.client(cache))

	other := New(f.srv.URL, f.srv.URL, "skh_live_someoneelse")
	other.CachePath = cache
	if tok, _, ok := other.loadCache(); ok {
		t.Fatalf("another token's cache was used: %s", tok)
	}
	elsewhere := New("https://elsewhere.test", f.srv.URL, pat)
	elsewhere.CachePath = cache
	if _, _, ok := elsewhere.loadCache(); ok {
		t.Fatal("a cache for a different API was used")
	}
}

// The server restarted with a new key, or the token expired early: the request
// is retried once with a fresh exchange -- body and all -- instead of telling
// someone who is signed in that they are not.
func TestARefusedAccessTokenIsReplacedAndTheRequestRetried(t *testing.T) {
	f := newFakeAPI(t)
	c := f.client("")
	get(t, c)
	f.accepted.Store("eyJ.rotated.away") // the token in hand stops working

	req, _ := http.NewRequest("POST", f.srv.URL+"/v1/echo", strings.NewReader(`{"n":1}`))
	if err := c.do(req, nil); err != nil {
		t.Fatalf("retry did not recover: %v", err)
	}
	if n := f.exchanges.Load(); n != 2 {
		t.Fatalf("%d exchanges, want 2 (the original and one after the refusal)", n)
	}
	if b, _ := f.lastBody.Load().(string); b != `{"n":1}` {
		t.Fatalf("retried request lost its body: %q", b)
	}
}

func TestAnOlderAPIGetsThePersonalTokenItself(t *testing.T) {
	f := newFakeAPI(t)
	f.noExchange = true
	c := f.client("")
	get(t, c)
	get(t, c)
	if len(f.sawBearers) != 2 || f.sawBearers[1] != pat {
		t.Fatalf("an API without the exchange should get the personal token: %v", f.sawBearers)
	}
	// And it is asked once, not before every request.
	if n := f.attempts.Load(); n != 1 {
		t.Fatalf("%d exchange attempts against an API without one, want 1", n)
	}
}

func TestOtherTokensAreSentAsGiven(t *testing.T) {
	f := newFakeAPI(t)
	for _, tok := range []string{"alice", "eyJalready.an.access", "skh_sess_x"} {
		c := New(f.srv.URL, f.srv.URL, tok)
		if got := c.bearer(false); got != tok {
			t.Errorf("%q became %q", tok, got)
		}
	}
	if f.exchanges.Load() != 0 {
		t.Fatal("something other than a personal token was exchanged")
	}
}
