// Package client talks to the alphaQ services.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	API    string
	Ingest string
	Token  string
	HTTP   *http.Client
}

func New(api, ingest, token string) *Client {
	return &Client{API: api, Ingest: ingest, Token: token,
		HTTP: &http.Client{Timeout: 60 * time.Second}}
}

// Problem is the API's error shape. Rendered rather than swallowed, because the
// server's hint is usually the whole answer.
type Problem struct {
	Title  string `json:"title"`
	Status int    `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
	Hint   string `json:"hint"`
}

func (p *Problem) Error() string {
	msg := p.Detail
	if msg == "" {
		msg = p.Title
	}
	if p.Hint != "" {
		msg += "\n  " + p.Hint
	}
	return msg
}

func (c *Client) do(req *http.Request, out any) error {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		// A transport failure names the address, because "connection refused"
		// without one sends people to check the wrong service.
		return fmt.Errorf("could not reach %s: %w", req.URL.Host, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))

	if resp.StatusCode == 401 {
		return fmt.Errorf("not signed in, or the token was revoked\n  Run: llmsh login")
	}
	if resp.StatusCode >= 400 {
		var p Problem
		if json.Unmarshal(body, &p) == nil && (p.Code != "" || p.Detail != "") {
			return &p
		}
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

func (c *Client) getJSON(path string, out any) error {
	req, err := http.NewRequest("GET", c.API+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

type Me struct {
	ID      string `json:"id"`
	Handle  string `json:"handle"`
	IsAdmin bool   `json:"is_admin"`
}

func (c *Client) Me() (*Me, error) {
	var m Me
	return &m, c.getJSON("/v1/me", &m)
}

// Violation mirrors the server's, so the CLI prints the same codes a reviewer
// will quote back.
type Violation struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Message  string `json:"message"`
	Hint     string `json:"hint"`
}

type UploadResponse struct {
	OK          bool        `json:"ok"`
	DryRun      bool        `json:"dry_run"`
	Slug        string      `json:"slug"`
	Version     string      `json:"version"`
	VersionID   string      `json:"version_id"`
	ReviewState string      `json:"review_state"`
	Digest      string      `json:"digest"`
	ShortDigest string      `json:"short_digest"`
	Size        int64       `json:"size"`
	Message     string      `json:"message"`
	Violations  []Violation `json:"violations"`
	Scores      struct {
		Description struct {
			Total int `json:"total"`
		} `json:"description"`
		Completeness struct {
			Total int `json:"total"`
		} `json:"completeness"`
	} `json:"scores"`
}

// Publish uploads an archive to ingest, which validates it and asks api to
// record it. The CLI never talks to api for publishing: that boundary is the
// reason ingest exists.
//
// dryRun selects a different ENDPOINT, not a different parameter. /v1/validate
// inflates and checks the archive and stores nothing; /v1/upload stores. A flag
// that only changed what the CLI printed -- which is what this was at first --
// promises one thing and does the other, which is worse than not offering it.
func (c *Client) Publish(slug, version string, archive []byte, dryRun bool) (*UploadResponse, error) {
	path := "/v1/upload"
	q := url.Values{"slug": {slug}}
	if version != "" {
		q.Set("version", version)
	}
	if dryRun {
		// validate takes no slug or version: it reports on the bytes alone.
		path, q = "/v1/validate", url.Values{}
	}
	req, err := http.NewRequest("POST", c.Ingest+path+"?"+q.Encode(), bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/zip")
	var out UploadResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type Version struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Digest      string `json:"digest"`
	ShortDigest string `json:"short_digest"`
	Size        int64  `json:"size"`
	FileCount   int    `json:"file_count"`
	PublishedAt string `json:"published_at"`
	ReviewState string `json:"review_state"`
	ReviewNotes string `json:"review_notes"`
	Yanked      bool   `json:"yanked"`
}

func (c *Client) Versions(owner, slug string) ([]Version, error) {
	var out struct {
		Versions []Version `json:"versions"`
	}
	err := c.getJSON(fmt.Sprintf("/v1/skills/%s/%s/versions",
		url.PathEscape(owner), url.PathEscape(slug)), &out)
	return out.Versions, err
}

type ReviewDecision struct {
	Version     string   `json:"version"`
	ReviewState string   `json:"review_state"`
	ReasonText  []string `json:"reason_text"`
	Notes       string   `json:"notes"`
	Reviewer    string   `json:"reviewer"`
}

type Skill struct {
	Owner  string `json:"owner"`
	Slug   string `json:"slug"`
	Status string `json:"status"`
}

type Profile struct {
	Skills   []Skill                     `json:"skills"`
	Versions map[string][]Version        `json:"versions"`
	History  map[string][]ReviewDecision `json:"review_history"`
}

func (c *Client) Profile(handle string) (*Profile, error) {
	var p Profile
	return &p, c.getJSON("/v1/users/"+url.PathEscape(handle), &p)
}

// FileContent is one file of a published version, for diffing against local.
type FileContent struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	IsText  bool   `json:"is_text"`
	Size    int64  `json:"size"`
}

func (c *Client) File(owner, slug, version, path string) (*FileContent, error) {
	var f FileContent
	q := url.Values{"version": {version}, "path": {path}}
	return &f, c.getJSON(fmt.Sprintf("/v1/skills/%s/%s/file?%s",
		url.PathEscape(owner), url.PathEscape(slug), q.Encode()), &f)
}

type FileEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func (c *Client) Files(owner, slug, version string) ([]FileEntry, error) {
	var out struct {
		Files []FileEntry `json:"files"`
	}
	q := url.Values{"version": {version}}
	err := c.getJSON(fmt.Sprintf("/v1/skills/%s/%s/files?%s",
		url.PathEscape(owner), url.PathEscape(slug), q.Encode()), &out)
	return out.Files, err
}

// Download fetches a version's archive.
//
// The API answers with a redirect to a short-lived URL on the object store, so
// the bytes never pass through it. The digest comes back in a header on the
// redirect itself, which is the response the API signed off on -- reading it
// from the final hop would mean trusting whatever served the bytes.
func (c *Client) Download(owner, slug, version string) (archive []byte, digest string, err error) {
	q := url.Values{}
	if version != "" {
		q.Set("version", version)
	}
	u := fmt.Sprintf("%s/v1/skills/%s/%s/download?%s",
		c.API, url.PathEscape(owner), url.PathEscape(slug), q.Encode())

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, "", err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	// Stop at the redirect so the headers the API set are readable, and so the
	// Authorization header is not replayed to the object store, which neither
	// needs it nor should see it.
	noFollow := &http.Client{
		Timeout: c.HTTP.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := noFollow.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("could not reach %s: %w", req.URL.Host, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == 404:
		return nil, "", fmt.Errorf("no such skill or version, or it is not public yet")
	case resp.StatusCode >= 400:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var p Problem
		if json.Unmarshal(body, &p) == nil && p.Detail != "" {
			return nil, "", &p
		}
		return nil, "", fmt.Errorf("%s", resp.Status)
	}

	digest = resp.Header.Get("X-Skill-Digest")
	loc := resp.Header.Get("Location")
	if loc == "" {
		// A deployment serving bytes directly rather than redirecting.
		b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		return b, digest, err
	}

	get, err := http.NewRequest("GET", loc, nil)
	if err != nil {
		return nil, "", err
	}
	blob, err := c.HTTP.Do(get)
	if err != nil {
		return nil, "", fmt.Errorf("could not fetch the archive: %w", err)
	}
	defer blob.Body.Close()
	if blob.StatusCode != 200 {
		return nil, "", fmt.Errorf("the download link returned %s", blob.Status)
	}
	b, err := io.ReadAll(io.LimitReader(blob.Body, 64<<20))
	return b, digest, err
}
