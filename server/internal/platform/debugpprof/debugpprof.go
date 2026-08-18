// Package debugpprof mounts Go pprof handlers behind an explicit env switch.
//
// Endpoints are served under /internal/debug/pprof/ so they stay off the public
// client surface (WebSocket /v1). Call Mount only when Enabled() is true.
package debugpprof

import (
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
)

const PathPrefix = "/internal/debug/pprof"

// Enabled reports whether ENABLE_PPROF is truthy ("1", "true", "yes", "on").
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ENABLE_PPROF"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// Mount registers pprof routes on mux. Paths rewrite to the standard
// /debug/pprof/* layout expected by net/http/pprof.Index.
func Mount(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc("GET "+PathPrefix+"/", index)
	mux.HandleFunc("GET "+PathPrefix+"/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET "+PathPrefix+"/profile", pprof.Profile)
	mux.HandleFunc("GET "+PathPrefix+"/symbol", pprof.Symbol)
	mux.HandleFunc("GET "+PathPrefix+"/trace", pprof.Trace)
	for _, name := range []string{
		"allocs", "block", "goroutine", "heap", "mutex", "threadcreate",
	} {
		mux.Handle("GET "+PathPrefix+"/"+name, pprof.Handler(name))
	}
}

// MaybeMount mounts pprof when ENABLE_PPROF is set and logs the outcome.
func MaybeMount(mux *http.ServeMux, logger *slog.Logger) {
	if !Enabled() {
		if logger != nil {
			logger.Info("pprof disabled", "env", "ENABLE_PPROF")
		}
		return
	}
	Mount(mux)
	if logger != nil {
		logger.Info("pprof enabled", "path", PathPrefix+"/")
	}
}

func index(w http.ResponseWriter, r *http.Request) {
	// pprof.Index only auto-dispatches named profiles when Path has the
	// canonical /debug/pprof/ prefix.
	cloned := r.Clone(r.Context())
	urlCopy := *r.URL
	suffix := strings.TrimPrefix(r.URL.Path, PathPrefix)
	urlCopy.Path = "/debug/pprof" + suffix
	cloned.URL = &urlCopy
	pprof.Index(w, cloned)
}
