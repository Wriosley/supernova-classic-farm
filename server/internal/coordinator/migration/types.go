package migration

import (
	"context"
	"errors"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type Step string

const (
	StepSourceDraining Step = "SOURCE_DRAINING"
	StepSourceFlushed  Step = "SOURCE_FLUSHED"
	StepRoutePreparing Step = "ROUTE_PREPARING"
	StepFenceAdvanced  Step = "FENCE_ADVANCED"
	StepTargetLoading  Step = "TARGET_LOADING"
	StepTargetReady    Step = "TARGET_READY"
	StepRouteActive    Step = "ROUTE_ACTIVE"
)

var (
	ErrProgressConflict     = errors.New("migration progress conflict")
	ErrFenceAlreadyAdvanced = routing.ErrFenceAlreadyAdvanced
)

type Manifest []ManifestEntry

type ManifestEntry struct {
	PlayerID           uint64
	OwnerEpoch         uint64
	CheckpointRevision uint64
}

type Progress struct {
	ShardID      uint32
	TransitionID string
	Step         Step
	Source       routing.RouteEntry
	Prepared     routing.RouteEntry
	Manifest     Manifest
	UpdatedAtMS  int64
}

type ZoneLifecycle interface {
	Drain(context.Context, routing.RouteEntry, string) (Manifest, error)
	Restore(context.Context, routing.RouteEntry, string) error
	Prepare(context.Context, routing.RouteEntry, Manifest) error
	RefreshOwnership(context.Context, routing.RouteEntry) error
}

type FenceStore interface {
	AdvanceFence(context.Context, routing.RouteEntry) error
}
