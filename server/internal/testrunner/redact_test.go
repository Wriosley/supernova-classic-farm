package testrunner

import (
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	cfg := MySQLConfig{
		User:     "classicfarm",
		Password: "s3cret!",
		Host:     "127.0.0.1",
		Port:     3306,
		Database: "classicfarm",
	}
	dsn, err := cfg.BuildDSN()
	if err != nil {
		t.Fatal(err)
	}
	input := "using " + dsn + " password=s3cret! and classicfarm:s3cret!@tcp(127.0.0.1:3306)/classicfarm"
	got := RedactSecrets(input, cfg)
	if strings.Contains(got, "s3cret!") || strings.Contains(got, dsn) {
		t.Fatalf("secrets leaked: %q", got)
	}
	if !strings.Contains(got, "[REDACTED_DSN]") && !strings.Contains(got, "[REDACTED_PASSWORD]") {
		t.Fatalf("expected redaction markers, got %q", got)
	}
}
