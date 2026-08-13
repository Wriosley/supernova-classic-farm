package membership

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRegistryApplyVersionsVisibleChangesAndRejectsStaleConflict(t *testing.T) {
	base := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	registry := NewRegistry(func() time.Time { return base })
	first := Observation{LogicalZoneID: "zone-b", IncarnationID: "inc-1", Endpoint: "http://zone-b:8082", Namespace: "classic-farm", PodName: "zone-b-0", PodUID: "uid-1", ResourceVersion: "10", State: StateHealthy, ObservedAt: base}

	snapshot, changed, err := registry.Apply(first)
	if err != nil || !changed || snapshot.AvailabilityVersion != 1 || len(snapshot.Members) != 1 {
		t.Fatalf("first apply = %+v, %v, %v", snapshot, changed, err)
	}
	if snapshot, changed, err = registry.Apply(first); err != nil || changed || snapshot.AvailabilityVersion != 1 {
		t.Fatalf("duplicate = %+v, %v, %v", snapshot, changed, err)
	}

	suspect := first
	suspect.State, suspect.ConsecutiveFailures, suspect.ObservedAt = StateSuspect, 1, base.Add(time.Second)
	if snapshot, changed, err = registry.Apply(suspect); err != nil || !changed || snapshot.AvailabilityVersion != 2 {
		t.Fatalf("suspect = %+v, %v, %v", snapshot, changed, err)
	}
	recovered := suspect
	recovered.State, recovered.ConsecutiveFailures, recovered.ObservedAt = StateHealthy, 0, base.Add(2*time.Second)
	if snapshot, changed, err = registry.Apply(recovered); err != nil || !changed || snapshot.AvailabilityVersion != 3 {
		t.Fatalf("recovered = %+v, %v, %v", snapshot, changed, err)
	}

	stale := first
	stale.ResourceVersion, stale.Endpoint, stale.ObservedAt = "9", "http://stale:8082", base.Add(3*time.Second)
	if _, _, err = registry.Apply(stale); !errors.Is(err, ErrStaleObservation) {
		t.Fatalf("stale error = %v", err)
	}
	conflict := recovered
	conflict.PodUID, conflict.PodName, conflict.ResourceVersion, conflict.ObservedAt = "uid-2", "zone-other-0", "11", base.Add(4*time.Second)
	if _, _, err = registry.Apply(conflict); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("conflict error = %v", err)
	}
}

func TestRegistryCoalescesVisibleChangeNotifications(t *testing.T) {
	base := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	registry := NewRegistry(func() time.Time { return base })
	observation := Observation{LogicalZoneID: "zone-a", IncarnationID: "inc-a", Endpoint: "http://a:8082", PodUID: "uid-a", PodName: "a", ResourceVersion: "1", State: StateHealthy, ObservedAt: base}
	if _, _, err := registry.Apply(observation); err != nil {
		t.Fatal(err)
	}
	observation.State, observation.ObservedAt = StateSuspect, base.Add(time.Second)
	if _, _, err := registry.Apply(observation); err != nil {
		t.Fatal(err)
	}

	select {
	case <-registry.Changes():
	default:
		t.Fatal("visible changes did not notify")
	}
	select {
	case <-registry.Changes():
		t.Fatal("burst was not coalesced")
	default:
	}
	if _, changed, err := registry.Apply(observation); err != nil || changed {
		t.Fatalf("duplicate apply = changed %t err %v", changed, err)
	}
	select {
	case <-registry.Changes():
		t.Fatal("invisible duplicate notified")
	default:
	}
}

func TestRegistryNewIncarnationEndpointOrderingAndImmutableSnapshot(t *testing.T) {
	base := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	registry := NewRegistry(func() time.Time { return base })
	apply := func(observation Observation) Snapshot {
		t.Helper()
		snapshot, _, err := registry.Apply(observation)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	apply(Observation{LogicalZoneID: "zone-b", IncarnationID: "inc-b", Endpoint: "http://b:8082", PodUID: "uid-b", PodName: "b", ResourceVersion: "1", State: StateHealthy, ObservedAt: base})
	snapshot := apply(Observation{LogicalZoneID: "zone-a", IncarnationID: "inc-a", Endpoint: "http://a:8082", PodUID: "uid-a", PodName: "a", ResourceVersion: "1", State: StateHealthy, ObservedAt: base})
	if snapshot.Members[0].LogicalZoneID != "zone-a" || snapshot.Members[1].LogicalZoneID != "zone-b" {
		t.Fatalf("unsorted: %+v", snapshot.Members)
	}
	snapshot.Members[0].Endpoint = "mutated"
	if registry.Snapshot().Members[0].Endpoint == "mutated" {
		t.Fatal("snapshot mutation escaped")
	}

	changedEndpoint := Observation{LogicalZoneID: "zone-a", IncarnationID: "inc-a", Endpoint: "http://a-new:8082", PodUID: "uid-a", PodName: "a", ResourceVersion: "2", State: StateHealthy, ObservedAt: base.Add(time.Second)}
	apply(changedEndpoint)
	changedEndpoint.IncarnationID, changedEndpoint.ResourceVersion, changedEndpoint.ObservedAt = "inc-a-2", "3", base.Add(2*time.Second)
	if got := apply(changedEndpoint).Members[0].IncarnationID; got != "inc-a-2" {
		t.Fatalf("incarnation = %s", got)
	}
}

func TestRegistryConcurrentApplyAndSnapshot(t *testing.T) {
	registry := NewRegistry(time.Now)
	var group sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for iteration := 0; iteration < 100; iteration++ {
				_, _, _ = registry.Apply(Observation{LogicalZoneID: "zone-a", IncarnationID: "inc", Endpoint: "http://a:8082", PodUID: "uid", PodName: "a", ResourceVersion: "1", State: StateHealthy, ObservedAt: time.Now()})
				_ = registry.Snapshot()
			}
		}()
	}
	group.Wait()
}
