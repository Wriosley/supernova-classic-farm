package main

import (
	"testing"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func TestLoopbackAddress(t *testing.T) {
	tests := []struct {
		input string
		want  string
		ok    bool
	}{
		{input: ":8083", want: "127.0.0.1:8083", ok: true},
		{input: "127.0.0.1:8083", want: "127.0.0.1:8083", ok: true},
		{input: "localhost:8083", want: "localhost:8083", ok: true},
		{input: "[::1]:8083", want: "[::1]:8083", ok: true},
		{input: "0.0.0.0:8083", ok: false},
		{input: "192.0.2.1:8083", ok: false},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := loopbackAddress(test.input)
			if (err == nil) != test.ok {
				t.Fatalf("loopbackAddress(%q) error = %v, want ok=%v", test.input, err, test.ok)
			}
			if got != test.want {
				t.Fatalf("loopbackAddress(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestRoutingConfigurationFromEnvironment(t *testing.T) {
	t.Setenv("ROUTING_MODE", "")
	mode, zones, err := routingConfigurationFromEnvironment()
	if err != nil || mode != routingModeLocal || len(zones) != 1 ||
		zones[0].ZoneID != routing.DefaultZoneID {
		t.Fatalf("default routing config = %q, %+v, %v", mode, zones, err)
	}

	t.Setenv("ROUTING_MODE", routingModeStaticDualZone)
	t.Setenv("ZONE_A_ID", "zone-east")
	t.Setenv("ZONE_A_ENDPOINT", "http://127.0.0.1:9082")
	t.Setenv("ZONE_B_ID", "zone-west")
	t.Setenv("ZONE_B_ENDPOINT", "http://127.0.0.1:9084")
	mode, zones, err = routingConfigurationFromEnvironment()
	if err != nil || mode != routingModeStaticDualZone || len(zones) != 2 ||
		zones[0].ZoneID != "zone-east" || zones[1].ZoneID != "zone-west" {
		t.Fatalf("dual routing config = %q, %+v, %v", mode, zones, err)
	}

	t.Setenv("ROUTING_MODE", "unknown")
	if _, _, err := routingConfigurationFromEnvironment(); err == nil {
		t.Fatal("unsupported routing mode accepted")
	}
}

func TestLeaseDurationFromEnvironment(t *testing.T) {
	t.Setenv("COORDINATOR_LEASE_DURATION", "")
	got, err := leaseDurationFromEnvironment()
	if err != nil || got != defaultLeaseDuration {
		t.Fatalf("default duration = %v, %v", got, err)
	}

	t.Setenv("COORDINATOR_LEASE_DURATION", "45s")
	got, err = leaseDurationFromEnvironment()
	if err != nil || got != 45*time.Second {
		t.Fatalf("configured duration = %v, %v", got, err)
	}

	t.Setenv("COORDINATOR_LEASE_DURATION", "invalid")
	if _, err := leaseDurationFromEnvironment(); err == nil {
		t.Fatal("invalid duration accepted")
	}
}
