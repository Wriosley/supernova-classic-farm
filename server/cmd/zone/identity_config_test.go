package main

import "testing"

func TestZoneIdentityConfigUsesStatefulSetTopology(t *testing.T) {
	t.Setenv("CLUSTER_ID", "classic-farm-local")
	t.Setenv("POD_NAMESPACE", "classic-farm")
	t.Setenv("ZONE_STATEFULSET_NAME", "zone-pool")
	t.Setenv("POD_NAME", "zone-pool-0")
	t.Setenv("ZONE_ADVERTISED_ENDPOINT", "http://zone-pool-0.zone-headless.classic-farm.svc.cluster.local:8082")
	got := zoneIdentityConfig("static-dual-zone", "zone-a", "0.0.0.0:8082")
	if got.LogicalOverride != "" || got.StatefulSetName != "zone-pool" || got.PodName != "zone-pool-0" {
		t.Fatalf("config=%+v", got)
	}
}

func TestZoneIdentityConfigKeepsLegacyLogicalOwner(t *testing.T) {
	got := zoneIdentityConfig("static-dual-zone", "zone-b", "0.0.0.0:8082")
	if got.LogicalOverride != "zone-b" || got.Endpoint != "http://zone-b:8082" {
		t.Fatalf("config=%+v", got)
	}
}
