package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/leadership"
	coordinatormigration "github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/migration"
	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/publisher"
	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/routestore"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type electionConfig struct {
	Enabled       bool
	LeaseName     string
	LeaseDuration time.Duration
	RenewDeadline time.Duration
	RetryPeriod   time.Duration
	Identity      string
	Namespace     string
	FollowPoll    time.Duration
}

func electionConfigFromEnvironment() (electionConfig, error) {
	cfg := electionConfig{
		LeaseName:     environmentOr("COORDINATOR_LEASE_NAME", "classic-farm-coordinator"),
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   2 * time.Second,
		FollowPoll:    2 * time.Second,
		Namespace:     strings.TrimSpace(os.Getenv("POD_NAMESPACE")),
	}
	switch strings.TrimSpace(os.Getenv("COORDINATOR_ELECTION_ENABLED")) {
	case "", "0":
	case "1":
		cfg.Enabled = true
	default:
		return cfg, errors.New("COORDINATOR_ELECTION_ENABLED must be 0 or 1")
	}
	if !cfg.Enabled {
		return cfg, nil
	}
	var err error
	if raw := strings.TrimSpace(os.Getenv("COORDINATOR_LEASE_DURATION")); raw != "" {
		cfg.LeaseDuration, err = time.ParseDuration(raw)
		if err != nil || cfg.LeaseDuration <= 0 {
			return cfg, errors.New("invalid COORDINATOR_LEASE_DURATION")
		}
	}
	if raw := strings.TrimSpace(os.Getenv("COORDINATOR_RENEW_DEADLINE")); raw != "" {
		cfg.RenewDeadline, err = time.ParseDuration(raw)
		if err != nil || cfg.RenewDeadline <= 0 {
			return cfg, errors.New("invalid COORDINATOR_RENEW_DEADLINE")
		}
	}
	if raw := strings.TrimSpace(os.Getenv("COORDINATOR_RETRY_PERIOD")); raw != "" {
		cfg.RetryPeriod, err = time.ParseDuration(raw)
		if err != nil || cfg.RetryPeriod <= 0 {
			return cfg, errors.New("invalid COORDINATOR_RETRY_PERIOD")
		}
	}
	if raw := strings.TrimSpace(os.Getenv("COORDINATOR_FOLLOWER_ROUTE_POLL")); raw != "" {
		cfg.FollowPoll, err = time.ParseDuration(raw)
		if err != nil || cfg.FollowPoll <= 0 {
			return cfg, errors.New("invalid COORDINATOR_FOLLOWER_ROUTE_POLL")
		}
	}
	if cfg.Namespace == "" {
		return cfg, errors.New("POD_NAMESPACE is required when election is enabled")
	}
	cfg.Identity, err = leadership.ElectionIdentity(os.Getenv("POD_NAME"))
	if err != nil {
		return cfg, err
	}
	if !(cfg.LeaseDuration > cfg.RenewDeadline && cfg.RenewDeadline > cfg.RetryPeriod) {
		return cfg, errors.New("require COORDINATOR_LEASE_DURATION > RENEW_DEADLINE > RETRY_PERIOD")
	}
	return cfg, nil
}

type leaderRuntime struct {
	mu sync.Mutex

	elector leadership.Elector
	logger  *slog.Logger

	routes        *routing.Map
	membership    *membershipRuntime
	tasks         coordinatormigration.TaskStore
	progress      coordinatormigration.ProgressBackend
	durable       routestore.Store
	fences        coordinatormigration.FenceStore
	publisher     *publisher.Publisher
	leaseDuration time.Duration
	plannerConfig plannerConfig
	workerConfig  migrationWorkerConfig
	httpClient    *http.Client
}

func newLeaderRuntime(
	routes *routing.Map,
	membership *membershipRuntime,
	tasks coordinatormigration.TaskStore,
	progress coordinatormigration.ProgressBackend,
	durable routestore.Store,
	fences coordinatormigration.FenceStore,
	routePublisher *publisher.Publisher,
	leaseDuration time.Duration,
	plannerCfg plannerConfig,
	workerCfg migrationWorkerConfig,
	logger *slog.Logger,
) *leaderRuntime {
	return &leaderRuntime{
		logger: logger, routes: routes, membership: membership,
		tasks: tasks, progress: progress, durable: durable, fences: fences,
		publisher: routePublisher, leaseDuration: leaseDuration,
		plannerConfig: plannerCfg, workerConfig: workerCfg,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

func (r *leaderRuntime) setElector(elector leadership.Elector) {
	r.mu.Lock()
	r.elector = elector
	r.mu.Unlock()
}

func (r *leaderRuntime) IsLeader() bool {
	r.mu.Lock()
	elector := r.elector
	r.mu.Unlock()
	return elector != nil && elector.State().IsLeader
}

func (r *leaderRuntime) State() leadership.State {
	r.mu.Lock()
	elector := r.elector
	r.mu.Unlock()
	if elector == nil {
		return leadership.State{}
	}
	return elector.State()
}

func (r *leaderRuntime) Callbacks() leadership.Callbacks {
	return leadership.Callbacks{
		OnStartedLeading: r.onStartedLeading,
		OnStoppedLeading: r.onStoppedLeading,
		OnNewLeader: func(identity string) {
			r.logger.Info("coordinator observed leader", "leader", identity, "self", r.State().Identity)
		},
	}
}

func (r *leaderRuntime) onStartedLeading(ctx context.Context, generation uint64) {
	r.logger.Info("became coordinator leader", "generation", generation, "identity", r.State().Identity)
	if err := r.reloadDurable(ctx); err != nil {
		r.logger.Error("leader durable reload failed; refusing mutation loops", "error", err, "generation", generation)
		return
	}
	if err := startPlanner(ctx, r.routes, r.membership, r.tasks, r.plannerConfig, r.logger); err != nil {
		r.logger.Error("leader planner start failed", "error", err, "generation", generation)
		return
	}
	if err := startMigrationWorker(ctx, r.workerConfig, r.tasks, r.progress, r.routes,
		r.durable, r.fences, r.publisher, r.httpClient, r.leaseDuration, r.logger); err != nil {
		r.logger.Error("leader migration worker start failed", "error", err, "generation", generation)
		return
	}
	<-ctx.Done()
	r.logger.Info("leader mutation context cancelled", "generation", generation)
}

func (r *leaderRuntime) onStoppedLeading(generation uint64) {
	r.logger.Info("lost coordinator leadership", "generation", generation, "identity", r.State().Identity)
}

func (r *leaderRuntime) reloadDurable(ctx context.Context) error {
	if r.durable == nil || r.routes == nil {
		return nil
	}
	loaded, err := r.durable.Load(ctx)
	if err != nil {
		return err
	}
	snapshot := routestore.RoutingSnapshot(loaded)
	previous := r.routes.Snapshot()
	if snapshot.MapVersion < previous.MapVersion {
		return fmt.Errorf("durable map_version %d behind local %d", snapshot.MapVersion, previous.MapVersion)
	}
	if snapshot.MapVersion == previous.MapVersion {
		return nil
	}
	if err := r.routes.ApplyCommittedSnapshot(snapshot); err != nil {
		return err
	}
	current := r.routes.Snapshot()
	if r.publisher != nil {
		if pubErr := r.publisher.PublishRoutes(previous, current); pubErr != nil {
			r.logger.Warn("leader reload publish failed", "error", pubErr)
		}
	}
	r.logger.Info("leader reloaded durable routes", "map_version", current.MapVersion)
	return nil
}

func requireLeader(runtime *leaderRuntime) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if runtime != nil && !runtime.IsLeader() {
				state := runtime.State()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error":  "not_leader",
					"leader": state.LeaderIdentity,
					"self":   state.Identity,
				})
				return
			}
			next(w, r)
		}
	}
}

func leaderStatusHandler(runtime *leaderRuntime) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		payload := map[string]any{"election_enabled": runtime != nil}
		if runtime != nil {
			state := runtime.State()
			payload["is_leader"] = state.IsLeader
			payload["identity"] = state.Identity
			payload["leader"] = state.LeaderIdentity
			payload["generation"] = state.Generation
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}
}

func startElection(
	ctx context.Context,
	cfg electionConfig,
	runtime *leaderRuntime,
	logger *slog.Logger,
) error {
	inCluster, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("election requires in-cluster config: %w", err)
	}
	client, err := kubernetes.NewForConfig(inCluster)
	if err != nil {
		return fmt.Errorf("create election kubernetes client: %w", err)
	}
	elector, err := leadership.NewKubernetesElector(leadership.KubernetesConfig{
		Client: client, Namespace: cfg.Namespace, LeaseName: cfg.LeaseName,
		Identity: cfg.Identity, LeaseDuration: cfg.LeaseDuration,
		RenewDeadline: cfg.RenewDeadline, RetryPeriod: cfg.RetryPeriod,
	})
	if err != nil {
		return err
	}
	runtime.setElector(elector)
	go func() {
		if runErr := elector.Run(ctx, runtime.Callbacks()); runErr != nil && ctx.Err() == nil {
			logger.Error("leader election stopped", "error", runErr)
		}
	}()
	return nil
}

func startFollowerRouteSync(
	ctx context.Context,
	cfg electionConfig,
	runtime *leaderRuntime,
	routes *routing.Map,
	durable routestore.Store,
	routePublisher *publisher.Publisher,
	logger *slog.Logger,
) {
	if durable == nil || routes == nil || cfg.FollowPoll <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(cfg.FollowPoll)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if runtime != nil && runtime.IsLeader() {
					continue
				}
				if err := syncFollowerRoutes(ctx, routes, durable, routePublisher, logger); err != nil && ctx.Err() == nil {
					logger.Warn("follower route sync failed", "error", err)
				}
			}
		}
	}()
}

func syncFollowerRoutes(
	ctx context.Context,
	routes *routing.Map,
	durable routestore.Store,
	routePublisher *publisher.Publisher,
	logger *slog.Logger,
) error {
	loaded, err := durable.Load(ctx)
	if err != nil {
		return err
	}
	snapshot := routestore.RoutingSnapshot(loaded)
	previous := routes.Snapshot()
	if snapshot.MapVersion <= previous.MapVersion {
		return nil
	}
	if err := routes.ApplyCommittedSnapshot(snapshot); err != nil {
		return err
	}
	current := routes.Snapshot()
	if routePublisher != nil {
		if pubErr := routePublisher.PublishRoutes(previous, current); pubErr != nil {
			return pubErr
		}
	}
	logger.Info("follower applied durable routes", "previous_map_version", previous.MapVersion, "map_version", current.MapVersion)
	return nil
}
