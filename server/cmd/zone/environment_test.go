package main

import "testing"

func TestEnvironmentBool(t *testing.T) {
	t.Setenv("ZONE_QUICK_INFO_ENABLED", "")
	if enabled, err := environmentBool("ZONE_QUICK_INFO_ENABLED", true); err != nil || !enabled {
		t.Fatalf("default enabled=%v err=%v", enabled, err)
	}
	t.Setenv("ZONE_QUICK_INFO_ENABLED", "false")
	if enabled, err := environmentBool("ZONE_QUICK_INFO_ENABLED", true); err != nil || enabled {
		t.Fatalf("disabled enabled=%v err=%v", enabled, err)
	}
	t.Setenv("ZONE_QUICK_INFO_ENABLED", "not-a-bool")
	if _, err := environmentBool("ZONE_QUICK_INFO_ENABLED", true); err == nil {
		t.Fatal("invalid boolean accepted")
	}
}
