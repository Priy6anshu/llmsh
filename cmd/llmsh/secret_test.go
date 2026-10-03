package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestTheSecretIsNeverEchoed(t *testing.T) {
	const tok = "skh_live_AbCdEfGhIjKlMnOp1234"
	var out bytes.Buffer
	got, err := editLine(strings.NewReader(tok+"\r"), &out)
	if err != nil || got != tok {
		t.Fatalf("got %q, %v", got, err)
	}
	if strings.Contains(out.String(), "skh_") || strings.Contains(out.String(), "1234") {
		t.Fatalf("the token reached the screen: %q", out.String())
	}
	if out.String() != strings.Repeat("*", len(tok)) {
		t.Fatalf("want one star per character, got %q", out.String())
	}
}

func TestEditingKeys(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"backspace", "abx\x7fc\r", "abc"},
		{"ctrl-h", "abx\x08c\r", "abc"},
		{"backspace past the start", "\x7f\x7fab\r", "ab"},
		{"ctrl-u starts again", "wrong\x15right\r", "right"},
		{"arrow keys insert nothing", "ab\x1b[D\x1b[Cc\r", "abc"},
		{"newline ends it", "abc\nignored", "abc"},
		{"control characters dropped", "a\x01b\x02c\r", "abc"},
		{"multi-byte erased whole", "aé\x7fb\r", "ab"},
		{"ctrl-d with input ends it", "abc\x04", "abc"},
		{"end of input with text", "abc", "abc"},
	}
	for _, c := range cases {
		got, err := editLine(strings.NewReader(c.in), io.Discard)
		if err != nil || got != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

func TestCancelling(t *testing.T) {
	if _, err := editLine(strings.NewReader("abc\x03def\r"), io.Discard); !errors.Is(err, errCancelled) {
		t.Fatalf("ctrl-c: %v, want errCancelled", err)
	}
	if _, err := editLine(strings.NewReader("\x04"), io.Discard); !errors.Is(err, io.EOF) {
		t.Fatalf("ctrl-d on an empty line: %v, want EOF", err)
	}
}

func TestMaskShowsOnlyTheTail(t *testing.T) {
	for in, want := range map[string]string{
		"skh_live_AbCdEfGhIjKlMnOp1234": "**********1234",
		"abcdefghijkl":                  "**********ijkl",
		"short":                         "*****",
		"":                              "",
	} {
		if got := maskSecret(in); got != want {
			t.Errorf("maskSecret(%q) = %q, want %q", in, got, want)
		}
	}
}
