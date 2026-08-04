package main

import (
	"reflect"
	"testing"
)

func TestParseConcurrencies(t *testing.T) {
	got, err := parseConcurrencies("100, 1,25,25,10")
	if err != nil {
		t.Fatal(err)
	}
	want := []int{1, 10, 25, 100}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseConcurrencies() = %v, want %v", got, want)
	}
	for _, raw := range []string{"", "0", "129", "x"} {
		if _, err := parseConcurrencies(raw); err == nil {
			t.Fatalf("parseConcurrencies(%q) succeeded", raw)
		}
	}
}

func TestPercentileUsesNearestRank(t *testing.T) {
	values := []int64{100, 200, 300, 400, 500}
	for percent, want := range map[int]int64{50: 300, 95: 500, 99: 500, 100: 500} {
		if got := percentile(values, percent); got != want {
			t.Fatalf("percentile(%d) = %d, want %d", percent, got, want)
		}
	}
	if got := percentile(nil, 95); got != 0 {
		t.Fatalf("empty percentile = %d, want 0", got)
	}
}

func TestValidRunID(t *testing.T) {
	for _, value := range []string{"r3_20260803", "smoke_no_stack", "a123"} {
		if !validRunID(value) {
			t.Fatalf("validRunID(%q) = false", value)
		}
	}
	for _, value := range []string{"", "Uppercase", "has-dash", "1234567890123456789"} {
		if validRunID(value) {
			t.Fatalf("validRunID(%q) = true", value)
		}
	}
}
