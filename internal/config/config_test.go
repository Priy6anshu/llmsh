package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A config that records the defaults freezes them. The next release that moves
// an address would then reach nobody who had ever run `llmsh login`, which is
// everybody who uses this.
func TestSaveDoesNotFreezeTheDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	c := &Config{API: DefaultAPI, Ingest: DefaultIngest, Token: "t", Handle: "someone"}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk map[string]any
	if err := json.Unmarshal(b, &onDisk); err != nil {
		t.Fatal(err)
	}
	if _, ok := onDisk["api"]; ok {
		t.Errorf("the default api was written to disk: %s", b)
	}
	if _, ok := onDisk["ingest"]; ok {
		t.Errorf("the default ingest was written to disk: %s", b)
	}
	if onDisk["token"] != "t" {
		t.Errorf("the token was not saved: %s", b)
	}

	// And a later build with a different default picks it up.
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.API != DefaultAPI || got.Token != "t" {
		t.Errorf("Load = %+v", got)
	}
}

// A real override is a choice, and choices are kept.
func TestSaveKeepsAnActualOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	c := &Config{API: "https://staging.example.com", Ingest: DefaultIngest, Token: "t"}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.API != "https://staging.example.com" {
		t.Errorf("api = %q; an override was discarded", got.API)
	}
	if got.Ingest != DefaultIngest {
		t.Errorf("ingest = %q, want the default", got.Ingest)
	}
	p, _ := Path()
	b, _ := os.ReadFile(filepath.Clean(p))
	var onDisk map[string]any
	_ = json.Unmarshal(b, &onDisk)
	if _, ok := onDisk["ingest"]; ok {
		t.Errorf("the default ingest was written alongside the override: %s", b)
	}
}
