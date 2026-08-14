package visit

import (
	"testing"
	"time"
)

func TestRegistryHasVisitorsLiveAndExpired(t *testing.T) {
	reg := NewRegistry()
	now := time.Unix(1000, 0)
	if reg.HasVisitors(1, now) {
		t.Fatal("empty owner must have no visitors")
	}
	if _, _, _, err := reg.Enter(1, 2, "gate-a", "req-1", now); err != nil {
		t.Fatalf("Enter: %v", err)
	}
	if !reg.HasVisitors(1, now.Add(time.Second)) {
		t.Fatal("live visit must count as visitors")
	}
	if reg.HasVisitors(1, now.Add(VisitTTL+time.Second)) {
		t.Fatal("expired visit must not count as visitors")
	}
	if reg.HasVisitors(99, now.Add(time.Second)) {
		t.Fatal("other owner must have no visitors")
	}
}
