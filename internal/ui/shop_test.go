package ui

import (
	"context"
	"strings"
	"testing"
	"time"

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

func TestShopStaysOpenWhileAPurchaseIsInFlight(t *testing.T) {
	ctx, _ := richContext(t, 300)
	shop := selectItem(t, openShop(t, ctx), "bar-dots")

	inFlight, cmd := shop.Update(key("enter"))
	if cmd == nil {
		t.Fatal("purchase produced no command")
	}
	stillShop, nav := inFlight.Update(key("esc"))
	if nav != nil {
		t.Fatalf("esc navigated away while the purchase was in flight: %T", nav())
	}
	shopAfterEscape, ok := stillShop.(Shop)
	if !ok {
		t.Fatalf("esc changed screen to %T", stillShop)
	}

	completed, _ := shopAfterEscape.Update(cmd())
	if !completed.(Shop).wallet.Owns("bar-dots") {
		t.Fatal("the in-flight purchase result was lost")
	}
}

func TestShopRefreshesWalletAfterAnotherSessionOwnsAnItem(t *testing.T) {
	ctx, repo := richContext(t, 300)
	other := newTestContext()
	other.Repo, other.User = repo, ctx.User
	wallet, err := repo.Wallet(context.Background(), ctx.User.ID)
	if err != nil {
		t.Fatalf("Wallet: %v", err)
	}
	other.applyWallet(wallet)

	stale := selectItem(t, openShop(t, ctx), "bar-dots")
	otherShop := selectItem(t, openShop(t, other), "bar-dots")
	_ = press(t, otherShop, "enter")

	pending, cmd := stale.Update(key("enter"))
	if cmd == nil {
		t.Fatal("refused purchase produced no command")
	}
	refused, refresh := pending.Update(cmd())
	if refresh == nil {
		t.Fatal("a purchase refusal did not refresh the wallet")
	}
	loaded, _ := refused.Update(refresh())
	after := loaded.(Shop)
	if !after.wallet.Owns("bar-dots") {
		t.Fatal("refreshed wallet omitted the cosmetic bought by another session")
	}
	if ctx.Balance != 180 {
		t.Errorf("refreshed session balance = %d, want 180", ctx.Balance)
	}
	if after.note != "you already own that" {
		t.Errorf("note after refresh = %q, want the refusal", after.note)
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

// selectMenuRow moves the cursor onto a named menu row by pressing the keys a
// typist would, so a test does not need to know the row's index.
func selectMenuRow(t *testing.T, s Screen, title string) Screen {
	t.Helper()

	for range len(menuItems) + 1 {
		if strings.Contains(plain(s.View()), "> "+title) {
			return s
		}
		s, _ = s.Update(key("down"))
	}
	t.Fatalf("%q never became the selected row:\n%s", title, plain(s.View()))
	return s
}

func TestShopIsReachableFromTheMenuWithAnAccount(t *testing.T) {
	ctx, _ := richContext(t, 300)

	m := selectMenuRow(t, NewMenu(ctx), "Shop")
	_, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("selecting Shop produced no command")
	}
	nav, ok := cmd().(navigateMsg)
	if !ok {
		t.Fatalf("selecting Shop produced %T, want navigateMsg", cmd())
	}
	if _, ok := nav.to.(Shop); !ok {
		t.Errorf("selecting Shop opened %T", nav.to)
	}
}

func TestShopIsInertWithoutAnAccountAndSaysWhy(t *testing.T) {
	// An anonymous session has no wallet, so the row has to stay visible and
	// explain itself rather than vanish.
	m := Screen(NewMenu(newTestContext()))

	view := plain(m.View())
	if !strings.Contains(view, "Shop") {
		t.Fatalf("the Shop row disappeared for an anonymous session:\n%s", view)
	}
	if !strings.Contains(view, "(no account on this server)") {
		t.Errorf("the Shop row does not say why it is unavailable:\n%s", view)
	}

	// A disabled row never draws the cursor, so there is no way to see which
	// one is selected. Press enter on every row instead: none of them may open
	// the shop, which is a stronger claim than checking one index anyway.
	for range len(menuItems) {
		if _, cmd := m.Update(key("enter")); cmd != nil {
			if nav, ok := cmd().(navigateMsg); ok {
				if _, isShop := nav.to.(Shop); isShop {
					t.Fatal("an anonymous session reached the shop")
				}
			}
		}
		m, _ = m.Update(key("down"))
	}
}

// slowRepo delays every write, so a read that does not wait for the write
// queue is certain to miss it rather than merely likely to.
type slowRepo struct {
	store.Repository
	delay time.Duration
}

func (r slowRepo) RecordRun(ctx context.Context, run store.Run) error {
	time.Sleep(r.delay)
	return r.Repository.RecordRun(ctx, run)
}

// TestShopSeesBytesTheSessionJustEarned reproduces the balance regression: the
// award is queued through store.Async, and a wallet read that does not wait for
// it reports the balance from before the race — briefly refusing a purchase the
// typist can afford.
func TestShopSeesBytesTheSessionJustEarned(t *testing.T) {
	mem := store.NewMemory()
	repo := store.NewAsync(slowRepo{Repository: mem, delay: 150 * time.Millisecond}, 8, nil)
	t.Cleanup(func() { _ = repo.Close() })

	user, err := mem.ResolveUser(context.Background(), "SHA256:test", "tester")
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}

	ctx := newTestContext()
	ctx.Repo, ctx.User, ctx.Fingerprint = repo, user, "SHA256:test"

	// Bank an award the way a finished race does: queued, not yet written.
	if err := repo.RecordRun(context.Background(), store.Run{
		UserID: user.ID, Mode: store.ModeRace, WPM: 80, Accuracy: 0.97, Earned: 300,
	}); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}
	ctx.Balance += 300

	// The hazard: without waiting, the underlying store has nothing yet.
	if runs, _ := mem.RecentRuns(context.Background(), user.ID, 10); len(runs) != 0 {
		t.Fatal("the write landed before the shop opened; this test proves nothing")
	}

	shop := openShop(t, ctx)

	if shop.wallet.Balance != 300 {
		t.Errorf("the shop read a balance of %d, want 300 — it did not wait for the award",
			shop.wallet.Balance)
	}
	if ctx.Balance != 300 {
		t.Errorf("the session balance dropped to %d after a load, want 300", ctx.Balance)
	}

	// The symptom the typist would have seen: a purchase they can afford
	// refused because the read was stale.
	after := press(t, selectItem(t, shop, "bar-dots"), "enter").(Shop)
	if strings.Contains(plain(after.View()), "short") {
		t.Errorf("a purchase the typist could afford was refused:\n%s", plain(after.View()))
	}
	if !after.wallet.Owns("bar-dots") {
		t.Error("the purchase did not go through")
	}
}

// equipFailsRepo takes the money but refuses to wear the cosmetic, which is the
// partial failure buy() has to survive: the two calls are each atomic, the pair
// is not.
type equipFailsRepo struct {
	*store.Memory
}

func (equipFailsRepo) Equip(context.Context, string, string, string) (store.Wallet, error) {
	return store.Wallet{}, errDown
}

func TestShopKeepsAPurchaseWhoseEquipFailed(t *testing.T) {
	ctx, mem := richContext(t, 300)
	ctx.Repo = equipFailsRepo{Memory: mem}

	shop := openShop(t, ctx)
	after := press(t, selectItem(t, shop, "bar-dots"), "enter").(Shop)

	// The purchase stands, so the screen must know it is owned.
	if !after.wallet.Owns("bar-dots") {
		t.Fatal("the successful purchase was discarded when wearing it failed")
	}
	if after.wallet.Balance != 180 {
		t.Errorf("the shop shows %d bytes, want 180 — it is showing money already spent",
			after.wallet.Balance)
	}
	if ctx.Balance != 180 {
		t.Errorf("the session balance is %d, want 180", ctx.Balance)
	}

	// It is a half-done action, not a broken shop: the catalogue stays up and
	// the note explains what happened.
	view := plain(after.View())
	if strings.Contains(view, "could not reach the shop") {
		t.Errorf("a half-done purchase replaced the shop with an error screen:\n%s", view)
	}
	if !strings.Contains(view, "Blocks") {
		t.Errorf("the catalogue disappeared:\n%s", view)
	}
	if !strings.Contains(view, "could not wear it") {
		t.Errorf("the shop does not say the cosmetic was not worn:\n%s", view)
	}

	// And pressing enter again tries to wear it rather than to buy it twice.
	again := press(t, after, "enter").(Shop)
	if strings.Contains(plain(again.View()), "you already own that") {
		t.Errorf("the next press tried to re-buy an owned cosmetic:\n%s", plain(again.View()))
	}
}
