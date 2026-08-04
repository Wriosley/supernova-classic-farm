package main

import (
	"net/http"
	"testing"
	"time"
)

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
