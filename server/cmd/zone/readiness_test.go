package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/health"
)

func TestReadinessStateTransitionsWithoutStorageProbe(t *testing.T) {
	state := newReadinessState()
	storageCalls := 0
	handler := health.NewHandler(health.Check{Name: "startup", Run: func(ctx context.Context) error {
		if err := state.Check(ctx); err != nil {
			return err
		}
		return nil
	}})

	assertHealthStatus(t, handler, "/livez", http.StatusOK)
	assertHealthStatus(t, handler, "/readyz", http.StatusServiceUnavailable)
	state.SetReady()
	assertHealthStatus(t, handler, "/readyz", http.StatusOK)
	assertHealthStatus(t, handler, "/readyz", http.StatusOK)
	state.SetNotReady("shutdown")
	assertHealthStatus(t, handler, "/readyz", http.StatusServiceUnavailable)
	if storageCalls != 0 {
		t.Fatalf("storage calls = %d", storageCalls)
	}
}

func assertHealthStatus(t *testing.T, handler http.Handler, path string, want int) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	if recorder.Code != want {
		t.Fatalf("%s status = %d, want %d: %s", path, recorder.Code, want, recorder.Body.String())
	}
}
