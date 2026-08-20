package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Pikaryu729/typesafe/internal/store"
)

// Link attaches another machine's SSH key to this account.
//
// Two halves of one exchange: on the machine you are already known on, press
// c for a code; on the new machine, type that code in. The second machine's
// account — created the moment it first connected — is folded into the first,
// so no history is lost either way round.
type Link struct {
	ctx *Context

	issued   store.LinkCode // the code this session handed out, if any
	entering bool
	entry    string

	linkedTo string // display name of the account just joined
	err      string
}

type linkCodeMsg struct {
	code store.LinkCode
	err  error
}

type linkedMsg struct {
	user   store.User
	wallet store.Wallet
	loaded bool
	err    error
}

// NewLink returns the device-linking screen.
func NewLink(ctx *Context) Link { return Link{ctx: ctx} }

func (l Link) Init() tea.Cmd { return nil }

func (l Link) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case linkCodeMsg:
		if msg.err != nil {
			l.err = "could not create a code right now"
			return l, nil
		}
		l.issued, l.err = msg.code, ""
		return l, nil

	case linkedMsg:
		return l.applyLinked(msg), nil

	case tea.KeyMsg:
		return l.handleKey(msg)
	}
	return l, nil
}

// applyLinked folds the result of a redeem back into the session.
func (l Link) applyLinked(msg linkedMsg) Link {
	switch {
	case errors.Is(msg.err, store.ErrUnknownCode):
		l.err = "no such code — check it and try again"
		return l
	case errors.Is(msg.err, store.ErrExpiredCode):
		l.err = "that code has expired; make a new one"
		return l
	case msg.err != nil && msg.user.ID == "":
		l.err = "could not link this device right now"
		return l
	}

	// The session is now this account. Updating the shared context is what
	// makes the next run record against it — Context is held by pointer, so
	// every screen built afterwards sees the change.
	l.ctx.User = msg.user
	l.ctx.Username = msg.user.DisplayName
	l.linkedTo = msg.user.DisplayName
	if !msg.loaded {
		l.ctx.applyWallet(store.Wallet{})
		l.err = "linked, but could not load your wallet"
		return l
	}
	l.ctx.applyWallet(msg.wallet)
	l.err = ""
	return l
}

func (l Link) handleKey(msg tea.KeyMsg) (Screen, tea.Cmd) {
	if l.entering {
		return l.handleEntryKey(msg)
	}

	switch msg.String() {
	case "esc", "q":
		return l, navigate(NewMenu(l.ctx))
	case "c":
		if l.ctx.tracking() {
			return l, l.createCode()
		}
	case "enter", "/":
		if l.ctx.tracking() {
			l.entering, l.entry, l.err = true, "", ""
		}
	}
	return l, nil
}

// handleEntryKey drives the code prompt, mirroring the lobby join prompt so
// the two feel the same.
func (l Link) handleEntryKey(msg tea.KeyMsg) (Screen, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		l.entering, l.entry = false, ""
	case tea.KeyEnter:
		l.entering = false
		return l, l.redeem(l.entry)
	case tea.KeyBackspace:
		if l.entry != "" {
			l.entry = l.entry[:len(l.entry)-1]
		}
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			if r < utf8.RuneSelf && len(l.entry) < store.LinkCodeLength {
				l.entry += strings.ToUpper(string(r))
			}
		}
	}
	return l, nil
}

func (l Link) createCode() tea.Cmd {
	repo, userID := l.ctx.Repo, l.ctx.User.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()

		code, err := repo.CreateLinkCode(ctx, userID)
		return linkCodeMsg{code: code, err: err}
	}
}

func (l Link) redeem(code string) tea.Cmd {
	repo, fingerprint := l.ctx.Repo, l.ctx.Fingerprint
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()

		if err := store.Flush(ctx, repo); err != nil {
			return linkedMsg{err: err}
		}

		user, err := repo.RedeemLinkCode(ctx, code, fingerprint)
		if err != nil {
			return linkedMsg{err: err}
		}
		wallet, err := repo.Wallet(ctx, user.ID)
		return linkedMsg{user: user, wallet: wallet, loaded: err == nil, err: err}
	}
}

func (l Link) View() string {
	s := l.ctx.Styles

	var b strings.Builder
	b.WriteString(l.ctx.header("link a device"))
	b.WriteString("\n\n")

	if !l.ctx.tracking() {
		b.WriteString(s.Subtitle.Render("accounts are unavailable on this server"))
		b.WriteString("\n\n")
		b.WriteString(l.ctx.help("esc menu"))
		return b.String()
	}

	b.WriteString(s.Subtitle.Render("your SSH key is your account, so a second machine starts out as"))
	b.WriteString("\n")
	b.WriteString(s.Subtitle.Render("someone else. A code joins them together."))
	b.WriteString("\n\n")

	switch {
	case l.entering:
		b.WriteString(s.Subtitle.Render("code: "))
		b.WriteString(s.StatValue.Render(l.entry + strings.Repeat("_", store.LinkCodeLength-len(l.entry))))
		b.WriteString("\n")

	case l.linkedTo != "":
		b.WriteString(s.Good.Render("linked — you are now " + l.linkedTo))
		b.WriteString("\n")
		b.WriteString(s.Subtitle.Render("this machine's earlier runs came across with it"))
		b.WriteString("\n")

	case l.issued.Code != "":
		b.WriteString(s.Subtitle.Render("type this on the other machine:"))
		b.WriteString("\n\n")
		b.WriteString(s.Title.Render(l.issued.Code))
		b.WriteString("\n")
		b.WriteString(s.Help.Render(fmt.Sprintf("expires in %s · anyone with it can use your account",
			formatExpiry(time.Until(l.issued.ExpiresAt)))))
		b.WriteString("\n")
	}

	if l.err != "" {
		b.WriteString(s.Error.Render(l.err))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if l.entering {
		b.WriteString(l.ctx.help("enter link", "esc cancel"))
	} else {
		b.WriteString(l.ctx.help("c get a code", "enter use a code", "esc menu"))
	}
	return b.String()
}

// formatExpiry renders the life left in a code, rounded down to the minute.
func formatExpiry(d time.Duration) string {
	if d < time.Minute {
		return "under a minute"
	}
	return fmt.Sprintf("%d minutes", int(d.Minutes()))
}
