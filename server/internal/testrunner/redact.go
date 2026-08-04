package testrunner

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	dsnPattern      = regexp.MustCompile(`(?i)[a-z0-9_.-]+:[^@\s]+@tcp\([^)]+\)/[a-z0-9_.-]+(?:\?[^\s]*)?`)
	userPassPattern = regexp.MustCompile(`(?i)([a-z0-9_.-]+):([^@\s/]+)@`)
)

func RedactSecrets(input string, cfg MySQLConfig) string {
	out := input
	if dsn, err := cfg.BuildDSN(); err == nil && dsn != "" {
		out = strings.ReplaceAll(out, dsn, "[REDACTED_DSN]")
	}
	if cfg.Password != "" {
		out = strings.ReplaceAll(out, cfg.Password, "[REDACTED_PASSWORD]")
		if escaped := url.QueryEscape(cfg.Password); escaped != cfg.Password {
			out = strings.ReplaceAll(out, escaped, "[REDACTED_PASSWORD]")
		}
	}
	out = dsnPattern.ReplaceAllString(out, "[REDACTED_DSN]")
	out = userPassPattern.ReplaceAllString(out, "$1:[REDACTED_PASSWORD]@")
	return out
}
