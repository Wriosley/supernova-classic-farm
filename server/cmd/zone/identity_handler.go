package main

import (
	"encoding/json"
	"net/http"

	"github.com/Wriosley/supernova-classic-farm/server/internal/zoneidentity"
)

func newZoneIdentityHandler(identity zoneidentity.Identity) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(identity)
	})
}
