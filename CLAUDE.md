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
internal/lobby/      server-shared state: registry, lobbies, pub/sub (pure, concurrent)
internal/ui/         Bubble Tea models: router plus one per screen
```

The three packages below `ui/` have no Bubble Tea or terminal dependency and are tested without
a PTY. Keep it that way — it is what makes the concurrency and scoring logic testable.

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

v1 is in-memory only: lobbies, races and stats live in process memory and reset on restart. No
database. Shared state sits behind `lobby.Store`, so persistence (e.g. SQLite for historical
stats) can be added without changing how screens interact with it.

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
