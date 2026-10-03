package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Access tokens, from the CLI's side.
//
// The token someone pastes into `llmsh login` is a personal token (skh_live_),
// opaque, and the API has to look it up in its database every time it sees
// one. So llmsh trades it once for a short-lived access token -- signed, and
// checked by the API without the database -- and sends that instead. A command
// making a dozen requests costs the API one lookup rather than a dozen, and a
// second command within a few minutes costs none, because the access token is
// kept on disk next to the config.
//
// Nothing here is required for llmsh to work. An API that predates the
// exchange still takes the personal token directly, and so does every other
// kind of token this client might be given.

const (
	personalTokenPrefix = "skh_live_"
	// Exchange again this long before expiry, so a request that leaves with a
	// valid token does not arrive with an expired one.
	refreshEarly = 60 * time.Second
)

type accessCache struct {
	API string `json:"api"`
	// For is a fingerprint of the personal token this was exchanged from, so a
	// cached access token is never used after logging in as someone else.
	// Truncated SHA-256 of a 256-bit random token: it identifies, and gives
	// nothing away.
	For       string    `json:"for"`
	Token     string    `json:"access_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func fingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}

// bearer is the credential to send: an access token when the client holds a
// personal token and the API will exchange it, and otherwise the token as given.
func (c *Client) bearer(force bool) string {
	if !strings.HasPrefix(c.Token, personalTokenPrefix) {
		return c.Token
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.direct {
		return c.Token
	}
	if !force {
		if c.access != "" && time.Until(c.accessExp) > refreshEarly {
			return c.access
		}
		if tok, exp, ok := c.loadCache(); ok {
			c.access, c.accessExp = tok, exp
			return tok
		}
	}
	tok, exp, err := c.exchange()
	if err != nil {
		var old *oldServerError
		if errors.As(err, &old) {
			// Not an error to report: this API takes the personal token itself.
			c.direct = true
		}
		// Otherwise send the personal token this once. If it is bad the request
		// fails with the API's own explanation, which says more than ours would.
		return c.Token
	}
	c.access, c.accessExp = tok, exp
	c.saveCache(tok, exp)
	return tok
}

type oldServerError struct{ status int }

func (e *oldServerError) Error() string { return fmt.Sprintf("no token exchange (%d)", e.status) }

func (c *Client) exchange() (string, time.Time, error) {
	req, err := http.NewRequest("POST", c.API+"/v1/auth/token", nil)
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 200:
	case 404, 405, 501:
		return "", time.Time{}, &oldServerError{resp.StatusCode}
	default:
		return "", time.Time{}, fmt.Errorf("token exchange: %s", resp.Status)
	}
	var out struct {
		AccessToken string    `json:"access_token"`
		ExpiresAt   time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("token exchange: unreadable answer")
	}
	return out.AccessToken, out.ExpiresAt, nil
}

func (c *Client) loadCache() (string, time.Time, bool) {
	if c.CachePath == "" {
		return "", time.Time{}, false
	}
	raw, err := os.ReadFile(c.CachePath)
	if err != nil {
		return "", time.Time{}, false
	}
	var ac accessCache
	if json.Unmarshal(raw, &ac) != nil {
		return "", time.Time{}, false
	}
	if ac.API != c.API || ac.For != fingerprint(c.Token) || time.Until(ac.ExpiresAt) <= refreshEarly {
		return "", time.Time{}, false
	}
	return ac.Token, ac.ExpiresAt, true
}

// saveCache writes the access token where the next command will find it.
// Best effort: a cache that cannot be written costs an exchange next time.
// 0600 like the config beside it, and written whole or not at all.
func (c *Client) saveCache(tok string, exp time.Time) {
	if c.CachePath == "" {
		return
	}
	b, err := json.Marshal(accessCache{API: c.API, For: fingerprint(c.Token), Token: tok, ExpiresAt: exp})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.CachePath), 0o700); err != nil {
		return
	}
	tmp := c.CachePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, c.CachePath)
}

// ForgetAccess drops any cached access token, for login and logout.
func ForgetAccess(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}

// send attaches the bearer and makes the request with hc. If an access token is
// refused -- it expired early, or the API's signing key changed -- it is
// exchanged afresh and the request made once more, so neither shows up as
// "not signed in".
func (c *Client) send(hc *http.Client, req *http.Request) (*http.Response, error) {
	tok := c.bearer(false)
	setBearer(req, tok)
	resp, err := hc.Do(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized || tok == "" || tok == c.Token {
		return resp, err
	}
	fresh := c.bearer(true)
	if fresh == tok {
		return resp, nil
	}
	retry, rerr := rewind(req)
	if rerr != nil {
		return resp, nil
	}
	resp.Body.Close()
	setBearer(retry, fresh)
	return hc.Do(retry)
}

func setBearer(req *http.Request, tok string) {
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
}

// rewind returns a copy of req that can be sent again. Bodies built from
// bytes, which every request here is, can be replayed; anything else cannot,
// and the original answer stands.
func rewind(req *http.Request) (*http.Request, error) {
	r := req.Clone(req.Context())
	if req.Body != nil && req.Body != http.NoBody {
		if req.GetBody == nil {
			return nil, errors.New("request body cannot be replayed")
		}
		b, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		r.Body = b
	}
	return r, nil
}
