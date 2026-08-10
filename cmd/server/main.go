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

	"github.com/Pikaryu729/typesafe/internal/lobby"
	"github.com/Pikaryu729/typesafe/internal/ui"
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

	// One store for the whole process: this is the state every session shares.
	store := lobby.NewStore()

	srv, err := wish.NewServer(
		wish.WithAddress(addr),
		wish.WithHostKeyPath(hostKeyPath),
		// Accept any key: identity is just the username the client chose.
		wish.WithPublicKeyAuth(func(ssh.Context, ssh.PublicKey) bool { return true }),
		wish.WithMiddleware(
			// Middleware runs in reverse order of this list, so logging sees
			// the session first and the TUI is innermost. cleanupMiddleware is
			// listed first, which makes it the innermost of all: the Bubble
			// Tea middleware calls it only after the program has stopped.
			cleanupMiddleware(),
			recover.Middleware(teaMiddleware(store)),
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
//
// The colour profile argument is a floor applied by wish's MakeRenderer, which
// newRenderer replaces; it is passed for correctness should that change.
func teaMiddleware(store *lobby.Store) wish.Middleware {
	handler := func(sess ssh.Session) *tea.Program { return newProgram(sess, store) }
	return bm.MiddlewareWithProgramHandler(handler, termenv.ANSI256)
}

func newProgram(sess ssh.Session, store *lobby.Store) *tea.Program {
	pty, _, ok := sess.Pty()
	if !ok {
		return nil // activeterm rejects these, but do not assume it ran
	}

	m := ui.NewRoot(ui.Config{
		Username: sess.User(),
		// Identity is per session, not per name: any key is accepted, so two
		// people can connect as the same user and one person can hold several
		// sessions at once.
		PlayerID: lobby.NewPlayerID(),
		Store:    store,
		// The renderer is scoped to this client's terminal rather than the
		// server's, which matters as soon as two people are connected.
		Renderer: newRenderer(sess),
		Width:    pty.Window.Width,
		Height:   pty.Window.Height,
	})

	p := tea.NewProgram(m, append(bm.MakeOptions(sess), tea.WithAltScreen())...)

	// Give the session a way to receive lobby events from other goroutines.
	// Safe unsynchronised because this happens before p.Run, on the same
	// goroutine that will then run the update loop.
	m.Context().SetSender(p.Send)

	// Stash the context so cleanupMiddleware can find it once the program ends.
	sess.Context().SetValue(sessionContextKey{}, m.Context())

	return p
}

// sessionContextKey retrieves a session's ui.Context from its SSH context.
type sessionContextKey struct{}

// cleanupMiddleware releases a session's shared state once its program stops.
//
// Without it, a client that disconnects stays in its lobby as a player nobody
// can remove: the lobby never empties, so it never closes, and the browser
// fills with ghost lobbies holding people who left.
//
// Placement matters. This is the innermost middleware, so the Bubble Tea
// middleware calls it as its next handler — which happens only after
// program.Run returns, on the session's own goroutine. Cleaning up there
// rather than from a goroutine watching the session context means it cannot
// race the update loop for the state it is tearing down. It runs after a
// panic too, because the recover middleware still calls its next handler.
func cleanupMiddleware() wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		return func(sess ssh.Session) {
			if c, ok := sess.Context().Value(sessionContextKey{}).(*ui.Context); ok {
				c.Disconnect()
			}
			next(sess)
		}
	}
}
