package credentials

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// ErrNotATerminal means a secret was asked for where input cannot be hidden.
//
// The resolver turns this into the ordinary "credential missing,
// non-interactive" error, because refusing is the only safe answer: falling
// back to an echoed read would print the user's token into whatever captured
// that session — a CI log, a tmux scrollback, a screen recording — which is
// exactly the class of leak docs/security.md threat 4 rules out.
var ErrNotATerminal = errors.New("cannot prompt for a secret: input is not a terminal")

// Prompt is what the user is asked for. Everything in it comes from the
// manifest and is non-secret: the injection point, plus the description and
// obtain URL that tell the user what to get and where to get it.
type Prompt struct {
	Name        string
	Header      bool
	Server      string
	Description string
	ObtainURL   string
}

// Prompter collects a credential from a human.
//
// It is an interface so the resolver can be driven in tests without a pty, and
// so a non-interactive restore expresses itself by having no Prompter at all
// rather than by a flag every layer has to pass along.
type Prompter interface {
	// Secret asks for the credential's value. Implementations must not echo
	// what is typed.
	Secret(p Prompt) (Value, error)
	// Confirm asks a yes/no question and must default to no: its only caller
	// is the offer to write a secret into the OS keychain, and that must
	// never happen because someone pressed enter.
	Confirm(question string) (bool, error)
}

// TerminalPrompter asks on a real terminal, reading the secret with echo
// disabled.
//
// Output goes to stderr by default, never stdout: restore's stdout is the
// plan, and a prompt written into it would corrupt any piped or --json
// consumer. It also keeps the prompt visible when output is redirected, which
// is the moment a hidden prompt looks most like a hang.
type TerminalPrompter struct {
	// Out receives the prompt text. nil means os.Stderr.
	Out io.Writer
	// In is read from. nil means os.Stdin.
	In *os.File

	// ReadSecret reads one line with echo disabled; nil means the real
	// terminal. ReadLine reads one echoed line; nil means buffered reads from
	// In. Both are fields rather than methods so a test can drive the whole
	// prompt flow — including the redaction guarantees on what gets printed —
	// without allocating a pty.
	ReadSecret func() (string, error)
	ReadLine   func() (string, error)

	lines *bufio.Reader
}

// Secret prints what the credential is and where to obtain it, then reads the
// value without echoing it.
//
// The description and URL are shown at the prompt rather than only in the
// earlier plan output because this is the moment the user has to act: they are
// being asked for a token they may not have yet, and the answer to "which
// token, from where?" needs to be on screen right then.
func (t *TerminalPrompter) Secret(p Prompt) (Value, error) {
	out := t.out()

	kind := "environment variable"
	if p.Header {
		kind = "header"
	}
	fmt.Fprintf(out, "\ncredential %s (%s)", p.Name, kind)
	if p.Server != "" {
		fmt.Fprintf(out, " for MCP server %s", p.Server)
	}
	fmt.Fprintln(out)
	if p.Description != "" {
		fmt.Fprintf(out, "  %s\n", p.Description)
	}
	if p.ObtainURL != "" {
		fmt.Fprintf(out, "  obtain: %s\n", p.ObtainURL)
	}
	fmt.Fprintf(out, "  value (input hidden): ")

	raw, err := t.readSecret()
	// The hidden read swallows the user's newline, so echo one back or the
	// next line of output lands on the prompt.
	fmt.Fprintln(out)
	if err != nil {
		return Value{}, err
	}

	// Pasted tokens routinely arrive with a trailing newline or a stray space
	// from a copy that grabbed one. No real credential has leading or
	// trailing whitespace, and an untrimmed one fails later as an opaque
	// authentication error.
	return NewValue(strings.TrimSpace(raw)), nil
}

// Confirm asks a yes/no question, defaulting to no. Only an explicit y/yes is
// a yes; every other answer, including an unreadable one, declines.
func (t *TerminalPrompter) Confirm(question string) (bool, error) {
	out := t.out()
	fmt.Fprintf(out, "  %s [y/N]: ", question)
	line, err := t.readLine()
	if err != nil {
		fmt.Fprintln(out)
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

func (t *TerminalPrompter) out() io.Writer {
	if t.Out == nil {
		return os.Stderr
	}
	return t.Out
}

func (t *TerminalPrompter) in() *os.File {
	if t.In == nil {
		return os.Stdin
	}
	return t.In
}

func (t *TerminalPrompter) readSecret() (string, error) {
	if t.ReadSecret != nil {
		return t.ReadSecret()
	}
	fd := int(t.in().Fd())
	if !term.IsTerminal(fd) {
		return "", ErrNotATerminal
	}
	b, err := term.ReadPassword(fd)
	if err != nil {
		return "", fmt.Errorf("reading hidden input: %w", err)
	}
	return string(b), nil
}

func (t *TerminalPrompter) readLine() (string, error) {
	if t.ReadLine != nil {
		return t.ReadLine()
	}
	// One reader for the life of the prompter: a fresh bufio.Reader per call
	// can buffer past the current line and drop the next answer.
	if t.lines == nil {
		t.lines = bufio.NewReader(t.in())
	}
	line, err := t.lines.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
}
