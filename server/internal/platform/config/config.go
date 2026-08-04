// Package config loads process configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultEnvironment     = "development"
	defaultLogLevel        = "info"
	defaultShutdownTimeout = 10 * time.Second
)

// Config contains settings shared by all backend processes.
type Config struct {
	ServiceName     string
	Environment     string
	HTTPAddress     string
	LogLevel        string
	MySQLDSN        string
	ShutdownTimeout time.Duration
}

// Load reads shared settings. The supplied service name and address remain the
// defaults when their environment variables are not set.
func Load(serviceName, defaultHTTPAddress string) (Config, error) {
	cfg := Config{
		ServiceName:     value("SERVICE_NAME", serviceName),
		Environment:     value("APP_ENV", defaultEnvironment),
		HTTPAddress:     value("HTTP_ADDRESS", defaultHTTPAddress),
		LogLevel:        strings.ToLower(value("LOG_LEVEL", defaultLogLevel)),
		MySQLDSN:        os.Getenv("MYSQL_DSN"),
		ShutdownTimeout: defaultShutdownTimeout,
	}

	if cfg.ServiceName == "" {
		return Config{}, errors.New("service name is required")
	}
	if cfg.HTTPAddress == "" {
		return Config{}, errors.New("HTTP address is required")
	}
	if !validLogLevel(cfg.LogLevel) {
		return Config{}, fmt.Errorf("invalid LOG_LEVEL %q", cfg.LogLevel)
	}

	if raw := os.Getenv("SHUTDOWN_TIMEOUT"); raw != "" {
		timeout, err := time.ParseDuration(raw)
		if err != nil || timeout <= 0 {
			return Config{}, fmt.Errorf("invalid SHUTDOWN_TIMEOUT %q", raw)
		}
		cfg.ShutdownTimeout = timeout
	}

	portKey := strings.ToUpper(strings.ReplaceAll(serviceName, "-", "_")) + "_PORT"
	rawPort := os.Getenv(portKey)
	if rawPort == "" {
		rawPort = os.Getenv("HTTP_PORT")
	}
	if raw := rawPort; raw != "" {
		port, err := strconv.ParseUint(raw, 10, 16)
		if err != nil || port == 0 {
			return Config{}, fmt.Errorf("invalid %s %q", portKey, raw)
		}
		cfg.HTTPAddress = fmt.Sprintf(":%d", port)
	}

	return cfg, nil
}

func value(key, fallback string) string {
	if raw, ok := os.LookupEnv(key); ok {
		if trimmed := strings.TrimSpace(raw); trimmed != "" {
			return trimmed
		}
	}
	return fallback
}

func validLogLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}
