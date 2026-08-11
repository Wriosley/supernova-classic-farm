package main

import (
	"context"
	"errors"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/interaction"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"github.com/Wriosley/supernova-classic-farm/server/internal/visit"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// visitorZoneRPCServer implements VisitorZoneService: it runs on the
// visitor's own Zone, so its only ownership check is that this process
// currently owns the caller's shard (no CommittedRoute is supplied by the
// client, unlike game commands, because friend-visit sessions are ephemeral
// and do not need the stronger route-fencing that durable player state does).
type visitorZoneRPCServer struct {
	rpcv1.UnimplementedVisitorZoneServiceServer

	visits        *visit.Service
	authorization ownerAuthorization
	ownZoneID     string

	// runtime and steal are nil-safe: a Zone that has not wired the Phase 5
	// interaction Saga (see main.go) simply reports every
	// ExecuteFriendAction as SERVICE_UNAVAILABLE rather than panicking.
	runtime *player.Runtime
	steal   *interaction.StealSaga
	action  *interaction.ActionSaga
}

func newVisitorZoneRPCServer(
	visits *visit.Service, authorization ownerAuthorization, ownZoneID string,
) *visitorZoneRPCServer {
	return &visitorZoneRPCServer{visits: visits, authorization: authorization, ownZoneID: ownZoneID}
}

// withStealSaga wires the Phase 5 STEAL_FRIEND_CROP interaction Saga onto
// an already-constructed visitorZoneRPCServer; main.go calls this once the
// Zone's interaction Store/Saga are ready.
func (s *visitorZoneRPCServer) withStealSaga(runtime *player.Runtime, saga *interaction.StealSaga) {
	s.runtime = runtime
	s.steal = saga
}

func (s *visitorZoneRPCServer) withFriendSagas(
	runtime *player.Runtime, steal *interaction.StealSaga, action *interaction.ActionSaga,
) {
	s.runtime, s.steal, s.action = runtime, steal, action
}

func (s *visitorZoneRPCServer) authorizeCaller(callerPlayerID uint64) error {
	if s.authorization == nil {
		return status.Error(codes.Unavailable, "ownership is unavailable")
	}
	entry, ok := s.authorization.Entry(routing.ShardForPlayer(callerPlayerID))
	if !ok || entry.OwnerZoneID != s.ownZoneID || entry.State != routing.RouteStateActive {
		return status.Error(codes.FailedPrecondition, "not shard owner")
	}
	return nil
}

func (s *visitorZoneRPCServer) EnterFriendFarm(
	ctx context.Context, request *rpcv1.EnterFriendFarmRequest,
) (*rpcv1.EnterFriendFarmResponse, error) {
	if request == nil || request.CallerPlayerId == 0 || request.OwnerPlayerId == 0 ||
		request.GateId == "" || request.RequestId == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid enter friend farm request")
	}
	if err := s.authorizeCaller(request.CallerPlayerId); err != nil {
		return nil, err
	}
	result, wsErr, err := s.visits.EnterFriendFarm(
		ctx, request.CallerPlayerId, request.OwnerPlayerId, request.GateId, request.RequestId,
	)
	if err != nil {
		return nil, status.Error(codes.Internal, "enter friend farm failed")
	}
	return &rpcv1.EnterFriendFarmResponse{Result: result, Error: wsErr}, nil
}

func (s *visitorZoneRPCServer) HeartbeatFriendFarm(
	ctx context.Context, request *rpcv1.HeartbeatFriendFarmRequest,
) (*rpcv1.HeartbeatFriendFarmResponse, error) {
	if request == nil || request.CallerPlayerId == 0 || request.OwnerPlayerId == 0 ||
		len(request.VisitId) != 16 || request.GateId == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid farm heartbeat request")
	}
	if err := s.authorizeCaller(request.CallerPlayerId); err != nil {
		return nil, err
	}
	result, wsErr, err := s.visits.HeartbeatFriendFarm(
		ctx, request.CallerPlayerId, request.OwnerPlayerId, request.VisitId, request.GateId,
	)
	if err != nil {
		return nil, status.Error(codes.Internal, "farm heartbeat failed")
	}
	return &rpcv1.HeartbeatFriendFarmResponse{Result: result, Error: wsErr}, nil
}

func (s *visitorZoneRPCServer) ExitFriendFarm(
	ctx context.Context, request *rpcv1.ExitFriendFarmRequest,
) (*rpcv1.ExitFriendFarmResponse, error) {
	if request == nil || request.CallerPlayerId == 0 || request.OwnerPlayerId == 0 ||
		len(request.VisitId) != 16 {
		return nil, status.Error(codes.InvalidArgument, "invalid exit friend farm request")
	}
	if err := s.authorizeCaller(request.CallerPlayerId); err != nil {
		return nil, err
	}
	wsErr, err := s.visits.ExitFriendFarm(ctx, request.CallerPlayerId, request.OwnerPlayerId, request.VisitId)
	if err != nil {
		return nil, status.Error(codes.Internal, "exit friend farm failed")
	}
	return &rpcv1.ExitFriendFarmResponse{Error: wsErr}, nil
}

// ExecuteFriendAction drives the Phase 5 interaction Saga for the caller's
// (visitor) side. Only STEAL_FRIEND_CROP is implemented; every other
// FriendInteractionAction (pest apply/catch, help clean — Phase 6) is
// rejected with UNKNOWN_ACTION rather than left Unimplemented, since the
// request shape itself is already valid RPC input.
func (s *visitorZoneRPCServer) ExecuteFriendAction(
	ctx context.Context, request *rpcv1.ExecuteFriendActionRequest,
) (*rpcv1.ExecuteFriendActionResponse, error) {
	if request == nil || request.CallerPlayerId == 0 || request.OwnerPlayerId == 0 ||
		len(request.VisitId) != 16 || request.GateId == "" || request.RequestId == "" || request.PlotId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid execute friend action request")
	}
	switch request.Action {
	case datav1.FriendInteractionAction_STEAL_FRIEND_CROP,
		datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND,
		datav1.FriendInteractionAction_CATCH_PEST_FOR_FRIEND,
		datav1.FriendInteractionAction_HELP_CLEAN_FRIEND_PLOT:
	default:
		return &rpcv1.ExecuteFriendActionResponse{Error: &wsv1.Error{Code: wsv1.ErrorCode_UNKNOWN_ACTION}}, nil
	}
	if request.Action != datav1.FriendInteractionAction_STEAL_FRIEND_CROP && s.action == nil {
		return &rpcv1.ExecuteFriendActionResponse{Error: &wsv1.Error{Code: wsv1.ErrorCode_UNKNOWN_ACTION}}, nil
	}
	if request.Action == datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND && request.GetPestId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "apply pest requires pest_id")
	}
	if err := s.authorizeCaller(request.CallerPlayerId); err != nil {
		return nil, err
	}
	if s.runtime == nil || s.steal == nil {
		return nil, status.Error(codes.Unavailable, "friend interaction Saga is unavailable")
	}
	interactionID, parseErr := interaction.ParseInteractionID(request.RequestId)
	if parseErr != nil {
		return nil, status.Error(codes.InvalidArgument, "request_id must be a UUID")
	}
	entry, ok := s.authorization.Entry(routing.ShardForPlayer(request.CallerPlayerId))
	if !ok {
		return nil, status.Error(codes.Unavailable, "ownership is unavailable")
	}
	var response *wsv1.FriendActionResponse
	var err error
	if request.Action == datav1.FriendInteractionAction_STEAL_FRIEND_CROP {
		if request.GetExpectedCropItemId() == 0 || len(request.GetFarmViewEpoch()) != 16 {
			return nil, status.Error(codes.InvalidArgument, "steal requires expected_crop_item_id and farm_view_epoch")
		}
		response, err = s.steal.Execute(ctx, interaction.StealRequest{
			InteractionID: interactionID, VisitorPlayerID: request.CallerPlayerId,
			VisitorOwnerEpoch: entry.OwnerEpoch, OwnerPlayerID: request.OwnerPlayerId,
			VisitID: request.VisitId, PlotID: request.PlotId,
			CropItemID: request.GetExpectedCropItemId(), Quantity: 1,
			FarmViewEpoch: request.GetFarmViewEpoch(), FarmViewSeq: request.GetFarmViewSeq(),
		}, time.Now())
	} else {
		if s.action == nil {
			return nil, status.Error(codes.Unavailable, "friend action Saga is unavailable")
		}
		response, err = s.action.Execute(ctx, interaction.ActionRequest{
			InteractionID: interactionID, VisitorPlayerID: request.CallerPlayerId,
			VisitorOwnerEpoch: entry.OwnerEpoch, OwnerPlayerID: request.OwnerPlayerId,
			VisitID: request.VisitId, Action: request.Action, PlotID: request.PlotId,
			PestID: request.GetPestId(),
		}, time.Now())
	}
	if wsErr, ok := stealSagaWsError(err); ok {
		return &rpcv1.ExecuteFriendActionResponse{Error: wsErr}, nil
	}
	if err != nil {
		if errors.Is(err, player.ErrNotOwner) {
			return nil, status.Error(codes.FailedPrecondition, "not shard owner")
		}
		return nil, status.Error(codes.Internal, "execute friend action failed")
	}
	return &rpcv1.ExecuteFriendActionResponse{Result: response}, nil
}

// stealSagaWsError classifies an interaction.StealSaga error into the
// stable, client-facing ws.Error the caller's WsEnvelope should carry, per
// docs/contracts/idempotency-and-errors.md. Every other error (mailbox
// failure, Tcaplus I/O, an unconverged Saga) is a transport/internal
// failure the RPC layer surfaces as codes.Internal instead.
func stealSagaWsError(err error) (*wsv1.Error, bool) {
	if err == nil {
		return nil, false
	}
	var aborted *interaction.AbortedError
	switch {
	case errors.As(err, &aborted):
		return &wsv1.Error{Code: aborted.Code}, true
	case errors.Is(err, interaction.ErrDigestConflict):
		return &wsv1.Error{Code: wsv1.ErrorCode_REQUEST_ID_CONFLICT}, true
	case errors.Is(err, interaction.ErrOutcomeUnknown):
		return &wsv1.Error{Code: wsv1.ErrorCode_INTERACTION_OUTCOME_UNKNOWN, Retryable: true}, true
	default:
		return nil, false
	}
}

// ownerFarmRPCServer implements OwnerFarmService: it runs on the owner's
// Zone and is called by whichever visitor Zone resolved this owner's route
// through the Coordinator, so every call (except the currently-Unimplemented
// ApplyVisitorAction) carries and is fenced by a CommittedRoute exactly like
// GameCommandService and PlayerSocialService.
type ownerFarmRPCServer struct {
	rpcv1.UnimplementedOwnerFarmServiceServer

	owner         *visit.OwnerService
	authorization ownerAuthorization
	gates         *shardExecutionGates
	now           func() time.Time

	// runtime is nil-safe like visitorZoneRPCServer.runtime: see withRuntime.
	runtime              *player.Runtime
	friendActionsEnabled bool
}

func newOwnerFarmRPCServer(
	owner *visit.OwnerService, authorization ownerAuthorization, gates *shardExecutionGates, now func() time.Time,
) *ownerFarmRPCServer {
	if now == nil {
		now = time.Now
	}
	return &ownerFarmRPCServer{owner: owner, authorization: authorization, gates: gates, now: now}
}

// withRuntime wires the Player Runtime needed by ApplyVisitorAction
// (Runtime.ApplyStealOnOwner); main.go calls this once construction order
// allows it (Runtime must already exist).
func (s *ownerFarmRPCServer) withRuntime(runtime *player.Runtime) {
	s.runtime = runtime
}

func (s *ownerFarmRPCServer) enableFriendActions() {
	s.friendActionsEnabled = true
}

func (s *ownerFarmRPCServer) validateRoute(route *rpcv1.CommittedRoute, ownerPlayerID uint64) error {
	if route == nil || route.LogicalShardId >= routing.ShardCount ||
		route.OwnerZoneId == "" || route.OwnerEpoch == 0 || route.RouteVersion == 0 {
		return status.Error(codes.InvalidArgument, "invalid committed route")
	}
	if s.authorization == nil {
		return status.Error(codes.Unavailable, "ownership is unavailable")
	}
	if err := s.authorization.Validate(
		ownerPlayerID, route.LogicalShardId, route.OwnerZoneId, route.OwnerEpoch, s.now(),
	); err != nil {
		return status.Error(codes.FailedPrecondition, "not shard owner")
	}
	return nil
}

func (s *ownerFarmRPCServer) EnterVisitor(
	ctx context.Context, request *rpcv1.EnterVisitorRequest,
) (*rpcv1.EnterVisitorResponse, error) {
	if request == nil || request.OwnerPlayerId == 0 || request.VisitorPlayerId == 0 ||
		request.GateId == "" || len(request.RelationId) != 16 || request.RequestId == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid enter visitor request")
	}
	if err := s.validateRoute(request.OwnerRoute, request.OwnerPlayerId); err != nil {
		return nil, err
	}
	unlockShard := s.gates.readLock(request.OwnerRoute.LogicalShardId)
	defer unlockShard()
	visitID, expiresAtMs, snapshot, wsErr, err := s.owner.EnterVisitor(
		ctx, request.OwnerPlayerId, request.OwnerRoute.OwnerEpoch, request.VisitorPlayerId,
		request.GateId, request.RequestId,
	)
	switch {
	case errors.Is(err, player.ErrNotOwner):
		return nil, status.Error(codes.FailedPrecondition, "not shard owner")
	case err != nil:
		return nil, status.Error(codes.Internal, "enter visitor failed")
	default:
		return &rpcv1.EnterVisitorResponse{
			VisitId: visitID, ExpiresAtMs: expiresAtMs, Snapshot: snapshot, Error: wsErr,
		}, nil
	}
}

func (s *ownerFarmRPCServer) RefreshVisitorHeartbeat(
	ctx context.Context, request *rpcv1.RefreshVisitorHeartbeatRequest,
) (*rpcv1.RefreshVisitorHeartbeatResponse, error) {
	if request == nil || request.OwnerPlayerId == 0 || request.VisitorPlayerId == 0 ||
		len(request.VisitId) != 16 || request.GateId == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid refresh visitor heartbeat request")
	}
	if err := s.validateRoute(request.OwnerRoute, request.OwnerPlayerId); err != nil {
		return nil, err
	}
	unlockShard := s.gates.readLock(request.OwnerRoute.LogicalShardId)
	defer unlockShard()
	expiresAtMs, wsErr, err := s.owner.RefreshVisitorHeartbeat(
		ctx, request.OwnerPlayerId, request.VisitorPlayerId, request.VisitId, request.GateId,
	)
	if err != nil {
		return nil, status.Error(codes.Internal, "refresh visitor heartbeat failed")
	}
	return &rpcv1.RefreshVisitorHeartbeatResponse{ExpiresAtMs: expiresAtMs, Error: wsErr}, nil
}

func (s *ownerFarmRPCServer) ExitVisitor(
	ctx context.Context, request *rpcv1.ExitVisitorRequest,
) (*rpcv1.ExitVisitorResponse, error) {
	if request == nil || request.OwnerPlayerId == 0 || request.VisitorPlayerId == 0 ||
		len(request.VisitId) != 16 {
		return nil, status.Error(codes.InvalidArgument, "invalid exit visitor request")
	}
	if err := s.validateRoute(request.OwnerRoute, request.OwnerPlayerId); err != nil {
		return nil, err
	}
	unlockShard := s.gates.readLock(request.OwnerRoute.LogicalShardId)
	defer unlockShard()
	wsErr, err := s.owner.ExitVisitor(ctx, request.OwnerPlayerId, request.VisitorPlayerId, request.VisitId)
	if err != nil {
		return nil, status.Error(codes.Internal, "exit visitor failed")
	}
	return &rpcv1.ExitVisitorResponse{Error: wsErr}, nil
}

func (s *ownerFarmRPCServer) GetPublicFarmSnapshot(
	ctx context.Context, request *rpcv1.GetPublicFarmSnapshotRequest,
) (*rpcv1.GetPublicFarmSnapshotResponse, error) {
	if request == nil || request.OwnerPlayerId == 0 || request.VisitorPlayerId == 0 ||
		len(request.VisitId) != 16 {
		return nil, status.Error(codes.InvalidArgument, "invalid get public farm snapshot request")
	}
	if err := s.validateRoute(request.OwnerRoute, request.OwnerPlayerId); err != nil {
		return nil, err
	}
	unlockShard := s.gates.readLock(request.OwnerRoute.LogicalShardId)
	defer unlockShard()
	snapshot, wsErr, err := s.owner.GetPublicFarmSnapshot(
		ctx, request.OwnerPlayerId, request.OwnerRoute.OwnerEpoch, request.VisitorPlayerId, request.VisitId,
	)
	switch {
	case errors.Is(err, player.ErrNotOwner):
		return nil, status.Error(codes.FailedPrecondition, "not shard owner")
	case err != nil:
		return nil, status.Error(codes.Internal, "get public farm snapshot failed")
	default:
		return &rpcv1.GetPublicFarmSnapshotResponse{Snapshot: snapshot, Error: wsErr}, nil
	}
}

// ApplyVisitorAction is the owner-side half of the Phase 5 interaction
// Saga: it fences on the caller's CommittedRoute exactly like every other
// OwnerFarmService method, additionally requires a currently-valid visit
// lease for the (owner, visitor, visit_id) tuple via OwnerService, and then
// applies the one Phase 5 action (STEAL) directly against Runtime. A
// deterministic domain rejection (e.g. STEAL_NOT_AVAILABLE) is returned
// inline on Error with a nil transport error: it must not retry and must
// not read as a Saga-halting failure to the caller.
func (s *ownerFarmRPCServer) ApplyVisitorAction(
	ctx context.Context, request *rpcv1.ApplyVisitorActionRequest,
) (*rpcv1.ApplyVisitorActionResponse, error) {
	if request == nil || request.OwnerPlayerId == 0 || request.VisitorPlayerId == 0 ||
		len(request.VisitId) != 16 || len(request.InteractionId) != 16 {
		return nil, status.Error(codes.InvalidArgument, "invalid apply visitor action request")
	}
	switch request.Action {
	case datav1.FriendInteractionAction_STEAL_FRIEND_CROP,
		datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND,
		datav1.FriendInteractionAction_CATCH_PEST_FOR_FRIEND,
		datav1.FriendInteractionAction_HELP_CLEAN_FRIEND_PLOT:
	default:
		return &rpcv1.ApplyVisitorActionResponse{Error: &wsv1.Error{Code: wsv1.ErrorCode_UNKNOWN_ACTION}}, nil
	}
	if request.Action != datav1.FriendInteractionAction_STEAL_FRIEND_CROP && !s.friendActionsEnabled {
		return &rpcv1.ApplyVisitorActionResponse{Error: &wsv1.Error{Code: wsv1.ErrorCode_UNKNOWN_ACTION}}, nil
	}
	if request.Action == datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND && request.GetPestId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "apply pest requires pest_id")
	}
	if request.PlotId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid apply visitor action request")
	}
	if err := s.validateRoute(request.OwnerRoute, request.OwnerPlayerId); err != nil {
		return nil, err
	}
	if s.runtime == nil {
		return nil, status.Error(codes.Unavailable, "player runtime is unavailable")
	}
	unlockShard := s.gates.readLock(request.OwnerRoute.LogicalShardId)
	defer unlockShard()

	wsErr, err := s.owner.ValidateVisitorAction(request.OwnerPlayerId, request.VisitorPlayerId, request.VisitId)
	if err != nil {
		return nil, status.Error(codes.Internal, "validate visitor action failed")
	}
	if wsErr != nil {
		return &rpcv1.ApplyVisitorActionResponse{Error: wsErr}, nil
	}

	var payload, digest []byte
	var farmPatch *wsv1.FarmViewPatch
	var applyErr error
	switch request.Action {
	case datav1.FriendInteractionAction_STEAL_FRIEND_CROP:
		payload, digest, farmPatch, _, applyErr = s.runtime.ApplyStealOnOwner(
			ctx, request.OwnerPlayerId, request.OwnerRoute.OwnerEpoch, request.VisitorPlayerId,
			request.InteractionId, request.PlotId,
			request.GetExpectedCropItemId(), request.GetFarmViewEpoch(), request.GetFarmViewSeq(),
		)
	case datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND:
		payload, digest, farmPatch, _, applyErr = s.runtime.ApplyApplyPestOnOwner(
			ctx, request.OwnerPlayerId, request.OwnerRoute.OwnerEpoch, request.VisitorPlayerId,
			request.InteractionId, request.PlotId, request.GetPestId(),
		)
	case datav1.FriendInteractionAction_CATCH_PEST_FOR_FRIEND:
		payload, digest, farmPatch, _, applyErr = s.runtime.ApplyCatchPestOnOwner(
			ctx, request.OwnerPlayerId, request.OwnerRoute.OwnerEpoch, request.VisitorPlayerId,
			request.InteractionId, request.PlotId,
		)
	case datav1.FriendInteractionAction_HELP_CLEAN_FRIEND_PLOT:
		payload, digest, farmPatch, _, applyErr = s.runtime.ApplyHelpCleanOnOwner(
			ctx, request.OwnerPlayerId, request.OwnerRoute.OwnerEpoch, request.VisitorPlayerId,
			request.InteractionId, request.PlotId,
		)
	}
	switch {
	case errors.Is(applyErr, player.ErrStealNotAvailable):
		return &rpcv1.ApplyVisitorActionResponse{Error: &wsv1.Error{Code: wsv1.ErrorCode_STEAL_NOT_AVAILABLE}}, nil
	case errors.Is(applyErr, player.ErrPlotNotEligible):
		return &rpcv1.ApplyVisitorActionResponse{Error: &wsv1.Error{Code: wsv1.ErrorCode_PLOT_NOT_ELIGIBLE}}, nil
	case errors.Is(applyErr, player.ErrPestAlreadyPresent):
		return &rpcv1.ApplyVisitorActionResponse{Error: &wsv1.Error{Code: wsv1.ErrorCode_PEST_ALREADY_PRESENT}}, nil
	case errors.Is(applyErr, player.ErrPestSourceForbidden):
		return &rpcv1.ApplyVisitorActionResponse{Error: &wsv1.Error{Code: wsv1.ErrorCode_PEST_SOURCE_FORBIDDEN}}, nil
	case errors.Is(applyErr, player.ErrNotOwner):
		return nil, status.Error(codes.FailedPrecondition, "not shard owner")
	case applyErr != nil:
		return nil, status.Error(codes.Internal, "apply visitor action failed")
	default:
		return &rpcv1.ApplyVisitorActionResponse{
			ResultDigestSha256: digest, ResultPayload: payload, FarmPatch: farmPatch,
		}, nil
	}
}
