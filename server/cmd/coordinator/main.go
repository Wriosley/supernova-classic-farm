// Coordinator is the local single-node, Coordinator-compatible control plane.
// It intentionally does not implement consensus or high availability.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/routestore"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/config"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/database"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/health"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/internalnet"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/logging"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/shutdown"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/tcaplusdb"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

const defaultLeaseDuration = 30 * time.Second

const (
	routingModeLocal          = "local"
	routingModeStaticDualZone = "static-dual-zone"
)

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
	mode, zones, err := routingConfigurationFromEnvironment()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	routeStoreMode, err := validateRouteStoreMode(os.Getenv("COORDINATOR_ROUTE_STORE"), os.Getenv("STORAGE_MODE"))
	if err != nil {
		return err
	}
	routes, err := routing.NewStaticMap(now, leaseDuration, zones)
	if err != nil {
		return err
	}
	var mysqlDB *sql.DB
	var migrations *migrationHandler
	var runtimeLeases *routing.RuntimeLeaseOverlay
	if mode == routingModeStaticDualZone {
		migrations = newMigrationHandler(
			routes, zones, &http.Client{Timeout: 5 * time.Second},
			time.Now, leaseDuration,
		)
		if strings.TrimSpace(os.Getenv("STORAGE_MODE")) == "tcaplus" {
			if strings.TrimSpace(os.Getenv("MYSQL_DSN")) != "" {
				return errors.New("pure Tcaplus mode forbids MYSQL_DSN")
			}
			tcaplusConfig, configErr := tcaplusdb.LoadConfigFromEnv()
			if configErr != nil {
				return configErr
			}
			fenceTable, configErr := tcaplusdb.TableName(
				"TCAPLUS_FENCE_TABLE", "ShardFence",
			)
			if configErr != nil {
				return configErr
			}
			migrationTable, configErr := tcaplusdb.TableName(
				"TCAPLUS_MIGRATION_TABLE", "MigrationProgress",
			)
			if configErr != nil {
				return configErr
			}
			tables := []string{fenceTable, migrationTable}
			var durableStore *routestore.TcaplusStore
			if routeStoreMode == routeStoreTcaplus {
				metaTable, tableErr := tcaplusdb.TableName("TCAPLUS_SHARD_MAP_META_TABLE", "ShardMapMeta")
				if tableErr != nil {
					return tableErr
				}
				routeTable, tableErr := tcaplusdb.TableName("TCAPLUS_SHARD_ROUTE_TABLE", "ShardRoute")
				if tableErr != nil {
					return tableErr
				}
				tables = append(tables, metaTable, routeTable)
			}
			client, openErr := tcaplusdb.Open(
				tcaplusConfig, tables...,
			)
			if openErr != nil {
				return openErr
			}
			defer client.Close()
			control, controlErr := routing.NewTcaplusControlStore(
				client, tcaplusConfig.ZoneID,
			)
			if controlErr != nil {
				return controlErr
			}
			if routeStoreMode == routeStoreTcaplus {
				durableStore, configErr = routestore.NewTcaplusStore(client, tcaplusConfig.ZoneID)
				if configErr != nil {
					return configErr
				}
				candidate := routestore.FromRoutingSnapshot(routes.Snapshot(), now)
				bootstrapCtx, bootstrapCancel := context.WithTimeout(context.Background(), 60*time.Second)
				var created bool
				routes, runtimeLeases, created, configErr = bootstrapDurableCurrent(bootstrapCtx, durableStore, candidate, now, leaseDuration)
				bootstrapCancel()
				if configErr != nil {
					return configErr
				}
				migrations.routes = routes
				migrations.routeStore = durableStore
				migrations.runtimeLeases = runtimeLeases
				logger.Info("durable Current ShardRoute ready", "bootstrapped", created, "map_version", routes.Snapshot().MapVersion)
			}
			fenceCtx, fenceCancel := context.WithTimeout(
				context.Background(), 60*time.Second,
			)
			updated, ensureErr := control.EnsureStaticFences(
				fenceCtx, routes.Snapshot(), now,
			)
			if ensureErr != nil {
				fenceCancel()
				return ensureErr
			}
			fences, loadErr := control.LoadFences(fenceCtx)
			if loadErr != nil {
				fenceCancel()
				return loadErr
			}
			if routeStoreMode == routeStoreLegacyFence {
				if hydrateErr := routing.HydrateActiveRoutesFromFences(routes, fences, zones, now, leaseDuration); hydrateErr != nil {
					fenceCancel()
					return hydrateErr
				}
			}
			fenceCancel()
			logger.Info(
				"dual-Zone Tcaplus fences ready",
				"initialized_or_aligned_shards", updated,
			)
			migrations.tcaplus = control
			migrations.advanceFence = control.AdvanceFence
			overlayCtx, overlayCancel := context.WithTimeout(
				context.Background(), 10*time.Second,
			)
			openCount, overlayErr := migrations.loadOpenProgress(overlayCtx, now)
			overlayCancel()
			if overlayErr != nil {
				return overlayErr
			}
			if openCount > 0 {
				logger.Warn(
					"loaded open PREPARING migrations; fail-closed until continue or abandon",
					"open_migrations", openCount,
				)
			}
			if routeStoreMode == routeStoreTcaplus {
				progress := make(map[uint32]*migrationProgress, openCount)
				for shardID, item := range migrations.progress {
					if item != nil {
						progress[uint32(shardID)] = item
					}
				}
				loaded, loadCurrentErr := durableStore.Load(context.Background())
				if loadCurrentErr != nil {
					return loadCurrentErr
				}
				if reconcileErr := validateCurrentFences(loaded, fences, progress); reconcileErr != nil {
					return reconcileErr
				}
			}
		} else if dsn := strings.TrimSpace(os.Getenv("MYSQL_DSN")); dsn != "" {
			if strings.TrimSpace(os.Getenv("DUAL_ZONE_FENCE_BOOTSTRAP")) != "1" {
				return errors.New(
					"dual-Zone MySQL requires DUAL_ZONE_FENCE_BOOTSTRAP=1",
				)
			}
			db, openErr := database.OpenMySQL(context.Background(), dsn)
			if openErr != nil {
				return openErr
			}
			mysqlDB = db
			defer db.Close()
			fenceCtx, fenceCancel := context.WithTimeout(
				context.Background(), 30*time.Second,
			)
			fences, loadErr := routing.LoadMySQLFences(fenceCtx, db)
			if loadErr != nil {
				fenceCancel()
				return loadErr
			}
			if routing.FencesAreEpochOneBootstrap(fences, routes.Snapshot()) {
				updated, reconcileErr := routing.ReconcileStaticMySQLFences(
					fenceCtx, db, routes.Snapshot(), now,
				)
				fenceCancel()
				if reconcileErr != nil {
					return reconcileErr
				}
				logger.Info(
					"static dual-Zone MySQL fences aligned",
					"updated_shards", updated,
				)
			} else {
				hydrateErr := routing.HydrateActiveRoutesFromFences(
					routes, fences, zones, now, leaseDuration,
				)
				fenceCancel()
				if hydrateErr != nil {
					return hydrateErr
				}
				logger.Info("dual-Zone MySQL routes hydrated from fences")
			}
			migrations.db = mysqlDB
			migrations.advanceFence = func(
				ctx context.Context,
				prepared routing.RouteEntry,
			) error {
				return routing.AdvanceMySQLFence(ctx, mysqlDB, prepared)
			}
			overlayCtx, overlayCancel := context.WithTimeout(
				context.Background(), 10*time.Second,
			)
			openCount, overlayErr := migrations.loadOpenProgress(overlayCtx, now)
			overlayCancel()
			if overlayErr != nil {
				return overlayErr
			}
			if openCount > 0 {
				logger.Warn(
					"loaded open PREPARING migrations; fail-closed until continue or abandon",
					"open_migrations", openCount,
				)
			}
		}
	}

	ctx, cancel := shutdown.SignalContext(context.Background())
	defer cancel()
	if routeStoreMode == routeStoreTcaplus {
		go renewRuntimeLeaseOverlay(ctx, routes, runtimeLeases, leaseDuration, logger)
	} else {
		go renewOwnedLeases(ctx, routes, zones, leaseDuration, logger)
	}

	mux := http.NewServeMux()
	if routeStoreMode == routeStoreTcaplus {
		mux.Handle("/internal/v1/", routing.NewHTTPHandlerWithRuntimeLeases(routes, runtimeLeases, time.Now))
	} else {
		mux.Handle("/internal/v1/", routing.NewHTTPHandler(routes, time.Now))
	}
	if mode == routingModeStaticDualZone && migrations != nil {
		mux.HandleFunc(
			"POST /internal/v1/shards/{shard_id}/move",
			migrations.move,
		)
		mux.HandleFunc(
			"GET /internal/v1/shards/{shard_id}/migration",
			migrations.inspect,
		)
		mux.HandleFunc(
			"GET /internal/v1/migrations",
			migrations.listOpen,
		)
		mux.HandleFunc(
			"POST /internal/v1/shards/{shard_id}/migration/continue",
			migrations.continueMigration,
		)
		mux.HandleFunc(
			"POST /internal/v1/shards/{shard_id}/migration/abandon",
			migrations.abandonMigration,
		)
	}
	mux.Handle("/", health.NewHandler(health.Check{
		Name: "shard_map",
		Run: func(context.Context) error {
			snapshot := routes.Snapshot()
			if len(snapshot.Entries) != int(routing.ShardCount) {
				return fmt.Errorf("expected %d routes, got %d", routing.ShardCount, len(snapshot.Entries))
			}
			active := 0
			for _, entry := range snapshot.Entries {
				if entry.State == routing.RouteStateActive {
					active++
				}
			}
			if active == 0 {
				return errors.New("no ACTIVE shard routes are committed")
			}
			return nil
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
		"routing_mode", mode,
		"shard_count", routing.ShardCount,
		"zone_count", len(zones),
		"lease_duration", leaseDuration.String(),
		"consensus", false,
		"high_availability", false,
	)
	return shutdown.Serve(ctx, server, cfg.ShutdownTimeout, logger)
}

func renewRuntimeLeaseOverlay(ctx context.Context, routes *routing.Map, overlay *routing.RuntimeLeaseOverlay, leaseDuration time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(leaseDuration / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := overlay.Renew(routes.Snapshot(), now.UTC(), leaseDuration); err != nil {
				logger.Error("runtime lease overlay renewal failed", "error", err)
			}
		}
	}
}

func renewOwnedLeases(
	ctx context.Context,
	routes *routing.Map,
	zones []routing.ZoneCandidate,
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
			for _, zone := range zones {
				renewed, err := routes.RenewOwnedLeases(
					zone.ZoneID,
					now.UTC(),
					leaseDuration,
				)
				if err != nil {
					logger.Error("lease renewal failed",
						"zone_id", zone.ZoneID, "error", err)
					continue
				}
				logger.Debug("leases renewed",
					"zone_id", zone.ZoneID, "route_count", renewed)
			}
		}
	}
}

func routingConfigurationFromEnvironment() (
	string,
	[]routing.ZoneCandidate,
	error,
) {
	mode := strings.TrimSpace(os.Getenv("ROUTING_MODE"))
	if mode == "" {
		mode = routingModeLocal
	}
	switch mode {
	case routingModeLocal:
		return mode, []routing.ZoneCandidate{{
			ZoneID: routing.DefaultZoneID, Endpoint: routing.DefaultZoneEndpoint,
		}}, nil
	case routingModeStaticDualZone:
		zones := []routing.ZoneCandidate{
			{
				ZoneID:   environmentOr("ZONE_A_ID", "zone-a"),
				Endpoint: environmentOr("ZONE_A_ENDPOINT", "http://127.0.0.1:8082"),
			},
			{
				ZoneID:   environmentOr("ZONE_B_ID", "zone-b"),
				Endpoint: environmentOr("ZONE_B_ENDPOINT", "http://127.0.0.1:8084"),
			},
		}
		return mode, zones, nil
	default:
		return "", nil, fmt.Errorf("unsupported ROUTING_MODE %q", mode)
	}
}

func environmentOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
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
		address = "127.0.0.1" + address
	}
	if err := internalnet.RequireListenAddress(address, "Coordinator"); err != nil {
		return "", err
	}
	return address, nil
}
