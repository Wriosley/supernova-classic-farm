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
	"google.golang.org/protobuf/proto"
)

// visitorZoneRPCServer implements VisitorZoneService: it runs on the
// visitor's own Zone, so its only ownership check is that this process
// currently owns the caller's shard (no CommittedRoute is supplied by the
// client, unlike game commands, because friend-visit sessions are ephemeral
// and do not need the stronger route-fencing that durable player state does).
type visitorZoneRPCServer struct {
	rpcv1.UnimplementedVisitorZoneServiceServer

	visits        *visit.Service
	owner         interaction.OwnerFarmClient
	authorization ownerAuthorization
	ownZoneID     string

	// runtime applies visitor-side coin effects after owner ApplyVisitorAction
	// succeeds on the direct friend-action path.
	runtime   *player.Runtime
	quickInfo *zoneQuickInfoClient
}

func (s *visitorZoneRPCServer) withQuickInfo(client *zoneQuickInfoClient) *visitorZoneRPCServer {
	s.quickInfo = client
	return s
}

func newVisitorZoneRPCServer(
	visits *visit.Service,
	owner interaction.OwnerFarmClient,
	authorization ownerAuthorization,
	ownZoneID string,
) *visitorZoneRPCServer {
	return &visitorZoneRPCServer{
		visits: visits, owner: owner, authorization: authorization, ownZoneID: ownZoneID,
	}
}

// withRuntime wires the Player Runtime needed for visitor coin side effects
// after a successful owner ApplyVisitorAction.
func (s *visitorZoneRPCServer) withRuntime(runtime *player.Runtime) {
	s.runtime = runtime
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
		ctx, request.CallerPlayerId, request.OwnerPlayerId, request.GateId, request.RequestId, request.GateEndpoint,
	)
	if err != nil {
		return nil, status.Error(codes.Internal, "enter friend farm failed")
	}
	if wsErr == nil && result != nil && s.quickInfo != nil {
		visitor, owner := request.CallerPlayerId, request.OwnerPlayerId
		go func() { _ = s.quickInfo.RecordOfflineFarmVisit(context.Background(), visitor, owner) }()
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
		ctx, request.CallerPlayerId, request.OwnerPlayerId, request.VisitId, request.GateId, request.GateEndpoint,
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

// ExecuteFriendAction handles visitor-side friend actions by calling owner
// ApplyVisitorAction directly (no FriendInteraction Saga), then applying
// visitor coin side effects on this Zone.
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
	if request.Action == datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND && request.GetPestId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "apply pest requires pest_id")
	}
	if request.Action == datav1.FriendInteractionAction_STEAL_FRIEND_CROP &&
		(request.GetExpectedCropItemId() == 0 || len(request.GetFarmViewEpoch()) != 16) {
		return nil, status.Error(codes.InvalidArgument, "steal requires expected_crop_item_id and farm_view_epoch")
	}
	if err := s.authorizeCaller(request.CallerPlayerId); err != nil {
		return nil, err
	}
	interactionID, parseErr := interaction.ParseInteractionID(request.RequestId)
	if parseErr != nil {
		return nil, status.Error(codes.InvalidArgument, "request_id must be a UUID")
	}
	return s.executeFriendActionDirect(ctx, request, interactionID)
}

func (s *visitorZoneRPCServer) executeFriendActionDirect(
	ctx context.Context,
	request *rpcv1.ExecuteFriendActionRequest,
	interactionID []byte,
) (*rpcv1.ExecuteFriendActionResponse, error) {
	if s.owner == nil {
		return nil, status.Error(codes.Unavailable, "owner farm client is unavailable")
	}
	if s.runtime == nil {
		return nil, status.Error(codes.Unavailable, "player runtime is unavailable")
	}
	entry, ok := s.authorization.Entry(routing.ShardForPlayer(request.CallerPlayerId))
	if !ok {
		return nil, status.Error(codes.Unavailable, "ownership is unavailable")
	}
	var ownerResp *rpcv1.ApplyVisitorActionResponse
	ownerPayload, err := s.runtime.AwaitFriendOwnerCall(ctx, request.CallerPlayerId, entry.OwnerEpoch,
		func(ownerCtx context.Context) ([]byte, error) {
			callCtx, cancel := context.WithTimeout(ownerCtx, 3*time.Second)
			defer cancel()
			var callErr error
			ownerResp, callErr = s.owner.ApplyVisitorAction(callCtx, &rpcv1.ApplyVisitorActionRequest{
				OwnerPlayerId: request.OwnerPlayerId, VisitorPlayerId: request.CallerPlayerId,
				VisitId: request.VisitId, InteractionId: interactionID,
				Action: request.Action, PlotId: request.PlotId, PestId: request.PestId,
				ExpectedCropItemId: request.GetExpectedCropItemId(),
				FarmViewEpoch:      request.GetFarmViewEpoch(),
				FarmViewSeq:        request.GetFarmViewSeq(),
			})
			if callErr != nil {
				return nil, callErr
			}
			return ownerResp.GetResultPayload(), nil
		})
	if err != nil {
		if errors.Is(err, player.ErrNotOwner) {
			return nil, status.Error(codes.FailedPrecondition, "not shard owner")
		}
		return nil, status.Error(codes.Internal, "execute friend action failed")
	}
	if ownerResp == nil {
		return nil, status.Error(codes.Internal, "friend owner await returned no response")
	}
	if wsErr := ownerResp.GetError(); wsErr != nil {
		return &rpcv1.ExecuteFriendActionResponse{Error: wsErr}, nil
	}
	visitorResult, _, sideErr := s.runtime.ApplyVisitorFriendSideEffect(
		ctx, request.CallerPlayerId, entry.OwnerEpoch, interactionID, request.Action, ownerPayload,
	)
	if sideErr != nil {
		if errors.Is(sideErr, player.ErrNotOwner) {
			return nil, status.Error(codes.FailedPrecondition, "not shard owner")
		}
		return nil, status.Error(codes.Internal, "apply visitor friend side effect failed")
	}
	result := &wsv1.FriendActionResponse{}
	if visitorResult != nil {
		result = proto.Clone(visitorResult).(*wsv1.FriendActionResponse)
	}
	if result.InteractionId == nil {
		result.InteractionId = append([]byte(nil), interactionID...)
	}
	if len(ownerPayload) > 0 && result.StealGuard == nil {
		ownerDecoded := &wsv1.FriendActionResponse{}
		if unmarshalErr := proto.Unmarshal(ownerPayload, ownerDecoded); unmarshalErr == nil {
			result.StealGuard = ownerDecoded.StealGuard
		}
	}
	result.FarmPatch = ownerResp.GetFarmPatch()
	return &rpcv1.ExecuteFriendActionResponse{Result: result}, nil
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
		request.GateId, request.RequestId, request.GateEndpoint,
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
		ctx, request.OwnerPlayerId, request.VisitorPlayerId, request.VisitId, request.GateId, request.GateEndpoint,
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
	var alreadyApplied bool
	var applyErr error
	switch request.Action {
	case datav1.FriendInteractionAction_STEAL_FRIEND_CROP:
		payload, digest, farmPatch, alreadyApplied, applyErr = s.runtime.ApplyStealOnOwner(
			ctx, request.OwnerPlayerId, request.OwnerRoute.OwnerEpoch, request.VisitorPlayerId,
			request.InteractionId, request.PlotId,
			request.GetExpectedCropItemId(), request.GetFarmViewEpoch(), request.GetFarmViewSeq(),
		)
	case datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND:
		payload, digest, farmPatch, alreadyApplied, applyErr = s.runtime.ApplyApplyPestOnOwner(
			ctx, request.OwnerPlayerId, request.OwnerRoute.OwnerEpoch, request.VisitorPlayerId,
			request.InteractionId, request.PlotId, request.GetPestId(),
		)
	case datav1.FriendInteractionAction_CATCH_PEST_FOR_FRIEND:
		payload, digest, farmPatch, alreadyApplied, applyErr = s.runtime.ApplyCatchPestOnOwner(
			ctx, request.OwnerPlayerId, request.OwnerRoute.OwnerEpoch, request.VisitorPlayerId,
			request.InteractionId, request.PlotId,
		)
	case datav1.FriendInteractionAction_HELP_CLEAN_FRIEND_PLOT:
		payload, digest, farmPatch, alreadyApplied, applyErr = s.runtime.ApplyHelpCleanOnOwner(
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
		if request.Action == datav1.FriendInteractionAction_STEAL_FRIEND_CROP && !alreadyApplied {
			result := &wsv1.FriendActionResponse{}
			if err := proto.Unmarshal(payload, result); err == nil {
				visitorPlayerID := request.VisitorPlayerId
				plotID := request.PlotId
				cropItemID := request.ExpectedCropItemId
				quantity := uint32(1)
				guardTriggered := result.GetStealGuard().GetGuardTriggered()
				guardPenalty := result.GetStealGuard().GetGuardPenaltyConfigured()
				s.owner.PublishFarmEvent(ctx, request.OwnerPlayerId, &wsv1.FarmPresencePush{
					Kind:            wsv1.FarmPresenceKind_FARM_CROP_STOLEN,
					VisitorPlayerId: &visitorPlayerID,
					PlotId:          &plotID,
					CropItemId:      &cropItemID,
					Quantity:        &quantity,
					GuardTriggered:  &guardTriggered,
					GuardPenalty:    &guardPenalty,
				})
			}
		}
		return &rpcv1.ApplyVisitorActionResponse{
			ResultDigestSha256: digest, ResultPayload: payload, FarmPatch: farmPatch,
		}, nil
	}
}
