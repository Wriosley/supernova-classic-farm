package actor

import (
	"context"
	"testing"
	"time"
)

func TestMailboxIdleWhenEmpty(t *testing.T) {
	m := NewMailbox(4)
	defer m.Close()
	if !m.Idle() {
		t.Fatal("empty mailbox must be idle")
	}
}

func TestMailboxNotIdleWhileQueuedOrRunning(t *testing.T) {
	m := NewMailbox(4)
	defer m.Close()

	started := make(chan struct{})
	release := make(chan struct{})
	if err := m.Submit(func() {
		close(started)
		<-release
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("job did not start")
	}
	if m.Idle() {
		t.Fatal("running job must make mailbox non-idle")
	}

	queued := make(chan struct{})
	if err := m.Submit(func() {
		close(queued)
	}); err != nil {
		t.Fatalf("queued Submit: %v", err)
	}
	if m.Idle() {
		t.Fatal("queued job must make mailbox non-idle")
	}

	close(release)
	select {
	case <-queued:
	case <-time.After(time.Second):
		t.Fatal("queued job did not run")
	}
	// Allow the worker to clear inflight after close(done).
	deadline := time.Now().Add(time.Second)
	for !m.Idle() {
		if time.Now().After(deadline) {
			t.Fatal("mailbox did not return to idle")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestMailboxNotIdleDuringDo(t *testing.T) {
	m := NewMailbox(1)
	defer m.Close()

	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- m.Do(context.Background(), func() {
			close(entered)
			<-release
		})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("Do did not start")
	}
	if m.Idle() {
		t.Fatal("Do in progress must make mailbox non-idle")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !m.Idle() {
		t.Fatal("mailbox must be idle after Do completes")
	}
}
