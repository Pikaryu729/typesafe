package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/ssh"
	"github.com/muesli/termenv"
)

// newRenderer builds a lipgloss renderer scoped to one SSH session, so styling
// follows the client's terminal rather than the server's.
//
// This deliberately does not use wish's bubbletea.MakeRenderer. That helper
// also asks the terminal for its background colour (OSC 11 plus a DA1 probe,
// with a 2s timeout) to decide light versus dark. Terminals that answer are
// fine, but when one does not, the timeout cancels the query while a blocking
// read is still outstanding on the PTY. That orphaned read then consumes
// exactly one input event and throws it away — so the user's first keypress
// after connecting silently vanishes, no matter how long they wait before
// pressing it.
//
// We have no use for the answer: the palette in internal/ui is a fixed set of
// ANSI256 colours with no light/dark variants. Skipping the query avoids both
// the lost keypress and a 2s stall on connect. Colour-profile detection still
// happens normally, from TERM, so weaker terminals degrade as usual.
func newRenderer(sess ssh.Session) *lipgloss.Renderer {
	pty, _, ok := sess.Pty()
	if !ok || pty.Term == "" || pty.Term == "dumb" {
		return lipgloss.NewRenderer(sess, termenv.WithProfile(termenv.Ascii))
	}

	env := sshEnviron(append(sess.Environ(), "TERM="+pty.Term))

	// Prefer the PTY slave, matching the input/output wish gives the program.
	// It is a real terminal, so termenv's own TTY check passes.
	if pty.Slave != nil {
		return lipgloss.NewRenderer(pty.Slave,
			termenv.WithEnvironment(env),
			termenv.WithColorCache(true),
		)
	}

	// Without a slave we write to the session itself, which is not a TTY.
	// WithUnsafe skips termenv's isatty check; without it colour detection
	// fails closed to plain ASCII even though the client is a real terminal.
	return lipgloss.NewRenderer(sess,
		termenv.WithEnvironment(env),
		termenv.WithUnsafe(),
		termenv.WithColorCache(true),
	)
}

// sshEnviron adapts an SSH session's environment to termenv.Environ, which
// termenv needs to detect the colour profile without reading the server's own
// process environment.
type sshEnviron []string

func (e sshEnviron) Environ() []string { return e }

func (e sshEnviron) Getenv(key string) string {
	prefix := key + "="
	// Later entries win, matching how the TERM we append above overrides any
	// TERM the client sent.
	for i := len(e) - 1; i >= 0; i-- {
		if strings.HasPrefix(e[i], prefix) {
			return strings.TrimPrefix(e[i], prefix)
		}
	}
	return ""
}
