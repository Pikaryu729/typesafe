# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`typesafe` is an SSH-accessible typing-practice and typing-race application. Users connect over
SSH and get a terminal UI where they can:
- Practice typing against a generated passage to improve speed and accuracy.
- Create or join lobbies and race other connected users head-to-head on the same passage.

It's a single Go server process: one SSH server that accepts connections and attaches a Bubble
Tea program to each session.

Module path: `github.com/Pikaryu729/typesafe`

## Commands

```sh
go run ./cmd/server              # run (listens on 0.0.0.0:2222)
ssh -p 2222 yourname@localhost   # connect, from another terminal

make build                       # build to bin/typesafe
make run
make test                        # go test ./... -race
make lint                        # gofmt check + go vet
make fmt                         # gofmt -w .
```

Flags: `-host`, `-port`, `-host-key` (default `.ssh/typesafe_ed25519`, generated on first run
and gitignored).

Run a single package or test: `go test ./internal/lobby`, `go test ./internal/lobby -run TestFinishOrderIsStable`.

**Always run tests with `-race`.** The lobby package is shared mutable state reached from every
session goroutine; the race detector is what catches mistakes there, and several tests exist
specifically to give it something to find.

## CI/CD

`.github/workflows/ci.yml` on every push and PR: `make lint`, a `go mod tidy` diff check,
`-race` tests with coverage, a linux amd64/arm64 cross-compile, `govulncheck`. It is also
`workflow_call`-able.

`.github/workflows/deploy.yml` on a `v*` tag or manual dispatch — never on merge, because a
deploy restarts the service and all state is in memory. It calls `ci.yml` as a gate, then
deploys to the GCE VM over the IAP tunnel using Workload Identity Federation (no stored key;
`deploy/github-oidc.sh` does the one-time setup).

**The systemd unit lives in `deploy/remote-install.sh`, in one copy.** Both `deploy/gcp.sh` and
the deploy workflow pipe that script over SSH. Change the unit there, never inline in a caller.

## Stack

The v1 line of the Charm libraries, under `github.com/charmbracelet/*`:

| Library | Version | Role |
|---|---|---|
| wish | v1.4.7 | SSH server and middleware |
| bubbletea | v1.3.10 | TUI framework, one program per session |
| lipgloss | v1.1.0 | terminal styling |

A v2 generation exists under `charm.land/*`. v1 was chosen deliberately for this build; moving
would be a contained change if the package boundaries below hold. `bubbles` is **not** a
dependency — the only widget needed so far is a four-character code prompt, which is a handful
of lines inline.

## Architecture

```
cmd/server/          entrypoint: flags, host key, middleware chain, shutdown, session renderer
internal/words/      embedded word list, seeded passage generation   (pure)
internal/typing/     keystroke-level engine: WPM, accuracy, progress (pure)
internal/economy/    what a finished attempt pays, in bytes          (pure)
internal/cosmetics/  the catalogue bytes are spent on                (pure)
internal/lobby/      server-shared state: registry, lobbies, pub/sub (pure, concurrent)
internal/store/      accounts, runs and wallets: interface, memory, async wrapper (pure)
internal/store/pg/   the PostgreSQL implementation and its migrations
internal/ui/         Bubble Tea models: router plus one per screen
```

Every package outside `ui/` has no Bubble Tea or terminal dependency and is tested without a
PTY. Keep it that way — it is what makes the concurrency and scoring logic testable.

### The two state tiers

**Session-local** (inside a `tea.Model`): the active screen, this player's input buffer, their
own WPM and accuracy, cursor position.

**Server-shared** (`lobby.Store`, one per process): the lobby registry, membership, race phase,
each racer's progress.

### Locking rules in `internal/lobby`

This is where the app's concurrency correctness lives. Two rules, both load-bearing:

1. **`Store` and `Lobby` each have a mutex, and neither is ever held while taking the other.**
   `Store.List` copies lobby pointers and releases its lock before snapshotting each one;
   `Lobby.Leave` releases its own lock before asking the store to drop it. The two orderings
   would otherwise deadlock.
2. **Broadcasts are non-blocking sends onto buffered channels**, performed while the lobby lock
   is held. A session that stops reading misses events rather than stalling everyone else's
   race. Holding the lock is safe precisely because no send can block, and it gives subscribers
   a consistent event order.

Snapshots deep-copy their player slice, so a value handed to another goroutine never aliases
live state.

### How shared state reaches the screen

A Bubble Tea `View()` cannot read shared state directly, so updates arrive as messages:

1. On joining, the session registers a buffered `chan lobby.Event`.
2. A per-session goroutine forwards that channel into `Program.Send`, which is why the server
   uses `bubbletea.MiddlewareWithProgramHandler` rather than the plain `Middleware`.
3. Lobby events are plain structs. `tea.Msg` is `any`, so they satisfy it without `lobby`
   importing Bubble Tea — the dependency runs one way.

The pump goroutine ends when the lobby closes the channel. There is nothing to cancel.

### Race synchronisation

- The countdown is **one lobby-owned goroutine broadcasting ticks**, not each session timing its
  own.
- `RaceStarted` carries an **absolute `StartAt`**, so every racer measures elapsed time from the
  same instant regardless of latency. It also carries the **seed and word count, not the
  passage** — every client generates identical text from a tiny message.
- Races use `typing.StartedAt` so the clock runs from that shared instant. Solo practice
  deliberately does the opposite and starts on the first keystroke.
- Finishing places are assigned server-side in arrival order, against one clock.
- Two ways a race could hang are handled explicitly: a player leaving during the countdown drops
  the lobby below two and abandons it, and a racer walking away mid-passage ends the race when
  they were the last one unfinished. A deadline backstops a race nobody completes.

### Typing mechanics

- Typing through errors is allowed. A wrong character advances the cursor and is kept as
  `Incorrect` so it renders red.
- **Backspace does not count as a keystroke**, and accuracy is computed over the keystroke
  stream, not the final text. Correcting a mistake fixes the passage but does not restore lost
  accuracy — the conventional typing-test behaviour.
- Where the user types over an expected space, the renderer draws the typed character instead of
  the space; otherwise the mistake would be invisible.

## Two traps specific to SSH TUIs

Both were hit during the build and are easy to reintroduce.

**Per-session renderers.** Lipgloss's package-level renderer detects the *server's* terminal, so
using it gives every connected user the server's colour profile. `ui.NewStyles` takes a
session-scoped `*lipgloss.Renderer` and there are deliberately no package-level styles.

**Do not use `wish/bubbletea.MakeRenderer`.** It probes the terminal for its background colour
(OSC 11 + DA1, 2s timeout) to pick light vs dark. When a terminal does not answer, the timeout
cancels the query while a read is still blocked on the PTY; that orphaned read then swallows
exactly one input event, so **the user's first keypress after connecting silently vanishes**, no
matter how long they wait before pressing it. `cmd/server/renderer.go` builds the renderer
directly instead. It needs `termenv.WithUnsafe()` on the non-slave path, because an `ssh.Session`
is not a TTY and colour detection otherwise fails closed to ASCII.

## Session cleanup

Cleanup runs as the **innermost** middleware, so the Bubble Tea middleware calls it as its next
handler — only after `program.Run` returns, on the session's own goroutine. That is what makes
it safe to tear down session state without racing the update loop, and it still runs after a
panic. Without it, dropped clients become players nobody can remove, in lobbies that never
close.

The session records its lobby the moment it creates or joins one, not when a screen subscribes,
so a client dropping in between does not strand an empty lobby.

## Persistence

**Live state is in memory; history is in Postgres.** Lobbies, races and the state of an
in-flight attempt still live in process memory and reset on restart — `lobby.Store` is
unchanged. What survives is accounts, finished runs and wallets, in `internal/store`.

- `internal/store` — `Repository`, the types, `Memory` (used by every test) and `Async`. Pure,
  no Bubble Tea.
- `internal/store/pg` — pgx implementation, hand-written SQL, migrations embedded and applied at
  startup under `pg_advisory_lock`. No ORM, no migration library.

`-dsn` (or `TYPESAFE_DSN`) turns it on. **Empty is a supported mode**, not a broken one: the app
runs exactly as it did before there was a database, anonymously. A DSN that is set but
unreachable at startup is a hard failure instead, so a typo does not produce a server that looks
healthy while recording nothing.

Three rules hold this together:

1. **The database is never on the typing path.** Writes go through `store.Async`, which is a
   non-blocking send onto a buffered channel — a full queue drops the run, exactly as a lobby
   broadcast drops an event, and for the same reason. Reads happen inside a `tea.Cmd`, off the
   update loop.
2. **Every use of `Context.Repo` must tolerate nil,** which is what `Context.tracking()` is for.
   An anonymous session types and races normally; it just has no history.
3. **`internal/lobby` knows nothing about persistence.** A race is recorded by each session
   writing its own result on `RaceEnded`, not by the lobby writing everyone's. It carries a
   `cosmetics.Flair` per player for the same reason it carries a name — the other sessions have
   to see it somehow — and interprets neither.

## Bytes and cosmetics

`internal/economy` decides what an attempt pays; `internal/cosmetics` is the catalogue it is
spent on. Both are pure, both are tables of constants, and both take their randomness by
argument so an award is assertable.

Three rules here too:

1. **A balance is derived, never stored.** `store.Balance` is everything earned less everything
   bought, and there is no column holding a total that could drift from it. It is the second
   definition living in two implementations — see `store.Summarize` below; the same rule applies
   and the same integration test guards it.
2. **Earnings ride in the run's own row.** `store.Run.Earned` is set by the session that records
   the attempt, so a finished race still costs the typing path exactly one queued statement and
   a run can never disagree with what it paid. Do not add a second write here.

   The cost of that is a write the session has not seen land. **A reader that must observe the
   session's own recent writes calls `store.Flush` first** — `Async` queues, so a wallet read
   issued straight after an award would otherwise derive a balance from before it, dip on screen
   and briefly refuse an affordable purchase. `Flush` is an optional interface, so it is a no-op
   for `Memory` and `pg.Repo`, which have already written by the time they return.
3. **A purchase records the price as paid.** Repricing the catalogue must not reach backwards
   into anyone's balance. `Buy` is atomic under an advisory lock on the account, because one
   person can hold several sessions and really can spend the same bytes twice.

The link-code merge has to fold wallets as well as runs: a cosmetic the target already owns is
dropped rather than moved — which refunds it, since the balance is derived — and everything that
moves arrives unequipped.

### Identity

The SSH public key fingerprint is the account. `cmd/server/main.go` computes it in the auth
callback — still accepting every key — and stashes it on the `ssh.Context`; the session resolves
it to an account, creating one on a first connection. Several keys can point at one account
through a link code, which merges the two accounts (runs, cosmetics and keys move, the emptied
one is deleted).

`store.Summarize` is the definition of what the profile figures mean, and `store.Balance` is the
definition of what a balance means. `Memory` calls each; the SQL recomputes each; the integration
tests assert the two agree. Change one and you must change both.

## Conventions

- Screens implement `ui.Screen` (like `tea.Model` but `Update` returns a `Screen`, saving every
  caller a type assertion). They are values; `Update` returns the updated copy.
- A screen builds its own successor and returns `navigate(next)`, keeping transition logic next
  to the code that knows when to make it.
- `Context` is shared by pointer, so a resize reaches every screen without the router notifying
  each one.
- Tests render with an `io.Discard` renderer, which degrades to plain ASCII and makes `View()`
  output assertable. Colour is dropped but attributes like underline are not, so assertions
  strip escapes with the `plain` helper.
