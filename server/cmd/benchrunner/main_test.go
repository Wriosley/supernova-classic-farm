package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
)

func TestAccountNamesForRunReadsCSV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.csv")
	if err := os.WriteFile(path, []byte("account_name,player_id\nbench_a,1\nbench_b,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	names, err := accountNamesForRun(options{accountFile: path}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"bench_a", "bench_b"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
}

func TestAccountNamesForRunRejectsTooFewAccountsForPool(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.csv")
	if err := os.WriteFile(path, []byte("account_name,player_id\nonly_one,1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := accountNamesForRun(options{accountFile: path}, 2)
	if err == nil || !strings.Contains(err.Error(), "need 2") {
		t.Fatalf("error = %v, want insufficient account error", err)
	}
}

func TestWriteIdentityCSVContainsNoCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identities.csv")
	clients := []*benchClient{{accountName: "bench_a", playerID: 42}}
	if err := writeIdentityCSV(path, clients); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "account_name,player_id,shard_id\nbench_a,42,"
	if len(body) < len(want) || string(body[:len(want)]) != want {
		t.Fatalf("identity CSV = %q", body)
	}
}

func TestHTTPStatusErrorIncludesSafeDiagnosticContext(t *testing.T) {
	err := (&httpStatusError{
		method: "POST", endpoint: "/v1/ws-tickets", requestID: "request-123",
		status: 500, code: "501",
	}).Error()
	want := "POST /v1/ws-tickets: unexpected HTTP status=500 error=501 request_id=request-123"
	if err != want {
		t.Fatalf("Error() = %q, want %q", err, want)
	}
}

func TestParseConcurrencies(t *testing.T) {
	got, err := parseConcurrencies("100, 1,25,25,10")
	if err != nil {
		t.Fatal(err)
	}
	want := []int{1, 10, 25, 100}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseConcurrencies() = %v, want %v", got, want)
	}
	for _, raw := range []string{"", "0", "20001", "x"} {
		if _, err := parseConcurrencies(raw); err == nil {
			t.Fatalf("parseConcurrencies(%q) succeeded", raw)
		}
	}
}

func TestParseTargetQPSs(t *testing.T) {
	got, err := parseTargetQPSs("4000, 2000,2000")
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{2000, 4000}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTargetQPSs() = %v, want %v", got, want)
	}
	empty, err := parseTargetQPSs("")
	if err != nil || empty != nil {
		t.Fatalf("empty parseTargetQPSs = %v, %v", empty, err)
	}
	for _, raw := range []string{"0", "-1", "x"} {
		if _, err := parseTargetQPSs(raw); err == nil {
			t.Fatalf("parseTargetQPSs(%q) succeeded", raw)
		}
	}
}

func TestParseAndSelectLoginURLs(t *testing.T) {
	urls, err := parseLoginURLs("http://127.0.0.1:18080/, http://127.0.0.1:18081")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"http://127.0.0.1:18080", "http://127.0.0.1:18081"}
	if !reflect.DeepEqual(urls, want) {
		t.Fatalf("parseLoginURLs() = %v, want %v", urls, want)
	}
	first := selectLoginURL(urls, "bench_user_001")
	if second := selectLoginURL(urls, "bench_user_001"); second != first {
		t.Fatalf("selection changed: %q then %q", first, second)
	}
	if _, err := parseLoginURLs("127.0.0.1:18080"); err == nil {
		t.Fatal("relative Login URL succeeded")
	}
}

func TestParseGateURLs(t *testing.T) {
	urls, err := parseGateURLs("ws://127.0.0.1:18181/ws, wss://gate.example/ws")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ws://127.0.0.1:18181/ws", "wss://gate.example/ws"}
	if !reflect.DeepEqual(urls, want) {
		t.Fatalf("parseGateURLs() = %v, want %v", urls, want)
	}
	if empty, err := parseGateURLs(""); err != nil || empty != nil {
		t.Fatalf("empty parseGateURLs = %v, %v", empty, err)
	}
	if _, err := parseGateURLs("http://127.0.0.1:8081/ws"); err == nil {
		t.Fatal("HTTP Gate URL succeeded")
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

func TestAssignPayloadSupportsExitFriendFarm(t *testing.T) {
	envelope := &wsv1.WsEnvelope{}
	payload := &wsv1.WsEnvelope_ExitFriendFarmRequest{
		ExitFriendFarmRequest: &wsv1.ExitFriendFarmRequest{OwnerPlayerId: 42, VisitId: []byte("visit")},
	}
	if err := assignPayload(envelope, payload); err != nil {
		t.Fatal(err)
	}
	if envelope.GetExitFriendFarmRequest().GetOwnerPlayerId() != 42 {
		t.Fatalf("payload = %+v", envelope.GetExitFriendFarmRequest())
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
