package main

import (
	"strings"
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
	t.Setenv("ZONE_C_ID", "")
	mode, zones, err = routingConfigurationFromEnvironment()
	if err != nil || mode != routingModeStaticDualZone || len(zones) != 2 ||
		zones[0].ZoneID != "zone-east" || zones[1].ZoneID != "zone-west" {
		t.Fatalf("dual routing config = %q, %+v, %v", mode, zones, err)
	}

	t.Setenv("ZONE_C_ID", "zone-pool-uuid")
	t.Setenv("ZONE_C_ENDPOINT", "http://zone-pool-0:8082")
	t.Setenv("ZONE_POOL_REPLICAS", "")
	extras := extraMoveTargetsFromEnvironment()
	if len(extras) != 1 || extras[0].ZoneID != "zone-pool-uuid" || extras[0].Endpoint != "http://zone-pool-0:8082" {
		t.Fatalf("extra move targets = %+v", extras)
	}
	mode, zones, err = routingConfigurationFromEnvironment()
	if err != nil || len(zones) != 2 {
		t.Fatalf("ZONE_C must not expand placement zones: %q %+v %v", mode, zones, err)
	}

	t.Setenv("ZONE_C_ID", "")
	t.Setenv("ZONE_POOL_REPLICAS", "2")
	t.Setenv("CLUSTER_ID", "classic-farm-local")
	t.Setenv("POD_NAMESPACE", "classic-farm")
	t.Setenv("ZONE_STATEFULSET_NAME", "zone-pool")
	t.Setenv("ZONE_HEADLESS_SERVICE", "zone-headless")
	extras = extraMoveTargetsFromEnvironment()
	if len(extras) != 2 {
		t.Fatalf("pool extras = %+v", extras)
	}
	if extras[0].ZoneID == extras[1].ZoneID || !strings.Contains(extras[0].Endpoint, "zone-pool-") {
		t.Fatalf("derived pool extras = %+v", extras)
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

func TestRouteBootstrapTimeoutFromEnvironment(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want time.Duration
		ok   bool
	}{
		{raw: "", want: 10 * time.Minute, ok: true},
		{raw: "15m", want: 15 * time.Minute, ok: true},
		{raw: "0s", ok: false},
		{raw: "-1s", ok: false},
		{raw: "invalid", ok: false},
	} {
		t.Setenv("COORDINATOR_ROUTE_BOOTSTRAP_TIMEOUT", test.raw)
		got, err := routeBootstrapTimeoutFromEnvironment()
		if (err == nil) != test.ok || got != test.want {
			t.Fatalf("raw=%q timeout=%v err=%v", test.raw, got, err)
		}
	}
}
