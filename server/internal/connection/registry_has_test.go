package connection

import (
	"testing"
	"time"
)

func TestRegistryHasReportsPresence(t *testing.T) {
	reg := NewRegistry()
	now := time.Unix(1000, 0)
	if reg.Has(7) {
		t.Fatal("empty registry must not report presence")
	}
	if err := reg.Register(PlayerConnection{
		PlayerID: 7, GateID: "gate-a", ConnectionID: "c1", ExpiresAt: now.Add(LeaseTTL),
	}); err != nil {
		t.Fatal(err)
	}
	if !reg.Has(7) {
		t.Fatal("registered player must be present")
	}
	if reg.Has(8) {
		t.Fatal("other player must not be present")
	}
	reg.Unregister(7, "gate-a", "c1")
	if reg.Has(7) {
		t.Fatal("unregistered player must not be present")
	}
}
