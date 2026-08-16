package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/Pikaryu729/typesafe/internal/cosmetics"
	"github.com/Pikaryu729/typesafe/internal/store"
)

// richContext returns a tracked session holding n bytes, with the wallet
// already loaded into the session the way login does it.
func richContext(t *testing.T, n int) (*Context, *store.Memory) {
	t.Helper()

	ctx, repo := trackedContext(t)
	if err := repo.RecordRun(context.Background(), store.Run{
		UserID: ctx.User.ID, Mode: store.ModePractice, WPM: 60, Accuracy: 0.95, Earned: n,
	}); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}

	w, err := repo.Wallet(context.Background(), ctx.User.ID)
	if err != nil {
		t.Fatalf("Wallet: %v", err)
	}
	ctx.applyWallet(w)
	return ctx, repo
}

// openShop builds the shop and runs its load, which is how it reaches the
// state a typist actually sees.
func openShop(t *testing.T, ctx *Context) Shop {
	t.Helper()

	s := Screen(NewShop(ctx))
	if cmd := s.Init(); cmd != nil {
		s, _ = s.Update(cmd())
	}
	return s.(Shop)
}

// press sends one key and settles whatever command it produced, so a write
// that goes through a tea.Cmd has landed by the time the test looks.
func press(t *testing.T, s Screen, k string) Screen {
	t.Helper()

	next, cmd := s.Update(key(k))
	if cmd == nil {
		return next
	}
	msg := cmd()
	if _, ok := msg.(navigateMsg); ok {
		return next
	}
	next, _ = next.Update(msg)
	return next
}

// selectItem moves the cursor onto a catalogue item by ID.
func selectItem(t *testing.T, s Shop, id string) Shop {
	t.Helper()

	for i, row := range s.rows {
		if row.selectable() && row.item.ID == id {
			s.cursor = i
			return s
		}
	}
	t.Fatalf("%q is not in the shop", id)
	return s
}

func TestShopListsEveryCosmeticWithItsPrice(t *testing.T) {
	ctx, _ := richContext(t, 300)
	view := plain(openShop(t, ctx).View())

	for _, it := range cosmetics.Catalog {
		if !strings.Contains(view, it.Name) {
			t.Errorf("the shop is missing %q:\n%s", it.Name, view)
		}
	}
	for _, slot := range cosmetics.Slots {
		if !strings.Contains(view, slot.Label()) {
			t.Errorf("the shop is missing the %q section:\n%s", slot.Label(), view)
		}
	}
	if !strings.Contains(view, "300 bytes") {
		t.Errorf("the shop does not show the balance:\n%s", view)
	}
	if !strings.Contains(view, "120 bytes") {
		t.Errorf("the shop does not show a price:\n%s", view)
	}
}

func TestShopBuysAndWearsAnItem(t *testing.T) {
	ctx, repo := richContext(t, 300)

	s := selectItem(t, openShop(t, ctx), "bar-dots")
	after := press(t, s, "enter").(Shop)

	w, err := repo.Wallet(context.Background(), ctx.User.ID)
	if err != nil {
		t.Fatalf("Wallet: %v", err)
	}
	if !w.Owns("bar-dots") {
		t.Fatal("the item was not bought")
	}
	if w.Balance != 180 {
		t.Errorf("balance = %d, want 180", w.Balance)
	}
	// Buying is the moment you want to wear it, so it should already be on.
	if w.EquippedIn("bar") != "bar-dots" {
		t.Errorf("the bar slot holds %q, want bar-dots", w.EquippedIn("bar"))
	}

	// The session has to follow, or the next lobby shows the old cosmetics.
	if ctx.Balance != 180 {
		t.Errorf("the session balance is %d, want 180", ctx.Balance)
	}
	if got := ctx.Equipped[cosmetics.SlotBar]; got != "bar-dots" {
		t.Errorf("the session wears %q, want bar-dots", got)
	}
	if got := ctx.flair().Bar; got != "bar-dots" {
		t.Errorf("the flair other players see is %q, want bar-dots", got)
	}

	if view := plain(after.View()); !strings.Contains(view, "worn") {
		t.Errorf("the shop does not mark the item as worn:\n%s", view)
	}
}

func TestShopRefusesWhatYouCannotAfford(t *testing.T) {
	ctx, repo := richContext(t, 50)

	s := selectItem(t, openShop(t, ctx), "color-gold")
	after := press(t, s, "enter").(Shop)

	w, _ := repo.Wallet(context.Background(), ctx.User.ID)
	if w.Owns("color-gold") {
		t.Fatal("an unaffordable item was bought anyway")
	}
	if w.Balance != 50 {
		t.Errorf("balance = %d, want 50; a refused purchase charged", w.Balance)
	}

	view := plain(after.View())
	if !strings.Contains(view, "short") {
		t.Errorf("the shop does not say why nothing happened:\n%s", view)
	}
	if strings.Contains(view, "could not reach the shop") {
		t.Errorf("a refusal was shown as a broken screen:\n%s", view)
	}
}

func TestShopTogglesAnOwnedItemOnAndOff(t *testing.T) {
	ctx, repo := richContext(t, 300)

	s := selectItem(t, openShop(t, ctx), "bar-dots")
	s = press(t, s, "enter").(Shop) // buy, which also wears it
	s = press(t, s, "enter").(Shop) // put it away

	w, _ := repo.Wallet(context.Background(), ctx.User.ID)
	if got := w.EquippedIn("bar"); got != "" {
		t.Errorf("the bar slot still holds %q", got)
	}
	if !w.Owns("bar-dots") {
		t.Error("putting a cosmetic away sold it")
	}

	s = press(t, s, "enter").(Shop) // wear it again
	w, _ = repo.Wallet(context.Background(), ctx.User.ID)
	if got := w.EquippedIn("bar"); got != "bar-dots" {
		t.Errorf("the bar slot holds %q, want bar-dots", got)
	}
	if w.Balance != 180 {
		t.Errorf("balance = %d, want 180; equipping charged again", w.Balance)
	}
}

func TestShopEquippingAThemeRecoloursThePassage(t *testing.T) {
	// Enough for both themes: the second half of this test buys one too.
	ctx, _ := richContext(t, 1000)
	before := ctx.Styles.Correct

	s := selectItem(t, openShop(t, ctx), "theme-ember")
	press(t, s, "enter")

	if ctx.Styles.Correct.GetForeground() == before.GetForeground() {
		t.Error("equipping a theme did not change the passage colours")
	}

	// Themes must not compound: swapping to another starts from the app's own
	// colours rather than the one just applied.
	s = selectItem(t, openShop(t, ctx), "theme-mono")
	press(t, s, "enter")

	want := ctx.baseStyles.Themed("theme-mono").Correct.GetForeground()
	if ctx.Styles.Correct.GetForeground() != want {
		t.Error("swapping themes did not start from the base styles")
	}
}

func TestShopCursorSkipsHeadings(t *testing.T) {
	ctx, _ := richContext(t, 300)
	s := openShop(t, ctx)

	if !s.rows[s.cursor].selectable() {
		t.Fatal("the shop opens on a heading")
	}
	for range len(s.rows) + 2 {
		s = press(t, s, "down").(Shop)
		if !s.rows[s.cursor].selectable() {
			t.Fatalf("the cursor landed on the heading %q", s.rows[s.cursor].heading)
		}
	}
	for range len(s.rows) + 2 {
		s = press(t, s, "up").(Shop)
		if !s.rows[s.cursor].selectable() {
			t.Fatalf("the cursor landed on the heading %q", s.rows[s.cursor].heading)
		}
	}
}

func TestShopWithoutAnAccountExplainsItself(t *testing.T) {
	view := plain(openShop(t, newTestContext()).View())

	if !strings.Contains(view, "without a database") {
		t.Errorf("the shop does not say why it is empty:\n%s", view)
	}
	if strings.Contains(view, "Blocks") {
		t.Errorf("an anonymous session was offered things it cannot buy:\n%s", view)
	}
}

func TestShopSurvivesADatabaseThatIsDown(t *testing.T) {
	ctx, _ := trackedContext(t)
	ctx.Repo = failingRepo{}

	view := plain(openShop(t, ctx).View())

	if !strings.Contains(view, "could not reach the shop") {
		t.Errorf("a broken database is not reported:\n%s", view)
	}
	if !strings.Contains(view, "your typing is unaffected") {
		t.Errorf("the reassurance is missing:\n%s", view)
	}
}

func TestShopIsOnTheMenuAndNeedsAnAccount(t *testing.T) {
	var shop menuItem
	for _, it := range menuItems {
		if it.title == "Shop" {
			shop = it
		}
	}
	if shop.title == "" {
		t.Fatal("the Shop is not on the menu")
	}
	if !shop.needsAccount {
		t.Error("the Shop is offered to sessions with no wallet")
	}

	// Quit has to stay last: the menu tests walk to the end and press enter.
	if got := menuItems[len(menuItems)-1].title; got != "Quit" {
		t.Errorf("the last menu item is %q, want Quit", got)
	}
}
