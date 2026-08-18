package actor

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMailboxBeginAwaitSuspendsUntilResume(t *testing.T) {
	m := NewMailbox(4)
	defer m.Close()

	started := make(chan struct{})
	resumed := make(chan struct{})
	handle, err := m.BeginAwait(context.Background(), func(h *AwaitHandle) error {
		close(started)
		h.Suspend()
		go func() {
			time.Sleep(10 * time.Millisecond)
			if err := h.Resume(context.Background(), func() error {
				close(resumed)
				return nil
			}); err != nil {
				t.Errorf("Resume: %v", err)
			}
		}()
		return nil
	})
	if err != nil {
		t.Fatalf("BeginAwait: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("initial await step did not start")
	}
	if !m.Idle() {
		t.Fatal("mailbox should be idle while await is suspended")
	}
	if err := handle.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	select {
	case <-resumed:
	case <-time.After(time.Second):
		t.Fatal("resume continuation did not run")
	}
	if !m.Idle() {
		t.Fatal("mailbox should be idle after await completes")
	}
}

func TestMailboxBeginAwaitPreservesQueueOrder(t *testing.T) {
	m := NewMailbox(4)
	defer m.Close()

	order := make(chan string, 2)
	handle, err := m.BeginAwait(context.Background(), func(h *AwaitHandle) error {
		h.Suspend()
		go func() {
			time.Sleep(20 * time.Millisecond)
			if err := h.Resume(context.Background(), func() error {
				order <- "resume"
				return nil
			}); err != nil {
				t.Errorf("Resume: %v", err)
			}
		}()
		return nil
	})
	if err != nil {
		t.Fatalf("BeginAwait: %v", err)
	}
	if err := m.Submit(func() { order <- "later" }); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if got := <-order; got != "later" {
		t.Fatalf("first completed step = %q, want later", got)
	}
	if got := <-order; got != "resume" {
		t.Fatalf("second completed step = %q, want resume", got)
	}
	if err := handle.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestMailboxAwaitHandleCompleteFailsWait(t *testing.T) {
	m := NewMailbox(1)
	defer m.Close()

	boom := errors.New("boom")
	handle, err := m.BeginAwait(context.Background(), func(h *AwaitHandle) error {
		h.Suspend()
		go h.Complete(boom)
		return nil
	})
	if err != nil {
		t.Fatalf("BeginAwait: %v", err)
	}
	if err := handle.Wait(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Wait error = %v, want %v", err, boom)
	}
}
