package membership

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPProberValidatesIdentityAndLiveness(t *testing.T) {
	var endpoint string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/internal/v1/zone-identity":
			fmt.Fprintf(writer, `{"logical_zone_id":"d859cea1-ac5b-5524-bffa-4e542301cd95","incarnation_id":"9e398c48-4c67-41e8-8655-d33167d42fb4","endpoint":%q}`, endpoint)
		case "/livez":
			writer.WriteHeader(http.StatusOK)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	endpoint = server.URL
	result := NewHTTPProber(time.Second).Probe(context.Background(), endpoint)
	if result.Err != nil || !result.Live || result.Endpoint != endpoint {
		t.Fatalf("result=%+v", result)
	}
}

func TestHTTPProberRejectsRedirectOversizeAndEndpointMismatch(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"redirect", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/livez", http.StatusFound) }},
		{"oversize", func(w http.ResponseWriter, r *http.Request) {
			for i := 0; i < 5000; i++ {
				_, _ = w.Write([]byte("x"))
			}
		}},
		{"endpoint mismatch", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"logical_zone_id":"d859cea1-ac5b-5524-bffa-4e542301cd95","incarnation_id":"9e398c48-4c67-41e8-8655-d33167d42fb4","endpoint":"http://127.0.0.1:1"}`))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			if result := NewHTTPProber(200*time.Millisecond).Probe(context.Background(), server.URL); result.Err == nil || result.Live {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestHTTPProberHonorsTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { time.Sleep(100 * time.Millisecond) }))
	defer server.Close()
	if result := NewHTTPProber(10*time.Millisecond).Probe(context.Background(), server.URL); result.Err == nil {
		t.Fatal("timeout accepted")
	}
}
