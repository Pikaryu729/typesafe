// Package cosmetics is the catalogue of things bytes can be spent on.
//
// Everything here is decoration: nothing a typist can buy changes a passage, a
// score, or who wins a race. What it changes is what other people see when
// they race you, which is the whole point of earning it.
//
// The catalogue is a flat table rather than a registry, in the style of the
// main menu's items. Adding a cosmetic is one entry and a price.
//
// Like lobby, typing, words and store, this package has no Bubble Tea or
// terminal dependency. Colours are ANSI256 indices as opaque strings, which is
// what lets lobby carry a player's choices without knowing what they mean.
package cosmetics

import "slices"

// Slot is a category of cosmetic. A typist has at most one of each equipped,
// so buying a second name colour replaces the first rather than stacking.
type Slot string

const (
	// SlotColor is the colour your name renders in, everywhere it appears.
	SlotColor Slot = "color"
	// SlotBadge is a short title shown beside your name.
	SlotBadge Slot = "badge"
	// SlotBar is the pair of glyphs your race progress bar is drawn with.
	SlotBar Slot = "bar"
	// SlotTheme recolours your own typing view. It is the one slot nobody
	// else can see.
	SlotTheme Slot = "theme"
)

// Slots is every slot, in the order the shop lists them: cheapest and most
// visible first.
var Slots = []Slot{SlotBar, SlotColor, SlotBadge, SlotTheme}

// Label names a slot for the shop's section headings.
func (s Slot) Label() string {
	switch s {
	case SlotColor:
		return "name colours"
	case SlotBadge:
		return "titles"
	case SlotBar:
		return "race bars"
	case SlotTheme:
		return "passage themes"
	default:
		return string(s)
	}
}

// Theme is a passage's four colours. An empty Theme means the app's own, which
// is why no purchase is needed to have one.
type Theme struct {
	Untyped   string
	Correct   string
	Incorrect string
	CursorFg  string
	CursorBg  string
}

// Item is one thing in the catalogue.
//
// Only the fields belonging to the item's slot carry anything; the rest are
// zero. A tagged union would be tidier in the abstract and worse to read here,
// where the whole catalogue is one table you want to scan down.
type Item struct {
	ID    string
	Name  string
	Desc  string
	Slot  Slot
	Price int

	Color         string // SlotColor: an ANSI256 index
	Badge         string // SlotBadge: the word shown in brackets
	Filled, Empty string // SlotBar: the full and empty bar cells
	Theme         Theme  // SlotTheme
}

// Catalog is everything for sale, in display order. It is built once and
// treated as read-only afterwards, so the sessions reading it concurrently
// need no synchronisation.
//
// Prices are set so the cheapest slot is four or five races away and the rare
// colours are a couple of weeks of casual play. Nothing here is ever removed:
// a purchase records what it cost, so repricing an item cannot reach back into
// anyone's balance.
var Catalog = []Item{
	// Race bars: the cheapest thing to buy and the most visible during a
	// race, so this is what a typist's first few hundred bytes go on.
	{ID: "bar-blocks", Name: "Blocks", Desc: "solid slabs", Slot: SlotBar, Price: 120, Filled: "▰", Empty: "▱"},
	{ID: "bar-dots", Name: "Dots", Desc: "round and light", Slot: SlotBar, Price: 120, Filled: "●", Empty: "○"},
	{ID: "bar-wedge", Name: "Wedge", Desc: "arrowheads", Slot: SlotBar, Price: 120, Filled: "▶", Empty: "▷"},
	{ID: "bar-shade", Name: "Shade", Desc: "heavy and soft", Slot: SlotBar, Price: 120, Filled: "▓", Empty: "▒"},

	// Name colours: seen everywhere a name is, which is the lobby browser,
	// the waiting room, the race and the standings.
	{ID: "color-rose", Name: "Rose", Desc: "the house pink", Slot: SlotColor, Price: 180, Color: "212"},
	{ID: "color-cyan", Name: "Cyan", Desc: "cold and clear", Slot: SlotColor, Price: 180, Color: "80"},
	{ID: "color-amber", Name: "Amber", Desc: "warm orange", Slot: SlotColor, Price: 180, Color: "214"},
	{ID: "color-mint", Name: "Mint", Desc: "pale green", Slot: SlotColor, Price: 180, Color: "78"},
	{ID: "color-violet", Name: "Violet", Desc: "deep purple · rare", Slot: SlotColor, Price: 500, Color: "141"},
	{ID: "color-gold", Name: "Gold", Desc: "bright yellow · rare", Slot: SlotColor, Price: 500, Color: "220"},

	// Titles: the one cosmetic that says something about how you type.
	{ID: "badge-swift", Name: "Swift", Desc: "for the quick", Slot: SlotBadge, Price: 250, Badge: "swift"},
	{ID: "badge-precise", Name: "Precise", Desc: "for the accurate", Slot: SlotBadge, Price: 250, Badge: "precise"},
	{ID: "badge-relentless", Name: "Relentless", Desc: "for the regular", Slot: SlotBadge, Price: 250, Badge: "relentless"},
	{ID: "badge-nocturnal", Name: "Nocturnal", Desc: "for the late", Slot: SlotBadge, Price: 250, Badge: "nocturnal"},

	// Passage themes: the most expensive, because they change the screen you
	// spend the most time looking at — even though only you see it.
	{
		ID: "theme-ember", Name: "Ember", Desc: "warm coals", Slot: SlotTheme, Price: 400,
		Theme: Theme{Untyped: "238", Correct: "223", Incorrect: "196", CursorFg: "236", CursorBg: "208"},
	},
	{
		ID: "theme-forest", Name: "Forest", Desc: "green on dark", Slot: SlotTheme, Price: 400,
		Theme: Theme{Untyped: "238", Correct: "151", Incorrect: "203", CursorFg: "235", CursorBg: "72"},
	},
	{
		ID: "theme-mono", Name: "Mono", Desc: "no colour at all", Slot: SlotTheme, Price: 400,
		Theme: Theme{Untyped: "240", Correct: "255", Incorrect: "244", CursorFg: "232", CursorBg: "250"},
	},
}

// Find returns the catalogue item with this ID.
//
// The empty ID is every slot's default — the app's own colours and glyphs —
// and is deliberately not in the catalogue: it is always owned, always free,
// and needs no entry to be equipped.
func Find(id string) (Item, bool) {
	if id == "" {
		return Item{}, false
	}
	for _, it := range Catalog {
		if it.ID == id {
			return it, true
		}
	}
	return Item{}, false
}

// BySlot returns the catalogue's items for one slot, in display order.
func BySlot(s Slot) []Item {
	var out []Item
	for _, it := range Catalog {
		if it.Slot == s {
			out = append(out, it)
		}
	}
	return out
}

// Equipped is what one typist is wearing, slot to cosmetic ID. A slot with no
// entry, or an empty one, is wearing the default.
type Equipped map[Slot]string

// Clone returns a copy, so a session's choices cannot be mutated through a map
// it handed to someone else.
func (e Equipped) Clone() Equipped {
	if e == nil {
		return nil
	}
	out := make(Equipped, len(e))
	for k, v := range e {
		out[k] = v
	}
	return out
}

// Flair is the part of a typist's cosmetics other players can see.
//
// It is a comparable value of nothing but IDs so the lobby can carry one per
// player, copy it into every snapshot, and hand it across goroutines without
// aliasing anything — exactly what it already does with a name. The lobby
// interprets neither.
type Flair struct {
	Color string
	Badge string
	Bar   string
}

// Flair returns the visible subset of these choices. The passage theme is
// left out on purpose: nobody else is looking at your passage.
func (e Equipped) Flair() Flair {
	return Flair{
		Color: e[SlotColor],
		Badge: e[SlotBadge],
		Bar:   e[SlotBar],
	}
}

// NameColor returns the ANSI256 index this flair's name should render in, and
// whether it has one. An unknown ID — a cosmetic retired since it was bought —
// falls back to the default rather than to a blank name.
func (f Flair) NameColor() (string, bool) {
	it, ok := Find(f.Color)
	if !ok || it.Slot != SlotColor {
		return "", false
	}
	return it.Color, true
}

// BadgeText returns the word shown beside a name, or "" for none.
func (f Flair) BadgeText() string {
	it, ok := Find(f.Badge)
	if !ok || it.Slot != SlotBadge {
		return ""
	}
	return it.Badge
}

// DefaultFilled and DefaultEmpty are the bar cells used by anyone who has not
// bought others. They are the ones the race screen has always drawn.
const (
	DefaultFilled = "█"
	DefaultEmpty  = "░"
)

// BarGlyphs returns the cells this flair's progress bar is drawn with, falling
// back to the default pair.
func (f Flair) BarGlyphs() (filled, empty string) {
	it, ok := Find(f.Bar)
	if !ok || it.Slot != SlotBar {
		return DefaultFilled, DefaultEmpty
	}
	return it.Filled, it.Empty
}

// ThemeOf returns the passage colours for an equipped theme ID. The zero Theme
// means "the app's own", which is what an unowned or unknown ID gets.
func ThemeOf(id string) Theme {
	it, ok := Find(id)
	if !ok || it.Slot != SlotTheme {
		return Theme{}
	}
	return it.Theme
}

// IDs returns every catalogue ID, for tests and for validating stored rows
// against the catalogue they were bought from.
func IDs() []string {
	out := make([]string, 0, len(Catalog))
	for _, it := range Catalog {
		out = append(out, it.ID)
	}
	slices.Sort(out)
	return out
}
