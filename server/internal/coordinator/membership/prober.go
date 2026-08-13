package membership

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const maxIdentityBytes = 4 << 10

var canonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type ProbeResult struct {
	LogicalZoneID string
	IncarnationID string
	Endpoint      string
	Live          bool
	ObservedAt    time.Time
	Err           error
}

type Prober interface {
	Probe(ctx context.Context, endpoint string) ProbeResult
}

type HTTPProber struct {
	timeout time.Duration
	client  *http.Client
}

func NewHTTPProber(timeout time.Duration) *HTTPProber {
	return &HTTPProber{timeout: timeout, client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (prober *HTTPProber) Probe(ctx context.Context, endpoint string) ProbeResult {
	result := ProbeResult{ObservedAt: time.Now().UTC()}
	if prober.timeout <= 0 {
		result.Err = errors.New("probe timeout must be positive")
		return result
	}
	discovered, err := normalizeProbeEndpoint(endpoint)
	if err != nil {
		result.Err = err
		return result
	}
	identityBody, err := prober.get(ctx, discovered+"/internal/v1/zone-identity", maxIdentityBytes)
	if err != nil {
		result.Err = fmt.Errorf("probe Zone identity: %w", err)
		return result
	}
	var identity struct {
		LogicalZoneID string `json:"logical_zone_id"`
		IncarnationID string `json:"incarnation_id"`
		Endpoint      string `json:"endpoint"`
	}
	if err := json.Unmarshal(identityBody, &identity); err != nil {
		result.Err = errors.New("decode Zone identity")
		return result
	}
	if !canonicalUUID.MatchString(identity.IncarnationID) ||
		(!canonicalUUID.MatchString(identity.LogicalZoneID) && identity.LogicalZoneID != "zone-a" && identity.LogicalZoneID != "zone-b") {
		result.Err = errors.New("Zone identity is not canonical")
		return result
	}
	advertised, err := normalizeProbeEndpoint(identity.Endpoint)
	if err != nil || advertised != discovered {
		result.Err = errors.New("advertised Zone endpoint does not match discovery")
		return result
	}
	if _, err := prober.get(ctx, discovered+"/livez", 1024); err != nil {
		result.Err = fmt.Errorf("probe Zone liveness: %w", err)
		return result
	}
	result.LogicalZoneID, result.IncarnationID, result.Endpoint, result.Live = identity.LogicalZoneID, identity.IncarnationID, advertised, true
	return result
}

func (prober *HTTPProber) get(parent context.Context, target string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, prober.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	response, err := prober.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP status %d", response.StatusCode)
	}
	reader := io.LimitReader(response.Body, limit+1)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("HTTP response exceeds limit")
	}
	return body, nil
}

func normalizeProbeEndpoint(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("invalid Zone endpoint")
	}
	parsed.Path = ""
	return parsed.String(), nil
}
