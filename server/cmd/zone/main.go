package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/database"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/health"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/logging"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/shutdown"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

const (
	defaultListenAddress = "127.0.0.1:8082"
	dualRoutingMode      = "static-dual-zone"
)

func main() {
	routingMode := environmentOr("ROUTING_MODE", "local")
	if routingMode != "local" && routingMode != dualRoutingMode {
		log.Fatalf("unsupported ROUTING_MODE %q", routingMode)
	}
	ownerZoneID := routing.DefaultZoneID
	if routingMode == dualRoutingMode {
		ownerZoneID = environmentOr("OWNER_ZONE_ID", "zone-a")
	}
	listenAddress := environmentOr("ZONE_HTTP_ADDRESS", defaultListenAddress)
	if err := requireLoopbackListenAddress(listenAddress); err != nil {
		log.Fatal(err)
	}
	dsn := strings.TrimSpace(os.Getenv("MYSQL_DSN"))
	logger, err := logging.New("zone-"+ownerZoneID, "development", "info")
	if err != nil {
		log.Fatal(err)
	}

	var runtime *player.Runtime
	if dsn == "" {
		runtime = player.NewRuntime()
		logger.Warn("using development-only lazy in-memory player state")
	} else {
		db, openErr := database.OpenMySQL(context.Background(), dsn)
		if openErr != nil {
			log.Fatal(openErr)
		}
		defer db.Close()
		runtime, err = player.NewRuntimeWithStore(&player.MySQLCheckpointStore{
			DB: db, OwnerZoneID: ownerZoneID,
		})
		if err != nil {
			log.Fatal(err)
		}
		logger.Info("using MySQL Player checkpoint store")
	}
	defer runtime.Close()

	ctx, cancel := shutdown.SignalContext(context.Background())
	defer cancel()
	gates := &shardExecutionGates{}
	var authorization ownerAuthorization = localAuthorization{}
	var lifecycle *lifecycleHandler
	if routingMode == dualRoutingMode {
		table, tableErr := routing.NewAuthorizationTable(ownerZoneID)
		if tableErr != nil {
			log.Fatal(tableErr)
		}
		coordinatorURL := environmentOr("COORDINATOR_URL", "http://127.0.0.1:8083")
		client := &http.Client{Timeout: 5 * time.Second}
		if err := refreshAuthorization(ctx, table, client, coordinatorURL); err != nil {
			log.Fatalf("load initial ownership snapshot: %v", err)
		}
		authorization = table
		lifecycle = &lifecycleHandler{
			runtime: runtime, authorization: table, gates: gates, now: time.Now,
			refresh: func() error {
				refreshCtx, refreshCancel := context.WithTimeout(ctx, 3*time.Second)
				defer refreshCancel()
				return refreshAuthorization(refreshCtx, table, client, coordinatorURL)
			},
		}
		go refreshAuthorizationLoop(ctx, table, client, coordinatorURL, logger)
	}

	pushEndpoint := os.Getenv("GATE_PUSH_URL")
	if pushEndpoint == "" {
		pushEndpoint = "http://127.0.0.1:8081/internal/v1/player-state-changes"
	}
	pushForwarder, err := player.NewHTTPPushForwarder(
		&http.Client{Timeout: 2 * time.Second},
		pushEndpoint,
	)
	if err != nil {
		log.Fatal(err)
	}
	if err := runtime.SetPushForwarder(pushForwarder); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("POST /internal/v1/command",
		newOwnedCommandHandlerWithGates(runtime, authorization, gates, time.Now))
	if lifecycle != nil {
		mux.HandleFunc("POST /internal/v1/shards/{shard_id}/drain", lifecycle.drain)
		mux.HandleFunc("POST /internal/v1/shards/{shard_id}/drain-complete", lifecycle.completeDrain)
		mux.HandleFunc("POST /internal/v1/shards/{shard_id}/prepare", lifecycle.prepareMigration)
		mux.HandleFunc("POST /internal/v1/shards/{shard_id}/resume", lifecycle.resume)
		mux.HandleFunc("POST /internal/v1/ownership/refresh", lifecycle.refreshOwnership)
	}
	healthHandler := health.NewHandler()
	mux.Handle("GET /livez", healthHandler)
	mux.Handle("GET /readyz", healthHandler)

	server := &http.Server{
		Addr:              listenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	logger.Info("zone listening",
		"address", listenAddress,
		"owner_zone_id", ownerZoneID,
		"owner_epoch", player.LocalOwnerEpoch,
		"routing_mode", routingMode,
		"gate_push_url", pushEndpoint,
		"state_adapter", func() string {
			if os.Getenv("MYSQL_DSN") == "" {
				return "lazy-in-memory-development-only"
			}
			return "mysql-checkpoint"
		}(),
	)
	if err := shutdown.Serve(ctx, server, 5*time.Second, logger); err != nil {
		logger.Error("zone stopped", "error", err)
	}
}

func refreshAuthorization(
	ctx context.Context,
	table *routing.AuthorizationTable,
	client *http.Client,
	coordinatorURL string,
) error {
	snapshot, err := routing.FetchSnapshot(ctx, client, coordinatorURL)
	if err != nil {
		return err
	}
	return table.Replace(snapshot)
}

func refreshAuthorizationLoop(
	ctx context.Context,
	table *routing.AuthorizationTable,
	client *http.Client,
	coordinatorURL string,
	logger interface {
		Error(string, ...any)
		Debug(string, ...any)
	},
) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := refreshAuthorization(refreshCtx, table, client, coordinatorURL)
			cancel()
			if err != nil {
				logger.Error("ownership snapshot refresh failed", "error", err)
				continue
			}
			logger.Debug("ownership snapshot refreshed")
		}
	}
}

func environmentOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func requireLoopbackListenAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid Zone HTTP address %q: %w", address, err)
	}
	if port == "" {
		return errors.New("Zone HTTP port is required")
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") &&
		(ip == nil || !ip.IsLoopback()) {
		return errors.New("development ZoneSvr must bind an explicit loopback address")
	}
	return nil
}
