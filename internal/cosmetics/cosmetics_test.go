package cosmetics_test

import (
	"testing"

	"github.com/Pikaryu729/typesafe/internal/cosmetics"
)

func TestCatalogIsWellFormed(t *testing.T) {
	seen := make(map[string]bool)
	for _, it := range cosmetics.Catalog {
		if it.ID == "" {
			t.Errorf("%q has no ID; the empty ID is reserved for the default", it.Name)
		}
		if seen[it.ID] {
			t.Errorf("duplicate catalogue ID %q", it.ID)
		}
		seen[it.ID] = true

		if it.Name == "" {
			t.Errorf("%s has no name", it.ID)
		}
		if it.Price <= 0 {
			t.Errorf("%s costs %d; everything in the catalogue is bought", it.ID, it.Price)
		}

		// Each item has to carry the payload its slot renders, or equipping
		// it would silently do nothing.
		switch it.Slot {
		case cosmetics.SlotColor:
			if it.Color == "" {
				t.Errorf("%s is a colour with no colour", it.ID)
			}
		case cosmetics.SlotBadge:
			if it.Badge == "" {
				t.Errorf("%s is a badge with no text", it.ID)
			}
		case cosmetics.SlotBar:
			if it.Filled == "" || it.Empty == "" {
				t.Errorf("%s is a bar missing a glyph: %q/%q", it.ID, it.Filled, it.Empty)
			}
		case cosmetics.SlotTheme:
			if it.Theme == (cosmetics.Theme{}) {
				t.Errorf("%s is a theme with no colours", it.ID)
			}
		default:
			t.Errorf("%s has unknown slot %q", it.ID, it.Slot)
		}
	}
}

func TestEverySlotHasItemsAndIsListed(t *testing.T) {
	for _, s := range cosmetics.Slots {
		if len(cosmetics.BySlot(s)) == 0 {
			t.Errorf("slot %q is listed in the shop but sells nothing", s)
		}
		if s.Label() == string(s) {
			t.Errorf("slot %q has no human-readable label", s)
		}
	}

	listed := make(map[cosmetics.Slot]bool)
	for _, s := range cosmetics.Slots {
		listed[s] = true
	}
	for _, it := range cosmetics.Catalog {
		if !listed[it.Slot] {
			t.Errorf("%s is in slot %q, which the shop never shows", it.ID, it.Slot)
		}
	}
}

func TestFindRoundTrips(t *testing.T) {
	for _, id := range cosmetics.IDs() {
		it, ok := cosmetics.Find(id)
		if !ok {
			t.Errorf("Find(%q) found nothing", id)
			continue
		}
		if it.ID != id {
			t.Errorf("Find(%q) returned %q", id, it.ID)
		}
	}

	if _, ok := cosmetics.Find(""); ok {
		t.Error("the empty ID is the default and must not be in the catalogue")
	}
	if _, ok := cosmetics.Find("no-such-cosmetic"); ok {
		t.Error("Find invented a cosmetic")
	}
}

func TestFlairCarriesOnlyWhatOthersSee(t *testing.T) {
	e := cosmetics.Equipped{
		cosmetics.SlotColor: "color-gold",
		cosmetics.SlotBadge: "badge-swift",
		cosmetics.SlotBar:   "bar-dots",
		cosmetics.SlotTheme: "theme-ember",
	}

	f := e.Flair()
	want := cosmetics.Flair{Color: "color-gold", Badge: "badge-swift", Bar: "bar-dots"}
	if f != want {
		t.Errorf("Flair() = %+v, want %+v", f, want)
	}

	// The theme is the one slot nobody else can see, so it must not travel
	// through the lobby with the rest.
	if f.Color == "theme-ember" || f.Badge == "theme-ember" || f.Bar == "theme-ember" {
		t.Error("the passage theme leaked into the flair other players see")
	}
}

func TestFlairResolvesToRenderableValues(t *testing.T) {
	f := cosmetics.Equipped{
		cosmetics.SlotColor: "color-gold",
		cosmetics.SlotBadge: "badge-swift",
		cosmetics.SlotBar:   "bar-dots",
	}.Flair()

	if c, ok := f.NameColor(); !ok || c != "220" {
		t.Errorf("NameColor() = %q, %v; want 220, true", c, ok)
	}
	if got := f.BadgeText(); got != "swift" {
		t.Errorf("BadgeText() = %q, want swift", got)
	}
	if filled, empty := f.BarGlyphs(); filled != "●" || empty != "○" {
		t.Errorf("BarGlyphs() = %q/%q, want ●/○", filled, empty)
	}
}

func TestUnsetAndUnknownFlairFallsBackToTheDefault(t *testing.T) {
	for name, f := range map[string]cosmetics.Flair{
		"unset":   {},
		"unknown": {Color: "color-retired", Badge: "badge-retired", Bar: "bar-retired"},
		// A cosmetic pointed at the wrong slot must not be honoured either.
		"crossed": {Color: "bar-dots", Badge: "color-gold", Bar: "badge-swift"},
	} {
		if _, ok := f.NameColor(); ok {
			t.Errorf("%s flair claimed a name colour", name)
		}
		if got := f.BadgeText(); got != "" {
			t.Errorf("%s flair claimed the badge %q", name, got)
		}
		filled, empty := f.BarGlyphs()
		if filled != cosmetics.DefaultFilled || empty != cosmetics.DefaultEmpty {
			t.Errorf("%s flair drew %q/%q instead of the default", name, filled, empty)
		}
	}
}

func TestThemeOf(t *testing.T) {
	if got := cosmetics.ThemeOf("theme-mono"); got == (cosmetics.Theme{}) {
		t.Error("theme-mono resolved to no theme")
	}
	for _, id := range []string{"", "theme-retired", "color-gold"} {
		if got := cosmetics.ThemeOf(id); got != (cosmetics.Theme{}) {
			t.Errorf("ThemeOf(%q) = %+v, want the app's own", id, got)
		}
	}
}

func TestEquippedCloneDoesNotAlias(t *testing.T) {
	orig := cosmetics.Equipped{cosmetics.SlotBar: "bar-dots"}
	clone := orig.Clone()
	clone[cosmetics.SlotBar] = "bar-shade"

	if orig[cosmetics.SlotBar] != "bar-dots" {
		t.Error("writing to the clone reached the original")
	}
	if cosmetics.Equipped(nil).Clone() != nil {
		t.Error("cloning nil should stay nil")
	}
}
