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
	gossh "golang.org/x/crypto/ssh"

	"github.com/Pikaryu729/typesafe/internal/lobby"
	"github.com/Pikaryu729/typesafe/internal/store"
	"github.com/Pikaryu729/typesafe/internal/store/pg"
	"github.com/Pikaryu729/typesafe/internal/ui"
)

// shutdownTimeout bounds how long we wait for live sessions to drain before
// exiting anyway.
const shutdownTimeout = 10 * time.Second

// startupTimeout bounds connecting to the database and running migrations.
const startupTimeout = 30 * time.Second

// resolveTimeout bounds the account lookup a connection waits on. A session
// that cannot resolve in this long proceeds anonymously rather than hanging:
// the point of the app is typing, not the database.
const resolveTimeout = 3 * time.Second

// writeQueue is how many finished runs may be waiting to be stored before new
// ones are dropped. Runs arrive at human speed, so this is only ever reached
// if the database has stopped answering, in which case dropping is the point.
const writeQueue = 256

func main() {
	var (
		host        = flag.String("host", "0.0.0.0", "address to bind the SSH server to")
		port        = flag.String("port", "2222", "port to listen on")
		hostKeyPath = flag.String("host-key", ".ssh/typesafe_ed25519", "path to the SSH host key, generated if absent")
		dsn         = flag.String("dsn", os.Getenv("TYPESAFE_DSN"), "PostgreSQL connection string; empty runs with no accounts or history")
	)
	flag.Parse()

	if err := run(*host, *port, *hostKeyPath, *dsn); err != nil {
		log.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func run(host, port, hostKeyPath, dsn string) error {
	addr := net.JoinHostPort(host, port)

	repo, closeRepo, err := openRepository(dsn)
	if err != nil {
		return err
	}
	defer closeRepo()

	// One set of shared state for the whole process. The lobby store is the
	// live state every session mutates; the repository is what outlives them.
	d := &deps{lobbies: lobby.NewStore(), repo: repo}

	srv, err := wish.NewServer(
		wish.WithAddress(addr),
		wish.WithHostKeyPath(hostKeyPath),
		wish.WithPublicKeyAuth(func(ctx ssh.Context, key ssh.PublicKey) bool {
			// Still accept every key: there are no accounts to be shut out of,
			// and the key is how you are recognised rather than how you are
			// admitted. Keeping the fingerprint is what turns "some connection"
			// into "this typist, again".
			ctx.SetValue(fingerprintKey{}, gossh.FingerprintSHA256(key))
			return true
		}),
		wish.WithMiddleware(
			// Middleware runs in reverse order of this list, so logging sees
			// the session first and the TUI is innermost. cleanupMiddleware is
			// listed first, which makes it the innermost of all: the Bubble
			// Tea middleware calls it only after the program has stopped.
			cleanupMiddleware(),
			recover.Middleware(teaMiddleware(d)),
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

// deps is the process-wide state a session needs handed to it.
type deps struct {
	lobbies *lobby.Store
	// repo is nil when the server runs without a database.
	repo store.Repository
}

// openRepository connects to dsn, migrates it, and wraps it so writes never
// block a session. An empty dsn is not an error: it is how the server runs
// with no accounts and no history, exactly as it did before there was a
// database.
//
// A dsn that is set but unreachable *is* an error, deliberately. Starting
// anyway would give a server that looks healthy to the deploy's checks while
// silently recording nothing, and a typo in the DSN would be discovered days
// later by a user wondering where their history went. systemd restarts us, so
// a database that is merely slow to come up resolves itself.
func openRepository(dsn string) (store.Repository, func(), error) {
	if dsn == "" {
		log.Warn("no -dsn given: accounts and history are disabled")
		return nil, func() {}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()

	repo, err := pg.New(ctx, dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := repo.Migrate(ctx); err != nil {
		repo.Close()
		return nil, nil, fmt.Errorf("migrate database: %w", err)
	}
	log.Info("database ready")

	async := store.NewAsync(repo, writeQueue, func(err error) {
		log.Error("could not store run", "error", err)
	})
	return async, func() {
		// Drain queued runs before the pool goes away, so a clean shutdown
		// does not throw away the race that just finished.
		_ = async.Close()
		repo.Close()
	}, nil
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
func teaMiddleware(d *deps) wish.Middleware {
	handler := func(sess ssh.Session) *tea.Program { return newProgram(sess, d) }
	return bm.MiddlewareWithProgramHandler(handler, termenv.ANSI256)
}

func newProgram(sess ssh.Session, d *deps) *tea.Program {
	pty, _, ok := sess.Pty()
	if !ok {
		return nil // activeterm rejects these, but do not assume it ran
	}

	fingerprint, _ := sess.Context().Value(fingerprintKey{}).(string)
	user := resolveUser(sess, d.repo, fingerprint)

	// The account's name wins over whatever was typed at the ssh prompt, so a
	// returning typist is shown to others under the name their history is
	// under rather than a new one each connection.
	name := sess.User()
	if user.DisplayName != "" {
		name = user.DisplayName
	}

	m := ui.NewRoot(ui.Config{
		Username: name,
		// Identity is per session, not per name: any key is accepted, so two
		// people can connect as the same user and one person can hold several
		// sessions at once.
		PlayerID:    lobby.NewPlayerID(),
		Store:       d.lobbies,
		User:        user,
		Fingerprint: fingerprint,
		Repo:        d.repo,
		Wallet:      loadWallet(sess, d.repo, user),
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

// resolveUser finds the account behind this session's key, creating one on a
// first connection.
//
// Every failure here is survivable and none of them may refuse the connection:
// an anonymous session types and races exactly as before, it just has no
// history. That is the whole reason the return value is a zero User rather
// than an error.
func resolveUser(sess ssh.Session, repo store.Repository, fingerprint string) store.User {
	if repo == nil || fingerprint == "" {
		return store.User{}
	}

	ctx, cancel := context.WithTimeout(sess.Context(), resolveTimeout)
	defer cancel()

	user, err := repo.ResolveUser(ctx, fingerprint, sess.User())
	if err != nil {
		log.Error("could not resolve account; continuing anonymously",
			"user", sess.User(), "error", err)
		return store.User{}
	}
	return user
}

// loadWallet reads the bytes and cosmetics this account connected with.
//
// It happens once, here, rather than when a screen asks: the balance belongs
// on the main menu and the equipped cosmetics have to be known before the
// first lobby is joined, so both would otherwise need a query in the middle of
// a render. One read at login is cheaper than either.
//
// Failure is survivable in the same way resolveUser's is — a zero Wallet is a
// typist with nothing bought and nothing saved up, which is a state the whole
// app already handles — so it must not refuse the connection.
func loadWallet(sess ssh.Session, repo store.Repository, user store.User) store.Wallet {
	return loadWalletContext(sess.Context(), sess.User(), repo, user)
}

func loadWalletContext(parent context.Context, username string, repo store.Repository, user store.User) store.Wallet {
	if repo == nil || user.ID == "" {
		return store.Wallet{}
	}

	ctx, cancel := context.WithTimeout(parent, resolveTimeout)
	defer cancel()

	if err := store.Flush(ctx, repo); err != nil {
		log.Error("could not flush wallet writes; continuing with none",
			"user", username, "error", err)
		return store.Wallet{}
	}

	w, err := repo.Wallet(ctx, user.ID)
	if err != nil {
		log.Error("could not load wallet; continuing with none",
			"user", username, "error", err)
		return store.Wallet{}
	}
	return w
}

// sessionContextKey retrieves a session's ui.Context from its SSH context.
type sessionContextKey struct{}

// fingerprintKey retrieves the SHA256 fingerprint of the public key a session
// authenticated with, stored during the auth callback because that is the only
// place the key is offered to us.
type fingerprintKey struct{}

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
