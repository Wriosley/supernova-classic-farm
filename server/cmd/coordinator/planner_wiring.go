package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/migration"
	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/placement"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type plannerConfig struct {
	Enabled  bool
	Interval time.Duration
}

func plannerConfigFromEnvironment() (plannerConfig, error) {
	config := plannerConfig{Interval: 30 * time.Second}
	switch strings.TrimSpace(os.Getenv("COORDINATOR_PLANNER_ENABLED")) {
	case "", "0":
	case "1":
		config.Enabled = true
	default:
		return config, errors.New("COORDINATOR_PLANNER_ENABLED must be 0 or 1")
	}
	rawInterval := strings.TrimSpace(os.Getenv("COORDINATOR_PLANNER_INTERVAL"))
	if rawInterval != "" {
		interval, err := time.ParseDuration(rawInterval)
		if err != nil || interval <= 0 {
			return config, fmt.Errorf("invalid COORDINATOR_PLANNER_INTERVAL %q", rawInterval)
		}
		config.Interval = interval
	}
	return config, nil
}

func startPlanner(
	ctx context.Context,
	routes *routing.Map,
	members *membershipRuntime,
	store migration.TaskStore,
	config plannerConfig,
	logger *slog.Logger,
) error {
	if !config.Enabled {
		return nil
	}
	if routes == nil || members == nil || members.registry == nil || store == nil {
		return errors.New("enabled planner requires Current, membership and MigrationTask store")
	}
	planner, err := placement.NewPlanner(routes, members.registry, store)
	if err != nil {
		return err
	}
	go planner.Run(ctx, config.Interval, members.ready, members.registry.Changes(), func(result placement.Result, err error) {
		if err != nil {
			if ctx.Err() == nil {
				logger.Error("placement reconcile failed", "error", err)
			}
			return
		}
		logger.Info("placement reconcile complete",
			"proposed", result.Proposed, "deduplicated", result.Deduplicated,
			"cancelled", result.Cancelled, "unchanged", result.Unchanged,
			"skipped_unhealthy", result.SkippedUnhealthy)
	})
	return nil
}
