package visit

import (
	"bytes"
	"testing"
	"time"
)

func TestRegistryEnterIsIdempotentByRequestID(t *testing.T) {
	reg := NewRegistry()
	now := time.Unix(1000, 0)
	id1, exp1, created1, err := reg.Enter(1, 2, "gate-a", "req-1", now)
	if err != nil || !created1 {
		t.Fatalf("Enter #1: id=%x exp=%v created=%v err=%v", id1, exp1, created1, err)
	}
	later := now.Add(10 * time.Second)
	id2, exp2, created2, err := reg.Enter(1, 2, "gate-a", "req-1", later)
	if err != nil {
		t.Fatalf("Enter retry: %v", err)
	}
	if created2 {
		t.Fatalf("Enter retry with same request_id should not report newlyCreated")
	}
	if !bytes.Equal(id1, id2) {
		t.Fatalf("Enter retry visit_id changed: %x != %x", id1, id2)
	}
	if !exp2.After(exp1) {
		t.Fatalf("Enter retry should extend TTL: exp1=%v exp2=%v", exp1, exp2)
	}
}

func TestRegistryReEnterWithoutMatchingRequestIDReplacesVisit(t *testing.T) {
	reg := NewRegistry()
	now := time.Unix(1000, 0)
	id1, _, _, err := reg.Enter(1, 2, "gate-a", "req-1", now)
	if err != nil {
		t.Fatalf("Enter #1: %v", err)
	}
	id2, _, created2, err := reg.Enter(1, 2, "gate-a", "req-2", now.Add(time.Second))
	if err != nil {
		t.Fatalf("Enter #2: %v", err)
	}
	if !created2 {
		t.Fatalf("Enter with a different request_id should report newlyCreated")
	}
	if bytes.Equal(id1, id2) {
		t.Fatalf("re-enter should mint a new visit_id, got the same %x", id1)
	}
	// The old visit_id must no longer validate.
	if err := reg.Validate(1, 2, id1, now.Add(time.Second)); err != ErrVisitNotFound {
		t.Fatalf("Validate(old visit_id) = %v, want ErrVisitNotFound", err)
	}
	if err := reg.Validate(1, 2, id2, now.Add(time.Second)); err != nil {
		t.Fatalf("Validate(new visit_id) = %v, want nil", err)
	}
}

func TestRegistryRefreshExtendsTTLAndRejectsWrongVisitID(t *testing.T) {
	reg := NewRegistry()
	now := time.Unix(1000, 0)
	id, exp0, _, err := reg.Enter(1, 2, "gate-a", "req-1", now)
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	exp1, err := reg.Refresh(1, 2, id, "gate-a", now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !exp1.After(exp0) {
		t.Fatalf("Refresh should push expiry forward: exp0=%v exp1=%v", exp0, exp1)
	}
	if _, err := reg.Refresh(1, 2, []byte("not-the-visit-id"), "gate-a", now); err != ErrVisitNotFound {
		t.Fatalf("Refresh(wrong visit_id) = %v, want ErrVisitNotFound", err)
	}
	if _, err := reg.Refresh(1, 999, id, "gate-a", now); err != ErrVisitNotFound {
		t.Fatalf("Refresh(wrong visitor) = %v, want ErrVisitNotFound", err)
	}
}

func TestRegistryRefreshAfterTTLReportsExpiredAndEvicts(t *testing.T) {
	reg := NewRegistry()
	now := time.Unix(1000, 0)
	id, _, _, err := reg.Enter(1, 2, "gate-a", "req-1", now)
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	expired := now.Add(VisitTTL + time.Second)
	if _, err := reg.Refresh(1, 2, id, "gate-a", expired); err != ErrVisitExpired {
		t.Fatalf("Refresh(after TTL) = %v, want ErrVisitExpired", err)
	}
	if err := reg.Validate(1, 2, id, expired); err != ErrVisitNotFound {
		t.Fatalf("Validate after expired-refresh eviction = %v, want ErrVisitNotFound", err)
	}
}

func TestRegistryExitRemovesVisit(t *testing.T) {
	reg := NewRegistry()
	now := time.Unix(1000, 0)
	id, _, _, err := reg.Enter(1, 2, "gate-a", "req-1", now)
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	removed, err := reg.Exit(1, 2, id)
	if err != nil {
		t.Fatalf("Exit: %v", err)
	}
	if removed.OwnerPlayerID != 1 || removed.VisitorPlayerID != 2 {
		t.Fatalf("removed record = %+v", removed)
	}
	if _, err := reg.Exit(1, 2, id); err != ErrVisitNotFound {
		t.Fatalf("Exit after already-removed = %v, want ErrVisitNotFound", err)
	}
	if got := reg.ListVisitors(1); len(got) != 0 {
		t.Fatalf("ListVisitors after Exit = %+v, want empty", got)
	}
}

func TestRegistryEvictExpiredReturnsAllStaleLeases(t *testing.T) {
	reg := NewRegistry()
	now := time.Unix(1000, 0)
	if _, _, _, err := reg.Enter(1, 2, "gate-a", "req-1", now); err != nil {
		t.Fatalf("Enter visitor 2: %v", err)
	}
	if _, _, _, err := reg.Enter(1, 3, "gate-a", "req-2", now); err != nil {
		t.Fatalf("Enter visitor 3: %v", err)
	}
	// Visitor 3 refreshes just before the sweep so it should survive.
	refreshTime := now.Add(VisitTTL - time.Second)
	visitors := reg.ListVisitors(1)
	var idFor3 []byte
	for _, v := range visitors {
		if v.VisitorPlayerID == 3 {
			idFor3 = v.VisitID
		}
	}
	if _, err := reg.Refresh(1, 3, idFor3, "gate-a", refreshTime); err != nil {
		t.Fatalf("Refresh visitor 3: %v", err)
	}

	sweepTime := now.Add(VisitTTL + time.Second)
	removed := reg.EvictExpired(sweepTime)
	if len(removed) != 1 || removed[0].VisitorPlayerID != 2 {
		t.Fatalf("EvictExpired = %+v, want only visitor 2", removed)
	}
	remaining := reg.ListVisitors(1)
	if len(remaining) != 1 || remaining[0].VisitorPlayerID != 3 {
		t.Fatalf("remaining visitors = %+v, want only visitor 3", remaining)
	}
}

func TestRegistryListVisitorsForUnknownOwnerIsEmpty(t *testing.T) {
	reg := NewRegistry()
	if got := reg.ListVisitors(42); len(got) != 0 {
		t.Fatalf("ListVisitors(unknown owner) = %+v, want empty", got)
	}
}
