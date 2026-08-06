// Package rpcnet contains shared networking helpers for internal gRPC.
package rpcnet

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/internalnet"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
)

func TargetFromHTTPURL(raw string) (string, error) {
	if err := internalnet.ValidateHTTPURL(raw); err != nil {
		return "", err
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("internal gRPC endpoint must be an HTTP origin")
	}
	return parsed.Host, nil
}

func H2CHandler(grpcServer *grpc.Server, fallback http.Handler) http.Handler {
	return h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 &&
			strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			grpcServer.ServeHTTP(w, r)
			return
		}
		fallback.ServeHTTP(w, r)
	}), &http2.Server{})
}
