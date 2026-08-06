package visit

import (
	"context"
	"errors"
	"time"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
)

// evictionInterval matches the design's "background ticker every 5s".
const evictionInterval = 5 * time.Second

// FarmSnapshotBuilder abstracts player.Runtime.BuildPublicFarmSnapshot so
// OwnerService is testable without a full Runtime/Actor stack. The one
// production implementation is *player.Runtime itself.
type FarmSnapshotBuilder interface {
	BuildPublicFarmSnapshot(
		ctx context.Context, ownerPlayerID, ownerEpoch uint64,
	) (*wsv1.FarmVisitSnapshot, error)
}

// PresencePublisher abstracts pushing one FarmPresencePush tip to the owner
// through Gate (GatePushService.PublishFarmPresence on the production path).
// Publish failures are logged by the caller and otherwise ignored: a missed
// presence tip does not affect visit correctness, only how promptly the
// owner's UI reflects it.
type PresencePublisher interface {
	PublishFarmPresence(ctx context.Context, ownerPlayerID uint64, presence *wsv1.FarmPresencePush) error
}

// OwnerService implements the Owner Zone side of Phase 3: it holds the
// in-memory VisitorRegistry, builds public FarmVisitSnapshots through
// FarmSnapshotBuilder, and pushes FARM_PRESENCE_CHANGED tips to the owner on
// enter/leave (including TTL eviction). Every method mirrors Service's
// (result, domainError, transportError) convention.
type OwnerService struct {
	registry  *Registry
	snapshots FarmSnapshotBuilder
	presence  PresencePublisher
	now       func() time.Time
}

func NewOwnerService(
	snapshots FarmSnapshotBuilder, presence PresencePublisher, now func() time.Time,
) (*OwnerService, error) {
	if snapshots == nil || presence == nil {
		return nil, errors.New("farm snapshot builder and presence publisher are required")
	}
	if now == nil {
		now = time.Now
	}
	return &OwnerService{
		registry: NewRegistry(), snapshots: snapshots, presence: presence, now: now,
	}, nil
}

// EnterVisitor builds the current public snapshot before registering the
// lease: a visitor who cannot get a valid snapshot must not appear to the
// owner as present, and no orphaned lease should linger past this call.
func (o *OwnerService) EnterVisitor(
	ctx context.Context,
	ownerPlayerID, ownerEpoch, visitorPlayerID uint64,
	gateID, requestID string,
) (visitID []byte, expiresAtMs int64, snapshot *wsv1.FarmVisitSnapshot, wsErr *wsv1.Error, err error) {
	snapshot, err = o.snapshots.BuildPublicFarmSnapshot(ctx, ownerPlayerID, ownerEpoch)
	if err != nil {
		return nil, 0, nil, nil, err
	}
	visitID, expiresAt, newlyCreated, err := o.registry.Enter(
		ownerPlayerID, visitorPlayerID, gateID, requestID, o.now(),
	)
	if err != nil {
		return nil, 0, nil, nil, err
	}
	if newlyCreated {
		_ = o.presence.PublishFarmPresence(ctx, ownerPlayerID, &wsv1.FarmPresencePush{
			OwnerPlayerId: ownerPlayerID, Kind: wsv1.FarmPresenceKind_FARM_VISITOR_ENTERED,
		})
	}
	return visitID, expiresAt.UnixMilli(), snapshot, nil, nil
}

func (o *OwnerService) RefreshVisitorHeartbeat(
	_ context.Context,
	ownerPlayerID, visitorPlayerID uint64,
	visitID []byte,
	gateID string,
) (expiresAtMs int64, wsErr *wsv1.Error, err error) {
	expiresAt, refreshErr := o.registry.Refresh(ownerPlayerID, visitorPlayerID, visitID, gateID, o.now())
	switch {
	case errors.Is(refreshErr, ErrVisitNotFound):
		return 0, &wsv1.Error{Code: wsv1.ErrorCode_VISIT_NOT_FOUND}, nil
	case errors.Is(refreshErr, ErrVisitExpired):
		return 0, &wsv1.Error{Code: wsv1.ErrorCode_VISIT_EXPIRED}, nil
	case refreshErr != nil:
		return 0, nil, refreshErr
	default:
		return expiresAt.UnixMilli(), nil, nil
	}
}

func (o *OwnerService) ExitVisitor(
	ctx context.Context,
	ownerPlayerID, visitorPlayerID uint64,
	visitID []byte,
) (wsErr *wsv1.Error, err error) {
	_, exitErr := o.registry.Exit(ownerPlayerID, visitorPlayerID, visitID)
	switch {
	case errors.Is(exitErr, ErrVisitNotFound):
		return &wsv1.Error{Code: wsv1.ErrorCode_VISIT_NOT_FOUND}, nil
	case exitErr != nil:
		return nil, exitErr
	}
	_ = o.presence.PublishFarmPresence(ctx, ownerPlayerID, &wsv1.FarmPresencePush{
		OwnerPlayerId: ownerPlayerID, Kind: wsv1.FarmPresenceKind_FARM_VISITOR_LEFT,
	})
	return nil, nil
}

// GetPublicFarmSnapshot requires an unexpired visit lease (Validate does not
// extend it: only HEARTBEAT should renew the TTL) before returning the
// owner's current public snapshot.
func (o *OwnerService) GetPublicFarmSnapshot(
	ctx context.Context,
	ownerPlayerID, ownerEpoch, visitorPlayerID uint64,
	visitID []byte,
) (*wsv1.FarmVisitSnapshot, *wsv1.Error, error) {
	switch validateErr := o.registry.Validate(ownerPlayerID, visitorPlayerID, visitID, o.now()); {
	case errors.Is(validateErr, ErrVisitNotFound):
		return nil, &wsv1.Error{Code: wsv1.ErrorCode_VISIT_NOT_FOUND}, nil
	case errors.Is(validateErr, ErrVisitExpired):
		return nil, &wsv1.Error{Code: wsv1.ErrorCode_VISIT_EXPIRED}, nil
	case validateErr != nil:
		return nil, nil, validateErr
	}
	snapshot, err := o.snapshots.BuildPublicFarmSnapshot(ctx, ownerPlayerID, ownerEpoch)
	if err != nil {
		return nil, nil, err
	}
	return snapshot, nil, nil
}

// ValidateVisitorAction requires an unexpired visit lease for
// (ownerPlayerID, visitorPlayerID, visitID) before a friend action (e.g.
// STEAL_FRIEND_CROP) may be applied against the owner's farm. Unlike
// GetPublicFarmSnapshot it never builds a snapshot and never extends the
// lease TTL: only HEARTBEAT should renew it.
func (o *OwnerService) ValidateVisitorAction(
	ownerPlayerID, visitorPlayerID uint64, visitID []byte,
) (wsErr *wsv1.Error, err error) {
	switch validateErr := o.registry.Validate(ownerPlayerID, visitorPlayerID, visitID, o.now()); {
	case errors.Is(validateErr, ErrVisitNotFound):
		return &wsv1.Error{Code: wsv1.ErrorCode_VISIT_NOT_FOUND}, nil
	case errors.Is(validateErr, ErrVisitExpired):
		return &wsv1.Error{Code: wsv1.ErrorCode_VISIT_EXPIRED}, nil
	case validateErr != nil:
		return nil, validateErr
	default:
		return nil, nil
	}
}

// ListVisitors returns owner's current visitors (Phase 4 FarmViewPatch
// fan-out), wrapping the in-memory Registry so callers outside package visit
// never need to reach into it directly.
func (o *OwnerService) ListVisitors(ownerPlayerID uint64) []VisitRecord {
	return o.registry.ListVisitors(ownerPlayerID)
}

// RunEvictionLoop sweeps expired visits every five seconds and pushes a LEFT
// tip for each one, until ctx is cancelled. Callers should run it in its own
// goroutine for the lifetime of the Zone process.
func (o *OwnerService) RunEvictionLoop(ctx context.Context) {
	ticker := time.NewTicker(evictionInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			o.evictExpired(ctx)
		}
	}
}

func (o *OwnerService) evictExpired(ctx context.Context) {
	for _, record := range o.registry.EvictExpired(o.now()) {
		_ = o.presence.PublishFarmPresence(ctx, record.OwnerPlayerID, &wsv1.FarmPresencePush{
			OwnerPlayerId: record.OwnerPlayerID, Kind: wsv1.FarmPresenceKind_FARM_VISITOR_LEFT,
		})
	}
}
