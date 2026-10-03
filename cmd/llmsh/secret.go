package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// Reading a token without putting it on the screen.
//
// A token typed or pasted at an ordinary prompt is echoed in full, and from
// there it is in the terminal's scrollback, in a screen share, in a screenshot
// somebody pastes into an issue. So the prompt echoes a star per character --
// enough to see that a paste arrived -- and once Enter is pressed the line is
// redrawn as the last four characters, the same tail the website's token list
// shows, so the person can still tell which token they pasted.

var errCancelled = errors.New("cancelled")

// readSecret prompts for a secret on the terminal. When stdin is not a
// terminal -- piped, as in `echo $TOKEN | llmsh login` -- there is no echo to
// suppress, and the line is read as it is.
func readSecret(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		fmt.Print(prompt)
		b, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && b == "" {
			return "", err
		}
		return strings.TrimSpace(b), nil
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	// Restored on every path out, including Ctrl-C, which raw mode delivers as
	// a byte rather than a signal -- a terminal left raw is unusable afterwards.
	defer term.Restore(fd, old)

	fmt.Print(prompt)
	secret, err := editLine(os.Stdin, os.Stdout)
	// Raw mode does not translate "\n", so the cursor is returned by hand.
	if err != nil {
		fmt.Print("\r\n")
		return "", err
	}
	fmt.Printf("\r\033[K%s%s\r\n", prompt, maskSecret(secret))
	return secret, nil
}

// editLine reads one line of input byte by byte, echoing a star for each
// character and handling the few editing keys someone pasting a token uses.
// Separate from readSecret so it can be tested without a terminal.
func editLine(r io.Reader, w io.Writer) (string, error) {
	var buf []rune
	in := bufio.NewReader(r)
	for {
		b, err := in.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) && len(buf) > 0 {
				return string(buf), nil
			}
			return "", err
		}
		switch {
		case b == '\r' || b == '\n':
			return string(buf), nil
		case b == 3: // Ctrl-C
			return "", errCancelled
		case b == 4: // Ctrl-D: end of input, as at any other prompt
			if len(buf) == 0 {
				return "", io.EOF
			}
			return string(buf), nil
		case b == 127 || b == 8: // Backspace, Ctrl-H
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
				io.WriteString(w, "\b \b")
			}
		case b == 21: // Ctrl-U: start again
			io.WriteString(w, strings.Repeat("\b \b", len(buf)))
			buf = buf[:0]
		case b == 27: // Escape: an arrow key or similar. Skip the sequence.
			skipEscape(in)
		case b < 32:
			// Other control characters have no business in a token.
		default:
			// A multi-byte character arrives as several bytes; take the rest
			// so it counts, and is erased, as one.
			if b >= utf8.RuneSelf {
				in.UnreadByte()
				ru, _, err := in.ReadRune()
				if err != nil {
					return "", err
				}
				buf = append(buf, ru)
			} else {
				buf = append(buf, rune(b))
			}
			io.WriteString(w, "*")
		}
	}
}

// skipEscape consumes the rest of a CSI sequence such as "\x1b[A", so arrow
// keys move nothing and insert nothing.
func skipEscape(in *bufio.Reader) {
	if next, err := in.Peek(1); err != nil || next[0] != '[' {
		return
	}
	in.ReadByte()
	for {
		c, err := in.ReadByte()
		if err != nil || (c >= 0x40 && c <= 0x7e) {
			return
		}
	}
}

// maskSecret shows a secret as stars and its last four characters. The number
// of stars is fixed rather than the secret's length, which is nobody's business.
func maskSecret(s string) string {
	r := []rune(s)
	if len(r) <= 8 {
		return strings.Repeat("*", len(r))
	}
	return strings.Repeat("*", 10) + string(r[len(r)-4:])
}
