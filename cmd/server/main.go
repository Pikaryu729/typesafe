// Command server runs the typesafe SSH server.
//
// Every incoming SSH connection is handed its own Bubble Tea program. Any
// public key is accepted; the username the client connects with becomes their
// display name.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/activeterm"
	bm "github.com/charmbracelet/wish/bubbletea"
	"github.com/charmbracelet/wish/logging"
	"github.com/charmbracelet/wish/recover"
	"github.com/muesli/termenv"
)

// shutdownTimeout bounds how long we wait for live sessions to drain before
// exiting anyway.
const shutdownTimeout = 10 * time.Second

func main() {
	var (
		host        = flag.String("host", "0.0.0.0", "address to bind the SSH server to")
		port        = flag.String("port", "2222", "port to listen on")
		hostKeyPath = flag.String("host-key", ".ssh/typesafe_ed25519", "path to the SSH host key, generated if absent")
	)
	flag.Parse()

	if err := run(*host, *port, *hostKeyPath); err != nil {
		log.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func run(host, port, hostKeyPath string) error {
	addr := net.JoinHostPort(host, port)

	srv, err := wish.NewServer(
		wish.WithAddress(addr),
		wish.WithHostKeyPath(hostKeyPath),
		// Accept any key: identity is just the username the client chose.
		wish.WithPublicKeyAuth(func(ssh.Context, ssh.PublicKey) bool { return true }),
		wish.WithMiddleware(
			// Middleware runs in reverse order of this list, so logging sees
			// the session first and the TUI is innermost.
			recover.Middleware(teaMiddleware()),
			activeterm.Middleware(), // the TUI is unusable without a PTY
			logging.Middleware(),
		),
	)
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}

	errs := make(chan error, 1)
	go func() {
		log.Info("typesafe listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, ssh.ErrServerClosed) {
			errs <- err
		}
		close(errs)
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errs:
		return err
	case <-stop:
	}

	log.Info("shutting down", "timeout", shutdownTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// teaMiddleware builds the Bubble Tea middleware.
//
// MiddlewareWithProgramHandler is used rather than the simpler Middleware
// because it exposes the *tea.Program. Races need that handle: shared lobby
// state pushes updates into a session with Program.Send, since a Bubble Tea
// View cannot read shared state directly.
func teaMiddleware() wish.Middleware {
	return bm.MiddlewareWithProgramHandler(newProgram, termenv.ANSI256)
}

func newProgram(sess ssh.Session) *tea.Program {
	pty, _, ok := sess.Pty()
	if !ok {
		return nil // activeterm rejects these, but do not assume it ran
	}

	m := placeholderModel{
		username: sess.User(),
		width:    pty.Window.Width,
		height:   pty.Window.Height,
	}

	opts := append(bm.MakeOptions(sess), tea.WithAltScreen())
	return tea.NewProgram(m, opts...)
}

// placeholderModel is a stand-in until the real UI router lands. It confirms
// that the SSH plumbing, PTY sizing and rendering all work end to end.
type placeholderModel struct {
	username string
	width    int
	height   int
}

func (m placeholderModel) Init() tea.Cmd { return nil }

func (m placeholderModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m placeholderModel) View() string {
	return fmt.Sprintf("typesafe\n\nhello, %s\nterminal: %dx%d\n\npress q to quit\n",
		m.username, m.width, m.height)
}
