package main

import (
	"net/http"
	"testing"
	"time"
)

func TestWebsocketOriginPattern(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "IPv4 and port", raw: "http://192.168.255.10:1616", want: "192.168.255.10:1616"},
		{name: "HTTPS domain", raw: "https://farm.example.com", want: "farm.example.com"},
		{name: "reject path", raw: "http://localhost:1616/farm", wantErr: true},
		{name: "reject websocket scheme", raw: "ws://localhost:1616", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := websocketOriginPattern(test.raw)
			if test.wantErr {
				if err == nil {
					t.Fatalf("websocketOriginPattern(%q) succeeded with %q", test.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("websocketOriginPattern(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestInternalHTTPClientRetiresConnectionsBeforeZone(t *testing.T) {
	client := newInternalHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", client.Transport)
	}
	if client.Timeout != 5*time.Second {
		t.Fatalf("client timeout = %s", client.Timeout)
	}
	if transport.MaxIdleConnsPerHost < 10 {
		t.Fatalf("MaxIdleConnsPerHost = %d, want at least benchmark concurrency", transport.MaxIdleConnsPerHost)
	}
	if transport.IdleConnTimeout <= 0 || transport.IdleConnTimeout >= 30*time.Second {
		t.Fatalf("IdleConnTimeout = %s, want less than Zone 30s", transport.IdleConnTimeout)
	}
}
