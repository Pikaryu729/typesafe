# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

This repository is currently empty — no code has been written yet. This file describes the
intended architecture and conventions for the project so that implementation stays consistent
as it's built out. Update this file as real structure, commands, and decisions land; don't let
it drift into aspirational documentation once code exists.

## What this is

`typesafe` is an SSH-accessible typing-practice and typing-race application. Users connect over
SSH and get a terminal UI (TUI) where they can:
- Practice typing against generated/curated text to improve speed and accuracy.
- Create or join lobbies to race other connected users head-to-head on the same passage.

It's a single Go server process: one SSH server that authenticates/accepts connections and
attaches a Bubble Tea program to each session.

## Core stack

- **charm/wish** — SSH server middleware. Each incoming SSH session is wrapped and handed a
  `tea.Program` via the `bubbletea` middleware (`github.com/charmbracelet/wish/bubbletea`).
- **charm/bubbletea** — the Elm-architecture TUI framework driving each session's UI
  (`Model` / `Init` / `Update` / `View`).
- **charm/bubbles** (likely) — reusable Bubble Tea components (text input, viewport, spinner)
  for building practice/race screens.
- **charm/lipgloss** (likely) — terminal styling for the TUI.

Module path: `github.com/Pikary729/typesafe`

## Architecture

### Per-session isolation, shared server state

Each SSH connection gets its own `tea.Model` and its own goroutine (this is how `wish` +
`bubbletea` middleware works). Anything that needs to be shared *across* sessions — active
lobbies, connected players, race state — must live in a server-level store that session models
read from and write to concurrently. This is the central architectural concern of the project:

- **Session-local state**: current screen/menu, the player's own input buffer, WPM/accuracy
  calculation for their attempt, cursor position.
- **Server-shared state**: the lobby registry (id → lobby), which players are in which lobby,
  race countdowns, and each racer's live progress within a lobby (so opponents' progress can be
  rendered in real time).

Shared state must be guarded with a mutex (or channel-based ownership) since it's accessed
concurrently from each session's goroutine. A common pattern: a `Lobby` owns a broadcast
mechanism (e.g. a `chan` per subscriber, or a pub/sub map) so that when one racer's progress
updates, other sessions' Bubble Tea programs receive a `tea.Msg` (via `Program.Send`) and
re-render — Bubble Tea models can't poll shared state directly from `View()`, updates must be
pushed in as messages.

### Expected package shape

There's no code yet, but the natural split for this kind of app is:

- `cmd/server` — process entrypoint: sets up the `wish` SSH server, host key, middleware chain,
  and per-session `tea.Program` construction.
- Server-shared state (lobby registry, matchmaking, broadcast) — this is the part that most
  needs care around concurrency correctness; keep it separate from UI/rendering code so it can
  be tested without a terminal.
- TUI screens as Bubble Tea models — likely one model per screen (main menu, solo practice,
  lobby browser/create, in-race view, results) composed via a parent "router" model that swaps
  the active sub-model, which is the standard way to handle multi-screen flows in Bubble Tea.
- Typing content (word lists / passages) — a source for practice/race text, whether static data
  or generated.

### Race/typing mechanics to keep in mind

- WPM and accuracy must be computed from keystroke-level input (correct/incorrect chars, not
  just final string diff) if per-character feedback (e.g. red/green highlighting as you type) is
  wanted — decide this early since it affects the input-handling model.
- Race progress shared between racers should be a cheap, frequently-updated value (e.g. percent
  complete or characters typed), not the full text, to keep broadcast messages small.
- A countdown/ready-check phase before a race starts needs synchronized start across all
  sessions in a lobby — this is server-shared state, not something any one session can decide
  alone.

## Persistence

v1 is in-memory only: lobbies, active races, and user stats live in server process memory and
reset on restart. No database. Keep server-shared state behind a narrow interface/store so that
persistent storage (e.g. SQLite for historical stats) can be introduced later without changing
how session models interact with it.

## Commands

No `go.mod` exists yet. Once initialized (`go mod init github.com/Pikary729/typesafe`), the
standard Go commands apply:

- Build: `go build ./...`
- Run server: `go run ./cmd/server`
- Test all: `go test ./...`
- Test a single package: `go test ./internal/<package>`
- Test a single test: `go test ./internal/<package> -run TestName`
- Vet: `go vet ./...`
- Format: `gofmt -l .` (list unformatted files) / `gofmt -w .` (fix)

Update this section with real commands (and any `Makefile`/`justfile` targets) once the project
is scaffolded.
