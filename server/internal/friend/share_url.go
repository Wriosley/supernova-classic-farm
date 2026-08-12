package friend

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

const (
	defaultPublicWebBaseURL = "http://localhost:5173"
	friendInvitePath        = "/invite/friend"
)

// LoadPublicWebBaseURL reads PUBLIC_WEB_BASE_URL, defaulting to the local Vite
// origin. Trailing slashes are stripped so path joins stay unambiguous.
func LoadPublicWebBaseURL() (string, error) {
	raw := strings.TrimSpace(os.Getenv("PUBLIC_WEB_BASE_URL"))
	if raw == "" {
		raw = defaultPublicWebBaseURL
	}
	return normalizePublicWebBaseURL(raw)
}

func normalizePublicWebBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("PUBLIC_WEB_BASE_URL is invalid: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("PUBLIC_WEB_BASE_URL scheme must be http or https")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("PUBLIC_WEB_BASE_URL host is required")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

// FriendShareURL builds the invite link for an existing friend code. The code
// is query-escaped so odd alphabets never break the URL; validity still comes
// solely from the FriendCode row, not from the link itself.
func FriendShareURL(baseURL, code string) (string, error) {
	base, err := normalizePublicWebBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return "", fmt.Errorf("friend code is required")
	}
	return base + friendInvitePath + "?code=" + url.QueryEscape(trimmed), nil
}
