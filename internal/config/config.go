// Package config stores the CLI's credentials on disk.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is what the CLI remembers between runs.
type Config struct {
	// API and Ingest are separate because they are separate services: api owns
	// the database, ingest inflates archives and owns none. A deployment can put
	// them behind one host, but the CLI must not assume it.
	API    string `json:"api,omitempty"`
	Ingest string `json:"ingest,omitempty"`
	Token  string `json:"token,omitempty"`
	Handle string `json:"handle,omitempty"`
}

// DefaultAPI and DefaultIngest are where an llmsh with no configuration talks to.
//
// The public deployment, not localhost. Somebody who installs this has no
// services of their own, and a CLI whose out-of-the-box behaviour is
// "connection refused" teaches them it is broken before it teaches them
// anything else. Developers override with LLMSH_API and LLMSH_INGEST, which
// is one line in a shell profile and is done by the people best placed to know
// they need it.
//
// Both point at the same host because the deployment puts both services behind
// one: Caddy sends /v1/validate, /v1/upload and /v1/frontmatter to ingest and
// everything else to the api. They stay two settings because they are two
// services -- a deployment that splits them again should not need a new CLI.
//
// var, not const, so a build can pin them:
//
//	go build -ldflags "-X ...config.DefaultAPI=https://staging.example.com"
var (
	DefaultAPI    = "https://api.llmskillhub.com"
	DefaultIngest = "https://api.llmskillhub.com"
)

// Path is where the config lives.
//
// Under the XDG config directory rather than a dotfile in $HOME, because this
// file holds a credential and belongs with the others.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "llmsh", "config.json"), nil
}

// Load reads the config, applying environment overrides.
//
// The environment wins over the file so a CI job can point at a different
// deployment, or use a token, without writing anything to disk -- a credential
// written to disk in CI outlives the job.
func Load() (*Config, error) {
	c := &Config{API: DefaultAPI, Ingest: DefaultIngest}

	p, err := Path()
	if err == nil {
		b, err := os.ReadFile(p)
		switch {
		case err == nil:
			if err := json.Unmarshal(b, c); err != nil {
				return nil, fmt.Errorf("%s is not valid JSON: %w", p, err)
			}
		case !errors.Is(err, os.ErrNotExist):
			return nil, err
		}
	}

	if v := env("LLMSH_API", "ALPHAQ_API"); v != "" {
		c.API = v
	}
	if v := env("LLMSH_INGEST", "ALPHAQ_INGEST"); v != "" {
		c.Ingest = v
	}
	if v := env("LLMSH_TOKEN", "ALPHAQ_TOKEN"); v != "" {
		c.Token = v
		c.Handle = "" // whoever the env token belongs to, not the saved handle
	}
	c.API = strings.TrimSuffix(c.API, "/")
	c.Ingest = strings.TrimSuffix(c.Ingest, "/")
	return c, nil
}

// env reads the first of several names that is set.
//
// The tool was called aq and its variables ALPHAQ_. Renaming without a
// fallback would break every shell profile and CI job already configured, to
// save reading one extra variable -- so the old names still work and the new
// ones win.
func env(names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}

// Save writes the config with owner-only permissions.
//
// Written to a temp file and renamed, so an interrupted write cannot leave a
// truncated file that would silently look like "not logged in". The 0600 is set
// on the temp file before any content reaches it, since a file that is briefly
// world-readable is world-readable.
func (c *Config) Save() error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	// An address equal to the built-in default is not written.
	//
	// Otherwise logging in freezes today's default into the file forever, and
	// the next release that moves it reaches nobody who has ever run
	// `llmsh login` -- which is everybody who uses this. Writing only a genuine
	// override keeps the default a default.
	//
	// Found the obvious way: a config written when the default was localhost
	// kept sending a binary built for production at a machine that was not
	// running one, and the error it produced was "Not Found".
	out := *c
	if out.API == DefaultAPI {
		out.API = ""
	}
	if out.Ingest == DefaultIngest {
		out.Ingest = ""
	}

	b, err := json.MarshalIndent(&out, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Clear removes the stored credential but keeps the endpoints, so logging out
// does not also forget which deployment you were talking to.
func Clear() error {
	c, err := Load()
	if err != nil {
		return err
	}
	c.Token, c.Handle = "", ""
	return c.Save()
}
