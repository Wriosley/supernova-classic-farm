package main

import (
	"testing"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/membership"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func TestPlannerConfigDefaultsDisabledAndValidatesInterval(t *testing.T) {
	t.Setenv("COORDINATOR_PLANNER_ENABLED", "")
	t.Setenv("COORDINATOR_PLANNER_INTERVAL", "")
	config, err := plannerConfigFromEnvironment()
	if err != nil || config.Enabled || config.Interval != 30*time.Second {
		t.Fatalf("default config = %+v, err=%v", config, err)
	}

	t.Setenv("COORDINATOR_PLANNER_ENABLED", "1")
	t.Setenv("COORDINATOR_PLANNER_INTERVAL", "45s")
	config, err = plannerConfigFromEnvironment()
	if err != nil || !config.Enabled || config.Interval != 45*time.Second {
		t.Fatalf("enabled config = %+v, err=%v", config, err)
	}

	t.Setenv("COORDINATOR_PLANNER_ENABLED", "true")
	if _, err := plannerConfigFromEnvironment(); err == nil {
		t.Fatal("non-0/1 planner switch accepted")
	}
	t.Setenv("COORDINATOR_PLANNER_ENABLED", "1")
	t.Setenv("COORDINATOR_PLANNER_INTERVAL", "0s")
	if _, err := plannerConfigFromEnvironment(); err == nil {
		t.Fatal("non-positive planner interval accepted")
	}
}

func TestSeedStaticMembershipMakesZonesHealthy(t *testing.T) {
	registry := membership.NewRegistry(time.Now)
	zones := []routing.ZoneCandidate{{ZoneID: "zone-a", Endpoint: "http://a:8082"}, {ZoneID: "zone-b", Endpoint: "http://b:8082"}}
	if err := seedStaticMembership(registry, zones, time.Unix(100, 0)); err != nil {
		t.Fatalf("seedStaticMembership: %v", err)
	}
	snapshot := registry.Snapshot()
	if snapshot.AvailabilityVersion != 2 || len(snapshot.Members) != 2 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	for _, member := range snapshot.Members {
		if member.State != membership.StateHealthy {
			t.Fatalf("member is not healthy: %+v", member)
		}
	}
}
