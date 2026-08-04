package main

import (
	"testing"
	"time"
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
