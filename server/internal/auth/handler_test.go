package auth

import (
	"bytes"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	httpv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/http"
	"google.golang.org/protobuf/proto"
)

func TestAuthenticatedCSRFCreatesSessionBoundProof(t *testing.T) {
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(store, HandlerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	initialCSRF := getCSRFForTest(t, client, server.URL)
	registerBody, err := proto.Marshal(&httpv1.RegisterRequest{
		AccountName: "browser_test",
		Password:    "browser-password-2026",
	})
	if err != nil {
		t.Fatal(err)
	}
	registerRequest, err := http.NewRequest(http.MethodPost, server.URL+"/v1/auth/register", bytes.NewReader(registerBody))
	if err != nil {
		t.Fatal(err)
	}
	setBrowserHeaders(registerRequest, initialCSRF)
	registerResponse, err := client.Do(registerRequest)
	if err != nil {
		t.Fatal(err)
	}
	registerResponse.Body.Close()
	if registerResponse.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, want %d", registerResponse.StatusCode, http.StatusCreated)
	}

	authenticatedCSRF := getCSRFForTest(t, client, server.URL)
	ticketBody, err := proto.Marshal(&httpv1.WsTicketRequest{
		TicketRequestId: "11111111-1111-4111-8111-111111111111",
		GatewayId:       "local-gateway",
	})
	if err != nil {
		t.Fatal(err)
	}
	ticketRequest, err := http.NewRequest(http.MethodPost, server.URL+"/v1/ws-tickets", bytes.NewReader(ticketBody))
	if err != nil {
		t.Fatal(err)
	}
	setBrowserHeaders(ticketRequest, authenticatedCSRF)
	ticketResponse, err := client.Do(ticketRequest)
	if err != nil {
		t.Fatal(err)
	}
	ticketResponse.Body.Close()
	if ticketResponse.StatusCode != http.StatusCreated {
		t.Fatalf("ticket status = %d, want %d", ticketResponse.StatusCode, http.StatusCreated)
	}
}

func getCSRFForTest(t *testing.T, client *http.Client, serverURL string) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, serverURL+"/v1/auth/csrf", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", protobufMediaType)
	request.Header.Set("Origin", "http://localhost:5173")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("csrf status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var csrf httpv1.CsrfResponse
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := proto.Unmarshal(body, &csrf); err != nil {
		t.Fatal(err)
	}
	if csrf.CsrfToken == "" {
		t.Fatal("empty CSRF token")
	}
	return csrf.CsrfToken
}

func setBrowserHeaders(request *http.Request, csrf string) {
	request.Header.Set("Accept", protobufMediaType)
	request.Header.Set("Content-Type", protobufMediaType)
	request.Header.Set("Origin", "http://localhost:5173")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("X-CSRF-Token", csrf)
}
