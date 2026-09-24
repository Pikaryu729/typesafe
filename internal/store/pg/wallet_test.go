//go:build integration

package pg_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Pikaryu729/typesafe/internal/cosmetics"
	"github.com/Pikaryu729/typesafe/internal/store"
	"github.com/Pikaryu729/typesafe/internal/store/pg"
)

// fundedUser returns a fresh account holding n bytes.
func fundedUser(t *testing.T, repo *pg.Repo, fingerprint string, n int) store.User {
	t.Helper()
	ctx := context.Background()

	u, err := repo.ResolveUser(ctx, fingerprint, "tester")
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}
	if n > 0 {
		if err := repo.RecordRun(ctx, store.Run{
			UserID: u.ID, Mode: store.ModePractice, Seed: 1, WordCount: 30,
			WPM: 60, Accuracy: 0.95, Earned: n,
		}); err != nil {
			t.Fatalf("RecordRun: %v", err)
		}
	}
	return u
}

func TestEarningsRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	u := fundedUser(t, repo, "SHA256:aaa", 34)

	runs, err := repo.RecentRuns(ctx, u.ID, 10)
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	if runs[0].Earned != 34 {
		t.Errorf("Earned = %d, want 34", runs[0].Earned)
	}
}

// TestWalletMatchesBalance is the counterpart of TestSummaryMatchesSummarize:
// store.Balance and the SQL that recomputes it are two implementations of one
// definition, and nothing but this test stops them drifting apart.
func TestWalletMatchesBalance(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	u, _ := repo.ResolveUser(ctx, "SHA256:aaa", "ryu")

	for i, n := range []int{34, 0, 12, 27, 8} {
		if err := repo.RecordRun(ctx, store.Run{
			UserID: u.ID, Mode: store.ModePractice, Seed: int64(i), WordCount: 30,
			WPM: float64(50 + i), Accuracy: 0.95, Earned: n,
		}); err != nil {
			t.Fatalf("RecordRun: %v", err)
		}
	}
	for _, it := range []store.Owned{
		{ID: "bar-dots", Slot: "bar", Price: 60},
		{ID: "color-gold", Slot: "color", Price: 12},
	} {
		if _, err := repo.Buy(ctx, u.ID, it); err != nil {
			t.Fatalf("Buy %s: %v", it.ID, err)
		}
	}

	got, err := repo.Wallet(ctx, u.ID)
	if err != nil {
		t.Fatalf("Wallet: %v", err)
	}
	runs, err := repo.RecentRuns(ctx, u.ID, 100)
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}

	if want := store.Balance(runs, got.Owned); got.Balance != want {
		t.Errorf("Wallet.Balance = %d, store.Balance = %d", got.Balance, want)
	}
	if len(got.Owned) != 2 {
		t.Errorf("owns %d cosmetics, want 2: %+v", len(got.Owned), got.Owned)
	}
	for _, o := range got.Owned {
		if o.BoughtAt.IsZero() {
			t.Errorf("%s has no purchase time", o.ID)
		}
	}
}

func TestWalletOfAnUnknownUserIsEmpty(t *testing.T) {
	w, err := newRepo(t).Wallet(context.Background(), "nobody")
	if err != nil {
		t.Fatalf("Wallet: %v", err)
	}
	if w.Balance != 0 || len(w.Owned) != 0 {
		t.Errorf("Wallet of a stranger = %+v, want empty", w)
	}
}

func TestBuyRefusesWhatYouCannotAffordAndDuplicates(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	u := fundedUser(t, repo, "SHA256:aaa", 150)

	if _, err := repo.Buy(ctx, u.ID, store.Owned{ID: "color-gold", Slot: "color", Price: 500}); !errors.Is(err, store.ErrInsufficientFunds) {
		t.Fatalf("unaffordable Buy: %v, want ErrInsufficientFunds", err)
	}

	if _, err := repo.Buy(ctx, u.ID, store.Owned{ID: "bar-dots", Slot: "bar", Price: 120}); err != nil {
		t.Fatalf("Buy: %v", err)
	}
	if _, err := repo.Buy(ctx, u.ID, store.Owned{ID: "bar-dots", Slot: "bar", Price: 120}); !errors.Is(err, store.ErrAlreadyOwned) {
		t.Fatalf("duplicate Buy: %v, want ErrAlreadyOwned", err)
	}

	w, _ := repo.Wallet(ctx, u.ID)
	if w.Balance != 30 {
		t.Errorf("balance = %d, want 30; a refused purchase charged anyway", w.Balance)
	}
}

// TestConcurrentBuysSpendEachByteOnce is why Buy takes an advisory lock. One
// person can hold several sessions, so two purchases really can race, and a
// balance read outside a lock would let both of them see enough bytes.
func TestConcurrentBuysSpendEachByteOnce(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	u := fundedUser(t, repo, "SHA256:aaa", 120)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		bought  int
		refused int
	)
	for _, it := range []store.Owned{
		{ID: "bar-dots", Slot: "bar", Price: 120},
		{ID: "bar-shade", Slot: "bar", Price: 120},
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repo.Buy(ctx, u.ID, it)

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
	if w, _ := repo.Wallet(ctx, u.ID); w.Balance != 0 {
		t.Errorf("balance = %d, want 0; bytes were spent twice or not at all", w.Balance)
	}
}

func TestAccountMergesWaitForRunsOnTheSameAccount(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	target := fundedUser(t, repo, "SHA256:target", 0)
	source := fundedUser(t, repo, "SHA256:source", 0)
	code, err := repo.CreateLinkCode(ctx, target.ID)
	if err != nil {
		t.Fatalf("CreateLinkCode: %v", err)
	}

	release, err := repo.HoldAccountLock(ctx, source.ID)
	if err != nil {
		t.Fatalf("HoldAccountLock: %v", err)
	}
	t.Cleanup(release)
	runDone := make(chan error, 1)
	go func() {
		runDone <- repo.RecordRun(ctx, store.Run{
			UserID: source.ID, Mode: store.ModePractice, Earned: 42,
		})
	}()
	select {
	case err := <-runDone:
		t.Fatalf("RecordRun completed while the account was locked: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if err := <-runDone; err != nil {
		t.Fatalf("RecordRun: %v", err)
	}

	release, err = repo.HoldAccountLock(ctx, source.ID)
	if err != nil {
		t.Fatalf("HoldAccountLock: %v", err)
	}
	t.Cleanup(release)
	mergeDone := make(chan error, 1)
	go func() {
		_, err := repo.RedeemLinkCode(ctx, code.Code, "SHA256:source")
		mergeDone <- err
	}()
	select {
	case err := <-mergeDone:
		t.Fatalf("RedeemLinkCode completed while the account was locked: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if err := <-mergeDone; err != nil {
		t.Fatalf("RedeemLinkCode: %v", err)
	}

	runs, err := repo.RecentRuns(ctx, target.ID, 10)
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].Earned != 42 {
		t.Errorf("merged runs = %+v, want the source run", runs)
	}
}

func TestConcurrentWalletReadsAreSelfConsistent(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	u := fundedUser(t, repo, "SHA256:wallet", 10000)

	var (
		readers sync.WaitGroup
		buyers  sync.WaitGroup
		fail    = make(chan error, 1)
		once    sync.Once
	)
	for range 12 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for range 300 {
				w, err := repo.Wallet(ctx, u.ID)
				if err != nil {
					once.Do(func() { fail <- err })
					return
				}
				want := 10000
				for _, o := range w.Owned {
					want -= o.Price
				}
				if w.Balance != want {
					once.Do(func() {
						fail <- fmt.Errorf("wallet balance %d disagrees with owned cosmetics: want %d", w.Balance, want)
					})
					return
				}
			}
		}()
	}
	for _, item := range cosmetics.Catalog {
		buyers.Add(1)
		go func(item cosmetics.Item) {
			defer buyers.Done()
			if _, err := repo.Buy(ctx, u.ID, store.Owned{ID: item.ID, Slot: string(item.Slot), Price: item.Price}); err != nil {
				once.Do(func() { fail <- err })
			}
		}(item)
	}
	buyers.Wait()
	readers.Wait()
	select {
	case err := <-fail:
		t.Fatal(err)
	default:
	}
}

func TestEquipHoldsOnePerSlot(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	u := fundedUser(t, repo, "SHA256:aaa", 1000)

	for _, it := range []store.Owned{
		{ID: "bar-dots", Slot: "bar", Price: 120},
		{ID: "bar-shade", Slot: "bar", Price: 120},
		{ID: "color-gold", Slot: "color", Price: 500},
	} {
		if _, err := repo.Buy(ctx, u.ID, it); err != nil {
			t.Fatalf("Buy %s: %v", it.ID, err)
		}
	}

	w, _ := repo.Wallet(ctx, u.ID)
	if got := w.EquippedIn("bar"); got != "" {
		t.Errorf("buying equipped %q on its own", got)
	}

	if w, _ = repo.Equip(ctx, u.ID, "bar", "bar-dots"); w.EquippedIn("bar") != "bar-dots" {
		t.Fatalf("bar slot holds %q, want bar-dots", w.EquippedIn("bar"))
	}

	// The partial unique index would reject a second equipped row outright,
	// so this also proves the slot is cleared before the new one is worn.
	w, err := repo.Equip(ctx, u.ID, "bar", "bar-shade")
	if err != nil {
		t.Fatalf("Equip: %v", err)
	}
	if got := w.EquippedIn("bar"); got != "bar-shade" {
		t.Errorf("bar slot holds %q, want bar-shade", got)
	}

	if w, _ = repo.Equip(ctx, u.ID, "color", "color-gold"); w.EquippedIn("bar") != "bar-shade" {
		t.Error("equipping a colour disturbed the bar slot")
	}

	if w, err = repo.Equip(ctx, u.ID, "bar", ""); err != nil {
		t.Fatalf("unequip: %v", err)
	}
	if got := w.EquippedIn("bar"); got != "" {
		t.Errorf("bar slot still holds %q after unequipping", got)
	}
	if !w.Owns("bar-shade") {
		t.Error("unequipping took the cosmetic away")
	}
}

// TestConcurrentEquipsLeaveOneWorn is why Equip takes the same advisory lock
// Buy does. Without it both sessions clear the slot, both set it, and the
// one-per-slot index rejects the loser with an error nobody can act on.
func TestConcurrentEquipsLeaveOneWorn(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	u := fundedUser(t, repo, "SHA256:aaa", 1000)

	for _, id := range []string{"bar-dots", "bar-shade"} {
		if _, err := repo.Buy(ctx, u.ID, store.Owned{ID: id, Slot: "bar", Price: 120}); err != nil {
			t.Fatalf("Buy %s: %v", id, err)
		}
	}

	var wg sync.WaitGroup
	for _, id := range []string{"bar-dots", "bar-shade"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := repo.Equip(ctx, u.ID, "bar", id); err != nil {
				t.Errorf("Equip %s: %v", id, err)
			}
		}()
	}
	wg.Wait()

	w, err := repo.Wallet(ctx, u.ID)
	if err != nil {
		t.Fatalf("Wallet: %v", err)
	}
	var worn int
	for _, o := range w.Owned {
		if o.Equipped {
			worn++
		}
	}
	if worn != 1 {
		t.Errorf("%d cosmetics worn in one slot, want 1: %+v", worn, w.Owned)
	}
}

func TestEquipWhatYouDoNotOwn(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	u := fundedUser(t, repo, "SHA256:aaa", 500)

	if _, err := repo.Equip(ctx, u.ID, "bar", "bar-dots"); !errors.Is(err, store.ErrNotOwned) {
		t.Fatalf("equipping an unowned cosmetic: %v, want ErrNotOwned", err)
	}
	if _, err := repo.Buy(ctx, u.ID, store.Owned{ID: "bar-dots", Slot: "bar", Price: 120}); err != nil {
		t.Fatalf("Buy: %v", err)
	}
	if _, err := repo.Equip(ctx, u.ID, "color", "bar-dots"); !errors.Is(err, store.ErrNotOwned) {
		t.Fatalf("equipping a bar in the colour slot: %v, want ErrNotOwned", err)
	}
}

func TestLinkCodeMergesWalletsAndRefundsDuplicates(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)

	target := fundedUser(t, repo, "SHA256:desktop", 400)
	source := fundedUser(t, repo, "SHA256:laptop", 800)

	for _, tc := range []struct {
		user string
		it   store.Owned
	}{
		{target.ID, store.Owned{ID: "bar-dots", Slot: "bar", Price: 120}},
		{source.ID, store.Owned{ID: "bar-dots", Slot: "bar", Price: 120}},
		{source.ID, store.Owned{ID: "color-gold", Slot: "color", Price: 500}},
	} {
		if _, err := repo.Buy(ctx, tc.user, tc.it); err != nil {
			t.Fatalf("Buy %s: %v", tc.it.ID, err)
		}
	}
	for _, slot := range []struct{ slot, id string }{{"bar", "bar-dots"}, {"color", "color-gold"}} {
		if _, err := repo.Equip(ctx, source.ID, slot.slot, slot.id); err != nil {
			t.Fatalf("Equip %s: %v", slot.id, err)
		}
	}

	code, err := repo.CreateLinkCode(ctx, target.ID)
	if err != nil {
		t.Fatalf("CreateLinkCode: %v", err)
	}
	if _, err := repo.RedeemLinkCode(ctx, code.Code, "SHA256:laptop"); err != nil {
		t.Fatalf("RedeemLinkCode: %v", err)
	}

	w, err := repo.Wallet(ctx, target.ID)
	if err != nil {
		t.Fatalf("Wallet: %v", err)
	}

	// Earned 400 + 800; the two distinct cosmetics cost 120 + 500. The
	// duplicate bar is dropped rather than moved, which hands its price back.
	if w.Balance != 580 {
		t.Errorf("balance = %d, want 580", w.Balance)
	}
	if len(w.Owned) != 2 || !w.Owns("bar-dots") || !w.Owns("color-gold") {
		t.Errorf("merged account owns %+v, want bar-dots and color-gold", w.Owned)
	}
	for _, o := range w.Owned {
		if o.Equipped {
			t.Errorf("%s carried its equipped state across the merge", o.ID)
		}
	}
}
