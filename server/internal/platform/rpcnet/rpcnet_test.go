package rpcnet

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTargetFromEndpointSupportsHeadlessDNS(t *testing.T) {
	got, err := TargetFromEndpoint(" dns:///friend-headless.classic-farm.svc.cluster.local:8085 ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "dns:///friend-headless.classic-farm.svc.cluster.local:8085" {
		t.Fatalf("target = %q", got)
	}
	legacy, err := TargetFromEndpoint("http://127.0.0.1:8085")
	if err != nil || legacy != "127.0.0.1:8085" {
		t.Fatalf("legacy target = %q, %v", legacy, err)
	}
}

func TestTargetFromEndpointRejectsMalformedDNS(t *testing.T) {
	for _, target := range []string{
		"dns:///friend-headless", "dns:///friend:8085/path", "dns://friend:8085", "dns:///friend:8085?x=1",
	} {
		if _, err := TargetFromEndpoint(target); err == nil {
			t.Fatalf("TargetFromEndpoint(%q) succeeded", target)
		}
	}
}

func TestRoundRobinServiceConfigScopesRetryMethods(t *testing.T) {
	config, err := RoundRobinServiceConfig(
		"/classicfarm.friend.v1.FriendService/ListFriends",
		"/classicfarm.friend.v1.FriendService/CheckMutualFriend",
	)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(config), &decoded); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(config, `"round_robin"`) || !strings.Contains(config, `"maxAttempts":2`) ||
		!strings.Contains(config, `"ListFriends"`) || !strings.Contains(config, `"UNAVAILABLE"`) {
		t.Fatalf("unexpected service config: %s", config)
	}
	if _, err := RoundRobinServiceConfig("not-a-full-method"); err == nil {
		t.Fatal("expected invalid method error")
	}
}
