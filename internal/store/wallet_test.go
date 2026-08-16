package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Pikaryu729/typesafe/internal/store"
)

// earned returns a practice run worth n bytes.
func earned(userID string, n int) store.Run {
	r := run(userID, 60)
	r.Earned = n
	return r
}

// item is a cosmetic to buy. The slot and price are the only fields the store
// cares about; it knows nothing of the catalogue these come from.
func item(id, slot string, price int) store.Owned {
	return store.Owned{ID: id, Slot: slot, Price: price}
}

// fund records runs totalling n bytes for a fresh account and returns its ID.
func fund(t *testing.T, m *store.Memory, n int) string {
	t.Helper()
	ctx := context.Background()

	u, err := m.ResolveUser(ctx, "SHA256:funded", "tester")
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}
	if n > 0 {
		if err := m.RecordRun(ctx, earned(u.ID, n)); err != nil {
			t.Fatalf("RecordRun: %v", err)
		}
	}
	return u.ID
}

func TestBalanceIsEarnedLessBought(t *testing.T) {
	runs := []store.Run{{Earned: 30}, {Earned: 12}, {Earned: 0}}
	owned := []store.Owned{{Price: 20}}

	if got := store.Balance(runs, owned); got != 22 {
		t.Errorf("Balance = %d, want 22", got)
	}
	if got := store.Balance(nil, nil); got != 0 {
		t.Errorf("an empty history is worth %d, want 0", got)
	}
}

func TestWalletTracksEarningsAndPurchases(t *testing.T) {
	ctx := context.Background()
	m := store.NewMemory()
	id := fund(t, m, 300)

	w, err := m.Wallet(ctx, id)
	if err != nil {
		t.Fatalf("Wallet: %v", err)
	}
	if w.Balance != 300 {
		t.Fatalf("balance = %d, want 300", w.Balance)
	}

	if w, err = m.Buy(ctx, id, item("bar-dots", "bar", 120)); err != nil {
		t.Fatalf("Buy: %v", err)
	}
	if w.Balance != 180 {
		t.Errorf("balance after buying = %d, want 180", w.Balance)
	}
	if !w.Owns("bar-dots") {
		t.Error("the cosmetic just bought is not owned")
	}
	if w.Owned[0].BoughtAt.IsZero() {
		t.Error("the purchase has no timestamp")
	}
}

func TestWalletOfAnUnknownUserIsEmpty(t *testing.T) {
	w, err := store.NewMemory().Wallet(context.Background(), "nobody")
	if err != nil {
		t.Fatalf("Wallet: %v", err)
	}
	if w.Balance != 0 || len(w.Owned) != 0 {
		t.Errorf("Wallet of a stranger = %+v, want empty", w)
	}
}

func TestBuyRefusesWhatYouCannotAfford(t *testing.T) {
	ctx := context.Background()
	m := store.NewMemory()
	id := fund(t, m, 100)

	if _, err := m.Buy(ctx, id, item("color-gold", "color", 500)); !errors.Is(err, store.ErrInsufficientFunds) {
		t.Fatalf("Buy with 100 bytes for a 500 item: %v, want ErrInsufficientFunds", err)
	}

	w, err := m.Wallet(ctx, id)
	if err != nil {
		t.Fatalf("Wallet: %v", err)
	}
	if w.Balance != 100 {
		t.Errorf("a refused purchase moved the balance to %d", w.Balance)
	}
	if len(w.Owned) != 0 {
		t.Errorf("a refused purchase left %+v behind", w.Owned)
	}
}

func TestBuyRefusesADuplicate(t *testing.T) {
	ctx := context.Background()
	m := store.NewMemory()
	id := fund(t, m, 500)

	if _, err := m.Buy(ctx, id, item("bar-dots", "bar", 120)); err != nil {
		t.Fatalf("Buy: %v", err)
	}
	if _, err := m.Buy(ctx, id, item("bar-dots", "bar", 120)); !errors.Is(err, store.ErrAlreadyOwned) {
		t.Fatalf("buying twice: %v, want ErrAlreadyOwned", err)
	}

	w, _ := m.Wallet(ctx, id)
	if w.Balance != 380 {
		t.Errorf("balance = %d, want 380; the second attempt should not have charged", w.Balance)
	}
}

func TestBuyingExactlyWhatYouHaveWorks(t *testing.T) {
	ctx := context.Background()
	m := store.NewMemory()
	id := fund(t, m, 120)

	w, err := m.Buy(ctx, id, item("bar-dots", "bar", 120))
	if err != nil {
		t.Fatalf("Buy with exactly enough: %v", err)
	}
	if w.Balance != 0 {
		t.Errorf("balance = %d, want 0", w.Balance)
	}
}

func TestEquipHoldsOnePerSlot(t *testing.T) {
	ctx := context.Background()
	m := store.NewMemory()
	id := fund(t, m, 1000)

	for _, it := range []store.Owned{
		item("bar-dots", "bar", 120),
		item("bar-shade", "bar", 120),
		item("color-gold", "color", 500),
	} {
		if _, err := m.Buy(ctx, id, it); err != nil {
			t.Fatalf("Buy %s: %v", it.ID, err)
		}
	}

	// Buying does not wear anything.
	w, _ := m.Wallet(ctx, id)
	if got := w.EquippedIn("bar"); got != "" {
		t.Errorf("buying equipped %q on its own", got)
	}

	if w, _ = m.Equip(ctx, id, "bar", "bar-dots"); w.EquippedIn("bar") != "bar-dots" {
		t.Fatalf("bar slot holds %q, want bar-dots", w.EquippedIn("bar"))
	}

	// Wearing a second bar replaces the first rather than stacking.
	w, err := m.Equip(ctx, id, "bar", "bar-shade")
	if err != nil {
		t.Fatalf("Equip: %v", err)
	}
	if got := w.EquippedIn("bar"); got != "bar-shade" {
		t.Errorf("bar slot holds %q, want bar-shade", got)
	}
	var wearing int
	for _, o := range w.Owned {
		if o.Slot == "bar" && o.Equipped {
			wearing++
		}
	}
	if wearing != 1 {
		t.Errorf("%d bars equipped at once, want 1", wearing)
	}

	// Slots are independent.
	if w, _ = m.Equip(ctx, id, "color", "color-gold"); w.EquippedIn("bar") != "bar-shade" {
		t.Error("equipping a colour disturbed the bar slot")
	}
}

func TestEquipNothingReturnsToTheDefault(t *testing.T) {
	ctx := context.Background()
	m := store.NewMemory()
	id := fund(t, m, 500)

	if _, err := m.Buy(ctx, id, item("bar-dots", "bar", 120)); err != nil {
		t.Fatalf("Buy: %v", err)
	}
	if _, err := m.Equip(ctx, id, "bar", "bar-dots"); err != nil {
		t.Fatalf("Equip: %v", err)
	}

	w, err := m.Equip(ctx, id, "bar", "")
	if err != nil {
		t.Fatalf("unequip: %v", err)
	}
	if got := w.EquippedIn("bar"); got != "" {
		t.Errorf("bar slot still holds %q after unequipping", got)
	}
	if !w.Owns("bar-dots") {
		t.Error("unequipping took the cosmetic away")
	}
}

func TestEquipWhatYouDoNotOwn(t *testing.T) {
	ctx := context.Background()
	m := store.NewMemory()
	id := fund(t, m, 500)

	if _, err := m.Equip(ctx, id, "bar", "bar-dots"); !errors.Is(err, store.ErrNotOwned) {
		t.Fatalf("equipping an unowned cosmetic: %v, want ErrNotOwned", err)
	}

	// Owned, but named under the wrong slot: still not something to wear.
	if _, err := m.Buy(ctx, id, item("bar-dots", "bar", 120)); err != nil {
		t.Fatalf("Buy: %v", err)
	}
	if _, err := m.Equip(ctx, id, "color", "bar-dots"); !errors.Is(err, store.ErrNotOwned) {
		t.Fatalf("equipping a bar in the colour slot: %v, want ErrNotOwned", err)
	}
}

func TestConcurrentBuysSpendEachByteOnce(t *testing.T) {
	ctx := context.Background()
	m := store.NewMemory()
	id := fund(t, m, 120)

	// Two sessions of one account, both able to afford one item and only one.
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		bought  int
		refused int
	)
	for _, it := range []store.Owned{
		item("bar-dots", "bar", 120),
		item("bar-shade", "bar", 120),
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.Buy(ctx, id, it)

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				bought++
			case errors.Is(err, store.ErrInsufficientFunds):
				refused++
			default:
				t.Errorf("Buy: %v", err)
			}
		}()
	}
	wg.Wait()

	if bought != 1 || refused != 1 {
		t.Errorf("%d bought and %d refused, want exactly one of each", bought, refused)
	}
	if w, _ := m.Wallet(ctx, id); w.Balance != 0 {
		t.Errorf("balance = %d, want 0; bytes were spent twice or not at all", w.Balance)
	}
}

func TestLinkCodeMergesWalletsAndRefundsDuplicates(t *testing.T) {
	ctx := context.Background()
	m := store.NewMemory()

	target, err := m.ResolveUser(ctx, "SHA256:desktop", "alice")
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}
	source, err := m.ResolveUser(ctx, "SHA256:laptop", "alice-laptop")
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}

	// Each side earns, and each buys the same bar. The laptop also owns a
	// colour the desktop does not, and is wearing both.
	if err := m.RecordRun(ctx, earned(target.ID, 400)); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}
	if err := m.RecordRun(ctx, earned(source.ID, 800)); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}
	for _, tc := range []struct {
		user string
		it   store.Owned
	}{
		{target.ID, item("bar-dots", "bar", 120)},
		{source.ID, item("bar-dots", "bar", 120)},
		{source.ID, item("color-gold", "color", 500)},
	} {
		if _, err := m.Buy(ctx, tc.user, tc.it); err != nil {
			t.Fatalf("Buy %s: %v", tc.it.ID, err)
		}
	}
	if _, err := m.Equip(ctx, source.ID, "bar", "bar-dots"); err != nil {
		t.Fatalf("Equip: %v", err)
	}
	if _, err := m.Equip(ctx, source.ID, "color", "color-gold"); err != nil {
		t.Fatalf("Equip: %v", err)
	}

	code, err := m.CreateLinkCode(ctx, target.ID)
	if err != nil {
		t.Fatalf("CreateLinkCode: %v", err)
	}
	if _, err := m.RedeemLinkCode(ctx, code.Code, "SHA256:laptop"); err != nil {
		t.Fatalf("RedeemLinkCode: %v", err)
	}

	w, err := m.Wallet(ctx, target.ID)
	if err != nil {
		t.Fatalf("Wallet: %v", err)
	}

	// Earned 400 + 800; the two distinct cosmetics cost 120 + 500. The
	// duplicate bar is dropped rather than moved, which hands its price back.
	if w.Balance != 580 {
		t.Errorf("balance = %d, want 580", w.Balance)
	}
	if len(w.Owned) != 2 {
		t.Errorf("owns %d cosmetics, want 2: %+v", len(w.Owned), w.Owned)
	}
	if !w.Owns("bar-dots") || !w.Owns("color-gold") {
		t.Errorf("the merged account is missing a cosmetic: %+v", w.Owned)
	}

	// What moved arrives unequipped, so the merge cannot produce two of a slot.
	for _, o := range w.Owned {
		if o.ID == "color-gold" && o.Equipped {
			t.Error("a cosmetic carried its equipped state across the merge")
		}
	}

	if w, err := m.Wallet(ctx, source.ID); err != nil || len(w.Owned) != 0 {
		t.Errorf("the merged-away account still owns %+v (err %v)", w.Owned, err)
	}
}
