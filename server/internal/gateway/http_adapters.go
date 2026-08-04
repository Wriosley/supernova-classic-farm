package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type HTTPTicketConsumer struct {
	Client    *http.Client
	Endpoint  string
	GatewayID string
}

func (c *HTTPTicketConsumer) Consume(ctx context.Context, ticket string) (uint64, error) {
	body, err := json.Marshal(struct {
		Ticket    string `json:"ticket"`
		GatewayID string `json:"gateway_id"`
	}{Ticket: ticket, GatewayID: valueOr(c.GatewayID, DefaultGatewayID)})
	if err != nil {
		return 0, err
	}
	endpoint := valueOr(c.Endpoint, "http://127.0.0.1:8080/internal/v1/ws-tickets/consume")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := clientOrDefault(c.Client).Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return 0, fmt.Errorf("ticket consume returned %s", response.Status)
	}
	var result struct {
		PlayerID string `json:"player_id"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4097))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return 0, fmt.Errorf("decode ticket response: %w", err)
	}
	playerID, err := strconv.ParseUint(result.PlayerID, 10, 64)
	if err != nil || playerID == 0 {
		return 0, errors.New("ticket response has invalid player_id")
	}
	return playerID, nil
}

type HTTPRouteResolver struct {
	Client  *http.Client
	BaseURL string
}

func (r *HTTPRouteResolver) Resolve(ctx context.Context, shardID uint32) (Route, error) {
	base := strings.TrimRight(valueOr(r.BaseURL, "http://127.0.0.1:8083"), "/")
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet,
		base+"/internal/v1/routes/"+strconv.FormatUint(uint64(shardID), 10),
		nil,
	)
	if err != nil {
		return Route{}, err
	}
	response, err := clientOrDefault(r.Client).Do(request)
	if err != nil {
		return Route{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return Route{}, fmt.Errorf("route lookup returned %s", response.Status)
	}
	var result httpRouteResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, 16<<10))
	if err := decoder.Decode(&result); err != nil {
		return Route{}, fmt.Errorf("decode route response: %w", err)
	}
	route, err := result.route()
	if err != nil || route.ShardID != shardID {
		return Route{}, errors.New("route is not a routable ACTIVE owner")
	}
	return route, nil
}

func (r *HTTPRouteResolver) LoadSnapshot(ctx context.Context) (RouteSnapshot, error) {
	base := strings.TrimRight(valueOr(r.BaseURL, "http://127.0.0.1:8083"), "/")
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, base+"/internal/v1/routes", nil,
	)
	if err != nil {
		return RouteSnapshot{}, err
	}
	response, err := clientOrDefault(r.Client).Do(request)
	if err != nil {
		return RouteSnapshot{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return RouteSnapshot{}, fmt.Errorf("route snapshot returned %s", response.Status)
	}
	var result struct {
		ShardCount                 uint32              `json:"shard_count"`
		HashAlgorithmVersion       uint32              `json:"hash_algorithm_version"`
		AssignmentAlgorithmVersion uint32              `json:"assignment_algorithm_version"`
		MapVersion                 string              `json:"map_version"`
		Entries                    []httpRouteResponse `json:"entries"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, (8<<20)+1))
	if err := decoder.Decode(&result); err != nil {
		return RouteSnapshot{}, fmt.Errorf("decode route snapshot: %w", err)
	}
	if result.ShardCount != routing.ShardCount ||
		result.HashAlgorithmVersion != routing.HashAlgorithmVersion ||
		result.AssignmentAlgorithmVersion != routing.AssignmentAlgorithmVersion ||
		len(result.Entries) != int(routing.ShardCount) {
		return RouteSnapshot{}, errors.New("route snapshot metadata is incompatible")
	}
	mapVersion, err := strconv.ParseUint(result.MapVersion, 10, 64)
	if err != nil || mapVersion == 0 {
		return RouteSnapshot{}, errors.New("route snapshot map_version is invalid")
	}
	routes := make([]Route, len(result.Entries))
	for index, entry := range result.Entries {
		route, routeErr := entry.route()
		if routeErr != nil || route.ShardID != uint32(index) ||
			route.MapVersion != mapVersion {
			return RouteSnapshot{}, fmt.Errorf("route snapshot entry %d is invalid", index)
		}
		routes[index] = route
	}
	return RouteSnapshot{MapVersion: mapVersion, Routes: routes}, nil
}

type httpRouteResponse struct {
	ShardID          uint32             `json:"shard_id"`
	OwnerZoneID      string             `json:"owner_zone_id"`
	OwnerEndpoint    string             `json:"owner_endpoint"`
	OwnerEpoch       string             `json:"owner_epoch"`
	RouteVersion     string             `json:"route_version"`
	MapVersion       string             `json:"map_version"`
	State            routing.RouteState `json:"state"`
	LeaseExpiresAtMS int64              `json:"lease_expires_at_ms"`
	Routable         bool               `json:"routable"`
}

func (r httpRouteResponse) route() (Route, error) {
	epoch, epochErr := strconv.ParseUint(r.OwnerEpoch, 10, 64)
	routeVersion, routeErr := strconv.ParseUint(r.RouteVersion, 10, 64)
	mapVersion, mapErr := strconv.ParseUint(r.MapVersion, 10, 64)
	if epochErr != nil || routeErr != nil || mapErr != nil ||
		epoch == 0 || routeVersion == 0 || mapVersion == 0 ||
		r.State != routing.RouteStateActive || !r.Routable ||
		r.OwnerZoneID == "" || r.OwnerEndpoint == "" ||
		r.LeaseExpiresAtMS <= 0 {
		return Route{}, errors.New("route is not a routable ACTIVE owner")
	}
	if err := validateLoopbackHTTPURL(r.OwnerEndpoint); err != nil {
		return Route{}, fmt.Errorf("invalid owner endpoint: %w", err)
	}
	return Route{
		ShardID: r.ShardID, OwnerZoneID: r.OwnerZoneID,
		OwnerEpoch: epoch, RouteVersion: routeVersion, MapVersion: mapVersion,
		LeaseExpiresAt: time.UnixMilli(r.LeaseExpiresAtMS).UTC(),
		OwnerEndpoint:  r.OwnerEndpoint,
	}, nil
}

type HTTPZoneCommander struct {
	Client *http.Client
}

func (z *HTTPZoneCommander) Command(ctx context.Context, route Route, caller uint64, body []byte) ([]byte, error) {
	endpoint := strings.TrimRight(route.OwnerEndpoint, "/") + "/internal/v1/command"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-protobuf")
	request.Header.Set("X-Caller-Player-ID", strconv.FormatUint(caller, 10))
	request.Header.Set("X-Shard-ID", strconv.FormatUint(uint64(route.ShardID), 10))
	request.Header.Set("X-Owner-Zone-ID", route.OwnerZoneID)
	request.Header.Set("X-Owner-Epoch", strconv.FormatUint(route.OwnerEpoch, 10))
	request.Header.Set("X-Route-Version", strconv.FormatUint(route.RouteVersion, 10))
	response, err := clientOrDefault(z.Client).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, ErrNotOwner
	}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("Zone returned %s", response.Status)
	}
	body, err = io.ReadAll(io.LimitReader(response.Body, MaxMessageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxMessageBytes {
		return nil, errors.New("Zone response exceeds 64 KiB")
	}
	return body, nil
}

func clientOrDefault(client *http.Client) *http.Client {
	if client == nil {
		return http.DefaultClient
	}
	return client
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func validateLoopbackHTTPURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() == "" {
		return errors.New("must be an HTTP URL")
	}
	host := parsed.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return errors.New("must use a loopback host")
	}
	return nil
}
