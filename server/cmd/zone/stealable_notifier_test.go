package main

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestStealableNotifierEnqueueNeverBlocksWhenFull(t *testing.T) {
	n := &zoneStealableNotifier{queue: make(chan stealableEvent, 1), logger: slog.Default()}
	if err := n.NotifyOwnerPlotStealable(context.Background(), 1, 2, "n1"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = n.NotifyOwnerPlotStealable(context.Background(), 1, 2, "n2")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("full notification queue blocked maturity caller")
	}
}
