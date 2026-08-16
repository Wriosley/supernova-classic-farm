// Package rpcnet contains shared networking helpers for internal gRPC.
package rpcnet

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/internalnet"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	_ "google.golang.org/grpc/balancer/roundrobin"
)

const dnsPrefix = "dns:///"

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

// TargetFromEndpoint accepts the historical internal HTTP-origin form and the
// gRPC DNS resolver form used by Kubernetes headless Services.
func TargetFromEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, dnsPrefix) {
		return TargetFromHTTPURL(raw)
	}
	address := strings.TrimPrefix(raw, dnsPrefix)
	if address == "" || strings.ContainsAny(address, "/?#@") {
		return "", errors.New("internal gRPC DNS target must contain only host:port")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		return "", errors.New("internal gRPC DNS target must contain host:port")
	}
	return dnsPrefix + net.JoinHostPort(host, port), nil
}

type serviceConfig struct {
	LoadBalancingConfig []map[string]map[string]any `json:"loadBalancingConfig"`
	MethodConfig        []methodConfig              `json:"methodConfig,omitempty"`
}

type methodConfig struct {
	Name        []methodName `json:"name"`
	RetryPolicy retryPolicy  `json:"retryPolicy"`
}

type methodName struct {
	Service string `json:"service"`
	Method  string `json:"method"`
}

type retryPolicy struct {
	MaxAttempts          int      `json:"maxAttempts"`
	InitialBackoff       string   `json:"initialBackoff"`
	MaxBackoff           string   `json:"maxBackoff"`
	BackoffMultiplier    float64  `json:"backoffMultiplier"`
	RetryableStatusCodes []string `json:"retryableStatusCodes"`
}

// RoundRobinServiceConfig returns a gRPC service config that balances across
// all resolver addresses. Only explicitly listed full RPC method names receive
// one retry, and only for transport-level UNAVAILABLE.
func RoundRobinServiceConfig(retryMethods ...string) (string, error) {
	config := serviceConfig{LoadBalancingConfig: []map[string]map[string]any{{"round_robin": {}}}}
	if len(retryMethods) > 0 {
		names := make([]methodName, 0, len(retryMethods))
		for _, fullMethod := range retryMethods {
			trimmed := strings.TrimPrefix(strings.TrimSpace(fullMethod), "/")
			service, method, ok := strings.Cut(trimmed, "/")
			if !ok || service == "" || method == "" || strings.Contains(method, "/") {
				return "", fmt.Errorf("invalid full gRPC method name %q", fullMethod)
			}
			names = append(names, methodName{Service: service, Method: method})
		}
		config.MethodConfig = []methodConfig{{
			Name: names,
			RetryPolicy: retryPolicy{
				MaxAttempts: 2, InitialBackoff: "0.020s", MaxBackoff: "0.050s",
				BackoffMultiplier: 1.5, RetryableStatusCodes: []string{"UNAVAILABLE"},
			},
		}}
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func RoundRobinDialOption(retryMethods ...string) (grpc.DialOption, error) {
	config, err := RoundRobinServiceConfig(retryMethods...)
	if err != nil {
		return nil, err
	}
	return grpc.WithDefaultServiceConfig(config), nil
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
