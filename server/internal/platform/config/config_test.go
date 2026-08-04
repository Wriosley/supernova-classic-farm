package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("SERVICE_NAME", "")
	t.Setenv("APP_ENV", "")
	t.Setenv("HTTP_ADDRESS", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("MYSQL_DSN", "")
	t.Setenv("SHUTDOWN_TIMEOUT", "")
	t.Setenv("HTTP_PORT", "")

	cfg, err := Load("gate", ":8081")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ServiceName != "gate" || cfg.HTTPAddress != ":8081" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("ShutdownTimeout = %s", cfg.ShutdownTimeout)
	}
}

func TestLoadOverridesPortAndTimeout(t *testing.T) {
	t.Setenv("SERVICE_NAME", "zone")
	t.Setenv("APP_ENV", "test")
	t.Setenv("HTTP_ADDRESS", ":9999")
	t.Setenv("HTTP_PORT", "8082")
	t.Setenv("ZONE_PORT", "9082")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("SHUTDOWN_TIMEOUT", "250ms")

	cfg, err := Load("zone", ":8082")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddress != ":9082" || cfg.ShutdownTimeout != 250*time.Millisecond {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}
