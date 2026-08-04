// Coordinator is the local single-node, Coordinator-compatible control plane.
// It intentionally does not implement consensus or high availability.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/config"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/health"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/logging"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/shutdown"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

const defaultLeaseDuration = 30 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("coordinator stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load("coordinator", "127.0.0.1:8083")
	if err != nil {
		return err
	}
	cfg.HTTPAddress, err = loopbackAddress(cfg.HTTPAddress)
	if err != nil {
		return err
	}
	logger, err := logging.New(cfg.ServiceName, cfg.Environment, cfg.LogLevel)
	if err != nil {
		return err
	}
	leaseDuration, err := leaseDurationFromEnvironment()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	routes, err := routing.NewLocalMap(now, leaseDuration)
	if err != nil {
		return err
	}

	ctx, cancel := shutdown.SignalContext(context.Background())
	defer cancel()
	go renewLocalLeases(ctx, routes, leaseDuration, logger)

	mux := http.NewServeMux()
	mux.Handle("/internal/v1/routes/", routing.NewHTTPHandler(routes, time.Now))
	mux.Handle("/", health.NewHandler(health.Check{
		Name: "shard_map",
		Run: func(context.Context) error {
			snapshot := routes.Snapshot()
			if len(snapshot.Entries) != int(routing.ShardCount) {
				return fmt.Errorf("expected %d routes, got %d", routing.ShardCount, len(snapshot.Entries))
			}
			_, err := routes.Route(0, time.Now())
			return err
		},
	}))

	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	logger.Info(
		"single-node coordinator listening",
		"address", cfg.HTTPAddress,
		"shard_count", routing.ShardCount,
		"zone_id", routing.DefaultZoneID,
		"zone_endpoint", routing.DefaultZoneEndpoint,
		"lease_duration", leaseDuration.String(),
		"consensus", false,
		"high_availability", false,
	)
	return shutdown.Serve(ctx, server, cfg.ShutdownTimeout, logger)
}

func renewLocalLeases(
	ctx context.Context,
	routes *routing.Map,
	leaseDuration time.Duration,
	logger *slog.Logger,
) {
	ticker := time.NewTicker(leaseDuration / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			renewed, err := routes.RenewOwnedLeases(
				routing.DefaultZoneID,
				now.UTC(),
				leaseDuration,
			)
			if err != nil {
				logger.Error("local lease renewal failed", "error", err)
				continue
			}
			logger.Debug("local leases renewed", "route_count", renewed)
		}
	}
}

func leaseDurationFromEnvironment() (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv("COORDINATOR_LEASE_DURATION"))
	if raw == "" {
		return defaultLeaseDuration, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration < 3*time.Millisecond {
		return 0, fmt.Errorf("invalid COORDINATOR_LEASE_DURATION %q", raw)
	}
	return duration, nil
}

func loopbackAddress(address string) (string, error) {
	if strings.HasPrefix(address, ":") {
		return "127.0.0.1" + address, nil
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("invalid Coordinator HTTP address %q: %w", address, err)
	}
	if port == "" {
		return "", errors.New("Coordinator HTTP port is required")
	}
	if strings.EqualFold(host, "localhost") {
		return address, nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("Coordinator HTTP address must be loopback, got %q", address)
	}
	return address, nil
}
