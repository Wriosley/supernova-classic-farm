package internalnet

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

const modeEnvironment = "INTERNAL_NETWORK_MODE"

func KubernetesEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(modeEnvironment)), "kubernetes")
}

func RequireListenAddress(address, service string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid %s HTTP address %q: %w", service, address, err)
	}
	if port == "" {
		return fmt.Errorf("%s HTTP port is required", service)
	}
	if KubernetesEnabled() {
		return nil
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") &&
		(ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf(
			"development %s must bind an explicit loopback address", service,
		)
	}
	return nil
}

func ValidateHTTPURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() == "" {
		return errors.New("must be an HTTP URL")
	}
	if KubernetesEnabled() {
		return nil
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") &&
		(ip == nil || !ip.IsLoopback()) {
		return errors.New("must use a loopback host")
	}
	return nil
}

func RemoteAllowed(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || KubernetesEnabled()
}
