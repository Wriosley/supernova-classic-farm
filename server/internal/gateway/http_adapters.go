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
	var result struct {
		ShardID       uint32             `json:"shard_id"`
		OwnerEndpoint string             `json:"owner_endpoint"`
		OwnerEpoch    string             `json:"owner_epoch"`
		State         routing.RouteState `json:"state"`
		Routable      bool               `json:"routable"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 16<<10))
	if err := decoder.Decode(&result); err != nil {
		return Route{}, fmt.Errorf("decode route response: %w", err)
	}
	epoch, err := strconv.ParseUint(result.OwnerEpoch, 10, 64)
	if err != nil || epoch == 0 || result.ShardID != shardID ||
		result.State != routing.RouteStateActive || !result.Routable ||
		result.OwnerEndpoint == "" {
		return Route{}, errors.New("route is not a routable ACTIVE owner")
	}
	if err := validateLoopbackHTTPURL(result.OwnerEndpoint); err != nil {
		return Route{}, fmt.Errorf("invalid owner endpoint: %w", err)
	}
	return Route{ShardID: shardID, OwnerEpoch: epoch, OwnerEndpoint: result.OwnerEndpoint}, nil
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
	request.Header.Set("X-Owner-Epoch", strconv.FormatUint(route.OwnerEpoch, 10))
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
