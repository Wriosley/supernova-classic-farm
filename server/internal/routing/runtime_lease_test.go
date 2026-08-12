package routing

import (
	"errors"
	"testing"
	"time"
)

func TestRuntimeLeaseRenewalChangesOnlyEffectiveExpiry(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	routes, err := NewLocalMap(now, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	before := routes.Snapshot()
	overlay, err := NewRuntimeLeaseOverlay(before, now, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := overlay.Renew(before, now.Add(20*time.Second), 30*time.Second); err != nil {
		t.Fatal(err)
	}
	after := routes.Snapshot()
	if after.MapVersion != before.MapVersion || after.Entries[42] != before.Entries[42] {
		t.Fatalf("runtime renewal mutated durable Current: before=%+v after=%+v", before.Entries[42], after.Entries[42])
	}
	effective, err := overlay.Effective(after.Entries[42], now.Add(20*time.Second))
	if err != nil || !effective.LeaseExpiresAt.Equal(now.Add(50*time.Second)) {
		t.Fatalf("effective expiry=%v err=%v", effective.LeaseExpiresAt, err)
	}
}

func TestRuntimeLeaseOverlayFailsClosedForPreparingOrBindingMismatch(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	routes, _ := NewLocalMap(now, 30*time.Second)
	snapshot := routes.Snapshot()
	overlay, err := NewRuntimeLeaseOverlay(snapshot, now, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	preparing := snapshot.Entries[42]
	preparing.State = RouteStatePreparing
	preparing.PreviousOwnerZoneID = preparing.OwnerZoneID
	preparing.OwnerZoneID = "zone-next"
	preparing.OwnerEndpoint = "http://zone-next:8082"
	preparing.OwnerEpoch++
	preparing.RouteVersion++
	preparing.TransitionID = "11112222-3333-4444-8555-666677778888"
	if _, err := overlay.Effective(preparing, now); !errors.Is(err, ErrRuntimeLeaseUnavailable) {
		t.Fatalf("PREPARING effective lease error=%v", err)
	}
	mismatch := snapshot.Entries[42]
	mismatch.RouteVersion++
	if _, err := overlay.Effective(mismatch, now); !errors.Is(err, ErrRuntimeLeaseUnavailable) {
		t.Fatalf("binding mismatch error=%v", err)
	}
}
