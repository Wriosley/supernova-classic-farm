package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/gateway"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/config"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/health"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/logging"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/shutdown"
)

func main() {
	if err := run(); err != nil {
		slog.Error("gate service stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load("gate", "127.0.0.1:8081")
	if err != nil {
		return err
	}
	if err := requireLoopbackAddress(cfg.HTTPAddress); err != nil {
		return err
	}
	logger, err := logging.New(cfg.ServiceName, cfg.Environment, cfg.LogLevel)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	clientConfigURL := envOr("CLIENT_CONFIG_URL", gateway.DefaultConfigURL)
	configSHA, err := configuredSHA(client, clientConfigURL)
	if err != nil {
		return err
	}
	routeSource := &gateway.HTTPRouteResolver{
		Client: client, BaseURL: envOr("COORDINATOR_URL", "http://127.0.0.1:8083"),
	}
	routeCache, err := gateway.NewCachedRouteResolver(routeSource, time.Now)
	if err != nil {
		return err
	}
	warmCtx, warmCancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = routeCache.Warm(warmCtx)
	warmCancel()
	if err != nil {
		return fmt.Errorf("warm route cache: %w", err)
	}
	wsHandler, err := gateway.NewHandler(gateway.Config{
		Tickets: &gateway.HTTPTicketConsumer{
			Client: client, Endpoint: envOr("LOGIN_TICKET_CONSUME_URL", "http://127.0.0.1:8080/internal/v1/ws-tickets/consume"),
			GatewayID: gateway.DefaultGatewayID,
		},
		Routes:          routeCache,
		Zone:            &gateway.HTTPZoneCommander{Client: client},
		ClientConfigURL: clientConfigURL,
		ClientConfigSHA: configSHA,
	})
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.Handle("GET /ws", wsHandler)
	mux.Handle("POST /internal/v1/player-state-changes", wsHandler.PushHandler())
	healthHandler := health.NewHandler()
	mux.Handle("GET /livez", healthHandler)
	mux.Handle("GET /readyz", healthHandler)
	server := &http.Server{
		Addr: cfg.HTTPAddress, Handler: mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	logger.Info("gate listening",
		"address", cfg.HTTPAddress,
		"gateway_id", gateway.DefaultGatewayID,
		"max_message_bytes", gateway.MaxMessageBytes,
		"production_backpressure", false,
		"distributed_connection_revocation", false,
	)
	ctx, cancel := shutdown.SignalContext(context.Background())
	defer cancel()
	return shutdown.Serve(ctx, server, cfg.ShutdownTimeout, logger)
}

func configuredSHA(client *http.Client, clientConfigURL string) ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv("CLIENT_CONFIG_SHA256_HEX"))
	if raw != "" {
		digest, err := hex.DecodeString(raw)
		if err != nil || len(digest) != sha256.Size {
			return nil, fmt.Errorf("CLIENT_CONFIG_SHA256_HEX must encode exactly %d bytes", sha256.Size)
		}
		return digest, nil
	}

	response, err := client.Get(clientConfigURL)
	if err != nil {
		return nil, fmt.Errorf("download client config for digest: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download client config for digest: %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("read client config for digest: %w", err)
	}
	if len(body) > 2<<20 {
		return nil, errors.New("client config exceeds 2 MiB")
	}
	digest := sha256.Sum256(body)
	return digest[:], nil
}

func requireLoopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("development GateSvr must bind an explicit loopback address")
	}
	return nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
