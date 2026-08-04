package actor

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMailboxSerializesSamePlayer(t *testing.T) {
	m := NewMailbox(8)
	defer m.Close()

	var active atomic.Int32
	var maxActive atomic.Int32
	var orderMu sync.Mutex
	var order []int
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(sequence int) {
			defer wg.Done()
			if err := m.Do(context.Background(), func() {
				current := active.Add(1)
				for {
					seen := maxActive.Load()
					if current <= seen || maxActive.CompareAndSwap(seen, current) {
						break
					}
				}
				time.Sleep(time.Millisecond)
				orderMu.Lock()
				order = append(order, sequence)
				orderMu.Unlock()
				active.Add(-1)
			}); err != nil {
				t.Errorf("Do() error = %v", err)
			}
		}(i)
	}
	wg.Wait()

	if got := maxActive.Load(); got != 1 {
		t.Fatalf("same mailbox maximum concurrency = %d, want 1", got)
	}
	if len(order) != 8 {
		t.Fatalf("executed jobs = %d, want 8", len(order))
	}
}

func TestDifferentMailboxesExecuteInParallel(t *testing.T) {
	first := NewMailbox(1)
	second := NewMailbox(1)
	defer first.Close()
	defer second.Close()

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	errs := make(chan error, 2)
	run := func(mailbox *Mailbox) {
		errs <- mailbox.Do(context.Background(), func() {
			started <- struct{}{}
			<-release
		})
	}
	go run(first)
	go run(second)

	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	for range 2 {
		select {
		case <-started:
		case <-timeout.C:
			t.Fatal("different player mailboxes did not overlap")
		}
	}
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("Do() error = %v", err)
		}
	}
}
