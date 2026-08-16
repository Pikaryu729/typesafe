package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Pikaryu729/typesafe/internal/cosmetics"
	"github.com/Pikaryu729/typesafe/internal/economy"
	"github.com/Pikaryu729/typesafe/internal/store"
)

// walletMsg carries a wallet, or the reason there isn't one, back into the
// update loop. Loads and writes both land here: what the screen needs to know
// afterwards is the same either way.
type walletMsg struct {
	wallet store.Wallet
	// note is what the last action did, shown until the next one. Errors from
	// a purchase are ordinary outcomes, not failures, so they go here too.
	note string
	err  error
}

// shopRow is one line of the shop: a catalogue item, or the heading above a
// group of them.
//
// Flattening the catalogue into rows at build time is what keeps the cursor
// simple — one index into one slice — while still letting the list be grouped
// by slot on screen.
type shopRow struct {
	item    cosmetics.Item
	heading string // set instead of item for a section heading
}

func (r shopRow) selectable() bool { return r.heading == "" }

// Shop lists the cosmetics bytes can be spent on, and equips the ones already
// owned.
type Shop struct {
	ctx     *Context
	rows    []shopRow
	cursor  int
	wallet  store.Wallet
	loading bool
	busy    bool // a buy or an equip is in flight
	note    string
	err     error
}

// NewShop returns the shop with the first buyable item selected.
func NewShop(ctx *Context) Shop {
	s := Shop{ctx: ctx, rows: shopRows(), loading: ctx.tracking()}
	s.cursor = s.nextSelectable(-1, 1)
	return s
}

// shopRows flattens the catalogue into headings and items, in slot order.
func shopRows() []shopRow {
	var rows []shopRow
	for _, slot := range cosmetics.Slots {
		items := cosmetics.BySlot(slot)
		if len(items) == 0 {
			continue
		}
		rows = append(rows, shopRow{heading: slot.Label()})
		for _, it := range items {
			rows = append(rows, shopRow{item: it})
		}
	}
	return rows
}

func (s Shop) Init() tea.Cmd {
	if !s.ctx.tracking() {
		return nil
	}
	return s.load()
}

// load reads the wallet.
//
// Like the profile's query it runs as a tea.Cmd, on Bubble Tea's own
// goroutine, and captures what it needs as locals rather than reaching through
// the context — so a slow database delays this screen filling in and nothing
// else.
func (s Shop) load() tea.Cmd {
	repo, userID := s.ctx.Repo, s.ctx.User.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()

		w, err := repo.Wallet(ctx, userID)
		return walletMsg{wallet: w, err: err}
	}
}

func (s Shop) buy(it cosmetics.Item) tea.Cmd {
	repo, userID := s.ctx.Repo, s.ctx.User.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()

		w, err := repo.Buy(ctx, userID, store.Owned{
			ID: it.ID, Slot: string(it.Slot), Price: it.Price,
		})
		if err != nil {
			return walletMsg{err: err}
		}
		// Buying something is the moment you want to wear it, so the purchase
		// equips it rather than making the typist press the key twice.
		w, err = repo.Equip(ctx, userID, string(it.Slot), it.ID)
		return walletMsg{wallet: w, note: "bought and equipped " + it.Name, err: err}
	}
}

func (s Shop) equip(it cosmetics.Item, wear bool) tea.Cmd {
	repo, userID := s.ctx.Repo, s.ctx.User.ID
	id, note := it.ID, "equipped "+it.Name
	if !wear {
		id, note = "", "put away "+it.Name
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()

		w, err := repo.Equip(ctx, userID, string(it.Slot), id)
		return walletMsg{wallet: w, note: note, err: err}
	}
}

func (s Shop) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case walletMsg:
		s.loading, s.busy = false, false
		if msg.err != nil {
			// A refusal is not a broken screen: keep the wallet on show and
			// say why the key did nothing.
			if note, ok := refusal(msg.err); ok {
				s.note = note
				return s, nil
			}
			s.err = msg.err
			return s, nil
		}
		s.wallet, s.note, s.err = msg.wallet, msg.note, nil
		// Context is shared by pointer, so this is what makes a newly equipped
		// theme reach every screen built afterwards.
		s.ctx.applyWallet(msg.wallet)
		return s, nil

	case tea.KeyMsg:
		return s.handleKey(msg)
	}
	return s, nil
}

func (s Shop) handleKey(msg tea.KeyMsg) (Screen, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		return s, navigate(NewMenu(s.ctx))

	case "up", "k":
		s.cursor = s.nextSelectable(s.cursor, -1)
	case "down", "j":
		s.cursor = s.nextSelectable(s.cursor, 1)
	case "home", "g":
		s.cursor = s.nextSelectable(-1, 1)
	case "end", "G":
		s.cursor = s.nextSelectable(len(s.rows), -1)

	case "r":
		if s.ctx.tracking() && !s.busy {
			s.loading, s.err, s.note = true, nil, ""
			return s, s.load()
		}

	case "enter", " ":
		return s.act()
	}
	return s, nil
}

// act buys, equips or puts away whatever is under the cursor.
//
// One key does all three because they are one intent: the typist is saying
// "this one". What that means depends only on whether they already own it and
// are already wearing it, which they can see on the row.
func (s Shop) act() (Screen, tea.Cmd) {
	if s.loading || s.busy || !s.ctx.tracking() {
		return s, nil
	}
	row := s.rows[s.cursor]
	if !row.selectable() {
		return s, nil
	}

	switch it := row.item; {
	case !s.wallet.Owns(it.ID):
		if s.wallet.Balance < it.Price {
			s.note = fmt.Sprintf("%s costs %s — you are %s short",
				it.Name, formatBalance(it.Price), formatBalance(it.Price-s.wallet.Balance))
			return s, nil
		}
		s.busy, s.note = true, ""
		return s, s.buy(it)

	case s.wallet.EquippedIn(string(it.Slot)) == it.ID:
		s.busy, s.note = true, ""
		return s, s.equip(it, false)

	default:
		s.busy, s.note = true, ""
		return s, s.equip(it, true)
	}
}

// refusal turns the store's ordinary "no" into something to say. Anything else
// is a real error and belongs on the error line instead.
func refusal(err error) (string, bool) {
	switch {
	case errors.Is(err, store.ErrInsufficientFunds):
		return "not enough " + economy.Unit + " for that yet", true
	case errors.Is(err, store.ErrAlreadyOwned):
		return "you already own that", true
	case errors.Is(err, store.ErrNotOwned):
		return "buy it before you wear it", true
	default:
		return "", false
	}
}

// nextSelectable returns the next row in direction step that can be chosen,
// skipping headings and wrapping at the ends.
func (s Shop) nextSelectable(from, step int) int {
	n := len(s.rows)
	if n == 0 {
		return 0
	}
	for range n {
		from = ((from+step)%n + n) % n
		if s.rows[from].selectable() {
			return from
		}
	}
	return 0
}

func (s Shop) View() string {
	st := s.ctx.Styles

	var b strings.Builder
	b.WriteString(s.ctx.header("shop · " + formatBalance(s.ctx.Balance)))
	b.WriteString("\n\n")

	switch {
	case !s.ctx.tracking():
		b.WriteString(st.Subtitle.Render(s.unavailableReason()))
		b.WriteString("\n\n")
		b.WriteString(s.ctx.help("esc menu"))
		return b.String()

	case s.loading:
		b.WriteString(st.Subtitle.Render("loading…"))
		b.WriteString("\n\n")
		b.WriteString(s.ctx.help("esc menu"))
		return b.String()

	case s.err != nil:
		b.WriteString(st.Error.Render("could not reach the shop"))
		b.WriteString("\n")
		b.WriteString(st.Subtitle.Render("the database is not answering; your typing is unaffected"))
		b.WriteString("\n\n")
		b.WriteString(s.ctx.help("r retry", "esc menu"))
		return b.String()
	}

	b.WriteString(s.renderRows())
	b.WriteString("\n")

	if s.note != "" {
		b.WriteString(st.Subtitle.Render(s.note))
		b.WriteString("\n\n")
	}

	b.WriteString(s.ctx.help("↑/↓ move", "enter buy/equip", "r refresh", "esc menu"))
	return b.String()
}

// unavailableReason says which of the two ways this session has no wallet,
// because the fix is different for each. It matches the profile's wording.
func (s Shop) unavailableReason() string {
	if s.ctx.Repo == nil {
		return "this server is running without a database, so there are no " + economy.Unit + " to earn"
	}
	return "your account could not be loaded, so this session is anonymous"
}

// Column widths for a shop row. The name and status are padded so prices and
// previews form columns; the description is last, so it can run as long as it
// likes without pushing anything out of line.
const (
	shopNameWidth    = 14
	shopStatusWidth  = 11
	shopPreviewWidth = 14
)

func (s Shop) renderRows() string {
	st := s.ctx.Styles

	var b strings.Builder
	for i, row := range s.rows {
		if !row.selectable() {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(st.StatLabel.Render(row.heading))
			b.WriteString("\n")
			continue
		}
		b.WriteString(s.renderItem(row.item, i == s.cursor))
		b.WriteString("\n")
	}
	return b.String()
}

func (s Shop) renderItem(it cosmetics.Item, selected bool) string {
	st := s.ctx.Styles

	// The marker carries the indent, so neither name style may add padding of
	// its own — Styles.Item does, which is why the muted one here is not it.
	marker, nameStyle := rowIndent, st.StatLabel
	if selected {
		marker, nameStyle = "> ", st.SelectedItem
	}

	var status string
	switch {
	case s.wallet.EquippedIn(string(it.Slot)) == it.ID:
		status = st.Equipped.Render("worn")
	case s.wallet.Owns(it.ID):
		status = st.StatLabel.Render("owned")
	case s.wallet.Balance >= it.Price:
		status = st.Currency.Render(formatBalance(it.Price))
	default:
		status = st.Locked.Render(formatBalance(it.Price))
	}

	return marker +
		padTo(nameStyle.Render(it.Name), shopNameWidth) + " " +
		padTo(status, shopStatusWidth) + " " +
		padTo(s.preview(it), shopPreviewWidth) + " " +
		st.Help.Render(it.Desc)
}

// preview shows the cosmetic doing its job, so a typist can tell a name colour
// from a bar glyph without buying one of each.
func (s Shop) preview(it cosmetics.Item) string {
	st := s.ctx.Styles

	switch it.Slot {
	case cosmetics.SlotColor:
		return st.Name(cosmetics.Flair{Color: it.ID}, st.Correct).Render(s.ctx.Username)
	case cosmetics.SlotBadge:
		return st.Correct.Render("[" + it.Badge + "]")
	case cosmetics.SlotBar:
		return st.StatValue.Render(strings.Repeat(it.Filled, 4) + strings.Repeat(it.Empty, 2))
	case cosmetics.SlotTheme:
		themed := s.ctx.baseStyles.Themed(it.ID)
		return themed.Correct.Render("typed") + " " + themed.Untyped.Render("untyped")
	default:
		return ""
	}
}
