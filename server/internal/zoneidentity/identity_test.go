package zoneidentity

import (
	"testing"
)

func TestDeriveLogicalIDGoldenVectors(t *testing.T) {
	tests := []struct {
		ordinal int
		want    string
	}{
		{ordinal: 0, want: "d859cea1-ac5b-5524-bffa-4e542301cd95"},
		{ordinal: 7, want: "5114530c-e228-52a0-8a76-bbdc860d4f58"},
	}
	for _, test := range tests {
		got, err := DeriveLogicalID("classic-farm-local", "classic-farm", "zone-pool", test.ordinal)
		if err != nil || got != test.want {
			t.Fatalf("ordinal %d: got=%q want=%q err=%v", test.ordinal, got, test.want, err)
		}
	}
}

func TestNewKeepsLogicalIDAndChangesIncarnation(t *testing.T) {
	t.Setenv("INTERNAL_NETWORK_MODE", "kubernetes")
	cfg := Config{ClusterID: "classic-farm-local", Namespace: "classic-farm", StatefulSetName: "zone-pool", PodName: "zone-pool-0", Endpoint: "http://zone-pool-0.zone-headless.classic-farm.svc.cluster.local:8082"}
	first, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if first.LogicalZoneID != second.LogicalZoneID || first.IncarnationID == second.IncarnationID {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestNewValidatesTopologyEndpointAndLegacyOverride(t *testing.T) {
	t.Setenv("INTERNAL_NETWORK_MODE", "kubernetes")
	validEndpoint := "http://zone-pool-0.zone-headless.classic-farm.svc.cluster.local:8082"
	for name, cfg := range map[string]Config{
		"missing topology": {Endpoint: validEndpoint},
		"malformed pod":    {ClusterID: "cluster", Namespace: "classic-farm", StatefulSetName: "zone-pool", PodName: "zone-pool-x", Endpoint: validEndpoint},
		"wrong prefix":     {ClusterID: "cluster", Namespace: "classic-farm", StatefulSetName: "zone-pool", PodName: "other-0", Endpoint: validEndpoint},
		"invalid endpoint": {ClusterID: "cluster", Namespace: "classic-farm", StatefulSetName: "zone-pool", PodName: "zone-pool-0", Endpoint: "https://zone-pool-0"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(cfg); err == nil {
				t.Fatal("invalid identity config accepted")
			}
		})
	}
	legacy, err := New(Config{LogicalOverride: "zone-a", Endpoint: "http://zone-a:8082"})
	if err != nil || legacy.LogicalZoneID != "zone-a" || legacy.IncarnationID == "" {
		t.Fatalf("legacy=%+v err=%v", legacy, err)
	}
}

func TestParseOrdinalRejectsAmbiguousNames(t *testing.T) {
	if got, err := ParseOrdinal("zone-pool-17", "zone-pool"); err != nil || got != 17 {
		t.Fatalf("got=%d err=%v", got, err)
	}
	for _, pod := range []string{"zone-pool", "zone-pool-", "zone-pool--1", "other-1"} {
		if _, err := ParseOrdinal(pod, "zone-pool"); err == nil {
			t.Fatalf("pod %q accepted", pod)
		}
	}
}
