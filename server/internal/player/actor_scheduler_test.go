package player

import (
	"testing"
	"time"
)

func TestActorSchedulerEarliestFiresFirst(t *testing.T) {
	book := newActorDeadlineBook()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	book.schedule(2, now.Add(2*time.Second), 1)
	book.schedule(1, now.Add(1*time.Second), 1)

	got, ok := book.popDue(now.Add(1500 * time.Millisecond))
	if !ok || got.playerID != 1 || got.generation != 1 {
		t.Fatalf("first due = %+v ok=%t, want player 1", got, ok)
	}
	got, ok = book.popDue(now.Add(3 * time.Second))
	if !ok || got.playerID != 2 {
		t.Fatalf("second due = %+v ok=%t, want player 2", got, ok)
	}
}

func TestActorSchedulerRescheduleInvalidatesOldGeneration(t *testing.T) {
	book := newActorDeadlineBook()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	book.schedule(7, now.Add(time.Second), 1)
	book.schedule(7, now.Add(5*time.Second), 2)

	if _, ok := book.popDue(now.Add(2 * time.Second)); ok {
		t.Fatal("stale generation must not deliver")
	}
	got, ok := book.popDue(now.Add(6 * time.Second))
	if !ok || got.generation != 2 {
		t.Fatalf("rescheduled delivery = %+v ok=%t", got, ok)
	}
}

func TestActorSchedulerCancelPreventsDelivery(t *testing.T) {
	book := newActorDeadlineBook()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	book.schedule(9, now.Add(time.Second), 1)
	book.cancel(9)
	if _, ok := book.popDue(now.Add(2 * time.Second)); ok {
		t.Fatal("cancelled deadline must not deliver")
	}
}

func TestActorSchedulerNoDeadlineProducesNoDelivery(t *testing.T) {
	book := newActorDeadlineBook()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	if _, ok := book.peek(); ok {
		t.Fatal("empty book must have no peek")
	}
	if _, ok := book.popDue(now); ok {
		t.Fatal("empty book must not deliver")
	}
}
