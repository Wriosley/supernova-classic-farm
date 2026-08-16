package main

import (
	"testing"
	"time"
)

func TestMembershipConfigDefaultsStaticAndValidatesKubernetes(t *testing.T) {
	t.Setenv("COORDINATOR_MEMBERSHIP_SOURCE", "")
	config, err := membershipConfigFromEnvironment()
	if err != nil || config.Source != "static" {
		t.Fatalf("config=%+v err=%v", config, err)
	}
	t.Setenv("COORDINATOR_MEMBERSHIP_SOURCE", "kubernetes")
	t.Setenv("CLUSTER_ID", "classic-farm-local")
	t.Setenv("POD_NAMESPACE", "classic-farm")
	t.Setenv("ZONE_DISCOVERY_SERVICE", "zone-discovery")
	t.Setenv("COORDINATOR_DRAIN_ZONE_IDS", " zone-a,zone-b,zone-a ")
	config, err = membershipConfigFromEnvironment()
	if err != nil || config.ProbeInterval != 10*time.Second || config.FailureThreshold != 3 || config.Workers != 8 || len(config.DrainingZoneIDs) != 2 {
		t.Fatalf("config=%+v err=%v", config, err)
	}
}

func TestMembershipConfigRejectsInvalidSource(t *testing.T) {
	t.Setenv("COORDINATOR_MEMBERSHIP_SOURCE", "automatic")
	if _, err := membershipConfigFromEnvironment(); err == nil {
		t.Fatal("invalid source accepted")
	}
}
