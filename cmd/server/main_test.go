package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Pikaryu729/typesafe/internal/store"
)

type heldWriteRepo struct {
	store.Repository
	started     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
}

func (r *heldWriteRepo) releaseWrite() {
	r.releaseOnce.Do(func() { close(r.release) })
}

func (r *heldWriteRepo) RecordRun(ctx context.Context, run store.Run) error {
	close(r.started)
	<-r.release
	return r.Repository.RecordRun(ctx, run)
}

func TestLoadWalletFlushesQueuedWrites(t *testing.T) {
	mem := store.NewMemory()
	user, err := mem.ResolveUser(context.Background(), "SHA256:test", "tester")
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}

	underlying := heldWriteRepo{
		Repository: mem,
		started:    make(chan struct{}),
		release:    make(chan struct{}),
	}
	repo := store.NewAsync(&underlying, 1, nil)
	defer func() {
		underlying.releaseWrite()
		_ = repo.Close()
	}()

	if err := repo.RecordRun(context.Background(), store.Run{
		UserID: user.ID,
		Earned: 300,
	}); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}
	select {
	case <-underlying.started:
	case <-time.After(time.Second):
		t.Fatal("the queued write did not start")
	}

	wallet := make(chan store.Wallet, 1)
	go func() {
		wallet <- loadWalletContext(context.Background(), "tester", repo, user)
	}()

	select {
	case <-wallet:
		t.Fatal("loadWallet returned before the queued write was released")
	case <-time.After(50 * time.Millisecond):
	}

	underlying.releaseWrite()
	select {
	case got := <-wallet:
		if got.Balance != 300 {
			t.Fatalf("loadWallet balance = %d, want 300", got.Balance)
		}
	case <-time.After(time.Second):
		t.Fatal("loadWallet did not return after the queued write was released")
	}
}
