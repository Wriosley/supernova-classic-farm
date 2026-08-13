package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	coordinatormigration "github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/migration"
	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/routestore"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type migrationWorkerConfig struct {
	Enabled bool
	Limits  coordinatormigration.Limits
}

func migrationWorkerConfigFromEnvironment() (migrationWorkerConfig, error) {
	config := migrationWorkerConfig{Limits: coordinatormigration.Limits{Global: 8, PerSource: 2, PerTarget: 2}}
	switch strings.TrimSpace(os.Getenv("COORDINATOR_MIGRATION_WORKER_ENABLED")) {
	case "", "0":
	case "1":
		config.Enabled = true
	default:
		return config, errors.New("COORDINATOR_MIGRATION_WORKER_ENABLED must be 0 or 1")
	}
	return config, nil
}

func startMigrationWorker(
	ctx context.Context,
	config migrationWorkerConfig,
	tasks coordinatormigration.TaskStore,
	progressBackend coordinatormigration.ProgressBackend,
	routes *routing.Map,
	routeStore routestore.Store,
	fences coordinatormigration.FenceStore,
	publisher coordinatormigration.RoutePublisher,
	client *http.Client,
	leaseDuration time.Duration,
	logger *slog.Logger,
) error {
	if !config.Enabled {
		return nil
	}
	progress, err := coordinatormigration.NewProgressStore(progressBackend)
	if err != nil {
		return err
	}
	executor, err := coordinatormigration.NewExecutor(coordinatormigration.ExecutorConfig{
		Tasks: tasks, Progress: progress, Routes: routes, RouteStore: routeStore,
		Zones: newHTTPZoneLifecycle(client), Fences: fences, Publisher: publisher,
		Now: time.Now, LeaseDuration: leaseDuration,
	})
	if err != nil {
		return err
	}
	scheduler, err := coordinatormigration.NewScheduler(tasks, executor, config.Limits)
	if err != nil {
		return err
	}
	go func() {
		if runErr := scheduler.Run(ctx); runErr != nil && ctx.Err() == nil {
			logger.Error("migration worker stopped", "error", runErr)
		}
	}()
	logger.Info("migration worker started", "global_limit", config.Limits.Global, "per_source_limit", config.Limits.PerSource, "per_target_limit", config.Limits.PerTarget)
	return nil
}

type httpZoneLifecycle struct{ client *http.Client }

func newHTTPZoneLifecycle(client *http.Client) *httpZoneLifecycle {
	if client == nil {
		client = http.DefaultClient
	}
	return &httpZoneLifecycle{client: client}
}

func (lifecycle *httpZoneLifecycle) Drain(ctx context.Context, source routing.RouteEntry, transitionID string) (coordinatormigration.Manifest, error) {
	body := lifecycleIdentity(source.OwnerEpoch, transitionID)
	if err := lifecycle.post(ctx, source.OwnerEndpoint, source.ShardID, "drain", body, http.StatusOK, http.StatusNoContent); err != nil {
		return nil, err
	}
	response, err := lifecycle.postResponse(ctx, source.OwnerEndpoint, source.ShardID, "drain-complete", body)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("Zone drain-complete returned %s", response.Status)
	}
	var result struct {
		ShardID    uint32            `json:"shard_id"`
		OwnerEpoch string            `json:"owner_epoch"`
		Players    []migrationPlayer `json:"players"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return nil, err
	}
	if result.ShardID != source.ShardID || result.OwnerEpoch != strconv.FormatUint(source.OwnerEpoch, 10) {
		return nil, errors.New("Zone drain manifest metadata mismatch")
	}
	manifest := make(coordinatormigration.Manifest, len(result.Players))
	for index, item := range result.Players {
		playerID, playerErr := strconv.ParseUint(item.PlayerID, 10, 64)
		epoch, epochErr := strconv.ParseUint(item.OwnerEpoch, 10, 64)
		revision, revisionErr := strconv.ParseUint(item.CheckpointRevision, 10, 64)
		if playerErr != nil || epochErr != nil || revisionErr != nil {
			return nil, errors.New("Zone drain manifest contains invalid player")
		}
		manifest[index] = coordinatormigration.ManifestEntry{PlayerID: playerID, OwnerEpoch: epoch, CheckpointRevision: revision}
	}
	return manifest, nil
}

func (lifecycle *httpZoneLifecycle) Restore(ctx context.Context, source routing.RouteEntry, transitionID string) error {
	return lifecycle.post(ctx, source.OwnerEndpoint, source.ShardID, "resume", lifecycleIdentity(source.OwnerEpoch, transitionID), http.StatusNoContent)
}

func (lifecycle *httpZoneLifecycle) Prepare(ctx context.Context, prepared routing.RouteEntry, manifest coordinatormigration.Manifest) error {
	players := make([]migrationPlayer, len(manifest))
	for index, item := range manifest {
		players[index] = migrationPlayer{PlayerID: strconv.FormatUint(item.PlayerID, 10), OwnerEpoch: strconv.FormatUint(item.OwnerEpoch, 10), CheckpointRevision: strconv.FormatUint(item.CheckpointRevision, 10)}
	}
	body, _ := json.Marshal(struct {
		OwnerEpoch   string            `json:"owner_epoch"`
		TransitionID string            `json:"transition_id"`
		Players      []migrationPlayer `json:"players"`
	}{strconv.FormatUint(prepared.OwnerEpoch, 10), prepared.TransitionID, players})
	return lifecycle.post(ctx, prepared.OwnerEndpoint, prepared.ShardID, "prepare", body, http.StatusNoContent)
}

func (lifecycle *httpZoneLifecycle) RefreshOwnership(ctx context.Context, target routing.RouteEntry) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(target.OwnerEndpoint, "/")+"/internal/v1/ownership/refresh", nil)
	if err != nil {
		return err
	}
	response, err := lifecycle.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("Zone ownership refresh returned %s", response.Status)
	}
	return nil
}

func lifecycleIdentity(ownerEpoch uint64, transitionID string) []byte {
	body, _ := json.Marshal(struct {
		OwnerEpoch   string `json:"owner_epoch"`
		TransitionID string `json:"transition_id"`
	}{strconv.FormatUint(ownerEpoch, 10), transitionID})
	return body
}

func (lifecycle *httpZoneLifecycle) post(ctx context.Context, endpoint string, shardID uint32, action string, body []byte, accepted ...int) error {
	response, err := lifecycle.postResponse(ctx, endpoint, shardID, action, body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	for _, status := range accepted {
		if response.StatusCode == status {
			return nil
		}
	}
	return fmt.Errorf("Zone %s returned %s", action, response.Status)
}

func (lifecycle *httpZoneLifecycle) postResponse(ctx context.Context, endpoint string, shardID uint32, action string, body []byte) (*http.Response, error) {
	url := strings.TrimRight(endpoint, "/") + "/internal/v1/shards/" + strconv.FormatUint(uint64(shardID), 10) + "/" + action
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return lifecycle.client.Do(req)
}
