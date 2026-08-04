// Package health provides standard liveness and readiness HTTP handlers.
package health

import (
	"context"
	"encoding/json"
	"net/http"
)

// Check is a named readiness dependency check.
type Check struct {
	Name string
	Run  func(context.Context) error
}

// NewHandler exposes GET /livez and GET /readyz.
func NewHandler(checks ...Check) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		results := make(map[string]string, len(checks))
		status := http.StatusOK
		for _, check := range checks {
			if err := check.Run(r.Context()); err != nil {
				results[check.Name] = "unavailable"
				status = http.StatusServiceUnavailable
				continue
			}
			results[check.Name] = "ready"
		}

		state := "ready"
		if status != http.StatusOK {
			state = "not_ready"
		}
		writeJSON(w, status, struct {
			Status string            `json:"status"`
			Checks map[string]string `json:"checks,omitempty"`
		}{Status: state, Checks: results})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
