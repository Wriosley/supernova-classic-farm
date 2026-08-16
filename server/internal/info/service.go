package info

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"

	infov1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/info"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/gateway"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

const maxRecipientsPerRequest = 256

// FriendLister loads active friends for an owner (FriendSvr ListFriends).
type FriendLister interface {
	ListFriendPlayerIDs(ctx context.Context, ownerPlayerID uint64) ([]uint64, error)
}

// ZoneDispatcher delivers a red-dot batch to one Owner Zone. Implementations
// must map FailedPrecondition to gateway.ErrNotOwner.
type ZoneDispatcher interface {
	DispatchRedDot(
		ctx context.Context,
		route gateway.Route,
		recipientPlayerIDs []uint64,
		redDot *wsv1.RedDotChangedPush,
	) error
}

// Service is the InfoSvr application core: route + fan-out, no red-dot state.
type Service struct {
	infov1.UnimplementedInfoServiceServer

	routes  gateway.RouteResolver
	zones   ZoneDispatcher
	friends FriendLister
	quick   *QuickStore
	logger  *slog.Logger
}

func NewService(
	routes gateway.RouteResolver,
	zones ZoneDispatcher,
	friends FriendLister,
	logger *slog.Logger,
) (*Service, error) {
	if routes == nil || zones == nil {
		return nil, errors.New("route resolver and zone dispatcher are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{routes: routes, zones: zones, friends: friends, quick: NewQuickStore(nil), logger: logger}, nil
}

func (s *Service) UpdatePresenceLease(ctx context.Context, request *infov1.UpdatePresenceLeaseRequest) (*infov1.UpdatePresenceLeaseResponse, error) {
	_ = ctx
	return &infov1.UpdatePresenceLeaseResponse{Applied: s.quick.UpdatePresence(request.GetUpdate())}, nil
}

func (s *Service) BatchRenewPresenceLeases(ctx context.Context, request *infov1.BatchRenewPresenceLeasesRequest) (*infov1.BatchRenewPresenceLeasesResponse, error) {
	_ = ctx
	if len(request.GetUpdates()) > maxRecipientsPerRequest {
		return nil, errors.New("presence batch exceeds limit")
	}
	var applied uint32
	for _, update := range request.GetUpdates() {
		if s.quick.UpdatePresence(update) {
			applied++
		}
	}
	return &infov1.BatchRenewPresenceLeasesResponse{AppliedCount: applied}, nil
}

func (s *Service) UpdateFarmQuickInfo(ctx context.Context, request *infov1.UpdateFarmQuickInfoRequest) (*infov1.UpdateFarmQuickInfoResponse, error) {
	_ = ctx
	return &infov1.UpdateFarmQuickInfoResponse{Applied: s.quick.UpdateFarm(request.GetUpdate())}, nil
}

func (s *Service) BatchGetPlayerQuickInfo(ctx context.Context, request *infov1.BatchGetPlayerQuickInfoRequest) (*infov1.BatchGetPlayerQuickInfoResponse, error) {
	_ = ctx
	if len(request.GetPlayerIds()) > maxRecipientsPerRequest {
		return nil, errors.New("player quick-info batch exceeds limit")
	}
	return &infov1.BatchGetPlayerQuickInfoResponse{Players: s.quick.BatchGetForViewer(request.GetPlayerIds(), request.GetViewerPlayerId())}, nil
}

func (s *Service) RecordOfflineFarmVisit(_ context.Context, request *infov1.RecordOfflineFarmVisitRequest) (*infov1.RecordOfflineFarmVisitResponse, error) {
	if request.GetVisitorPlayerId() == 0 || request.GetOwnerPlayerId() == 0 || request.GetVisitorPlayerId() == request.GetOwnerPlayerId() {
		return nil, errors.New("invalid offline farm visit")
	}
	recorded, revision := s.quick.RecordOfflineFarmVisit(request.GetVisitorPlayerId(), request.GetOwnerPlayerId())
	return &infov1.RecordOfflineFarmVisitResponse{RecordedForOfflineOwner: recorded, SeenCheckpointRevision: revision}, nil
}

func (s *Service) GetOfflineVisitors(_ context.Context, request *infov1.GetOfflineVisitorsRequest) (*infov1.GetOfflineVisitorsResponse, error) {
	if request.GetOwnerPlayerId() == 0 {
		return nil, errors.New("invalid offline visitor owner")
	}
	ids, version, truncated := s.quick.OfflineVisitors(request.GetOwnerPlayerId())
	return &infov1.GetOfflineVisitorsResponse{VisitorPlayerIds: ids, VisitorVersion: version, Truncated: truncated}, nil
}

func (s *Service) AckOfflineVisitors(_ context.Context, request *infov1.AckOfflineVisitorsRequest) (*infov1.AckOfflineVisitorsResponse, error) {
	return &infov1.AckOfflineVisitorsResponse{Applied: s.quick.AckOfflineVisitors(request.GetOwnerPlayerId(), request.GetVisitorVersion())}, nil
}

func (s *Service) ApplyPrivateMailEvent(ctx context.Context, request *infov1.ApplyPrivateMailEventRequest) (*infov1.ApplyPrivateMailEventResponse, error) {
	_ = ctx
	known, count, applied := s.quick.ApplyMailEvent(request.GetPlayerId(), strings.TrimSpace(request.GetMailId()), request.GetCreatedAtMs())
	return &infov1.ApplyPrivateMailEventResponse{Known: known, NewMailCount: count, Applied: applied}, nil
}

func (s *Service) SetMailboxQuickInfo(ctx context.Context, request *infov1.SetMailboxQuickInfoRequest) (*infov1.SetMailboxQuickInfoResponse, error) {
	_ = ctx
	applied := s.quick.SetMailbox(request.GetPlayerId(), request.GetNewMailCount(), request.GetCursorMs(), request.GetCalculatedAtMs())
	return &infov1.SetMailboxQuickInfoResponse{Applied: applied}, nil
}

func (s *Service) AdvancePublicMailWatermark(ctx context.Context, request *infov1.AdvancePublicMailWatermarkRequest) (*infov1.AdvancePublicMailWatermarkResponse, error) {
	_ = ctx
	return &infov1.AdvancePublicMailWatermarkResponse{Applied: s.quick.AdvancePublicWatermark(request.GetPublishedAtMs())}, nil
}

func (s *Service) GetMailboxQuickInfo(ctx context.Context, request *infov1.GetMailboxQuickInfoRequest) (*infov1.GetMailboxQuickInfoResponse, error) {
	_ = ctx
	known, count, cursor, refresh := s.quick.Mailbox(request.GetPlayerId())
	return &infov1.GetMailboxQuickInfoResponse{Known: known, NewMailCount: count, CursorMs: cursor, PublicRefreshRequired: refresh}, nil
}

func (s *Service) SetMailRedDot(
	ctx context.Context, request *infov1.SetMailRedDotRequest,
) (*infov1.SetMailRedDotResponse, error) {
	if request == nil || request.PlayerId == 0 || strings.TrimSpace(request.NotificationId) == "" {
		return &infov1.SetMailRedDotResponse{
			Error: &wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT},
		}, nil
	}
	redDot := &wsv1.RedDotChangedPush{
		NotificationId: request.NotificationId,
		Category:       wsv1.RedDotCategory_RED_DOT_CATEGORY_MAIL,
		Operation:      wsv1.RedDotOperation_RED_DOT_OPERATION_SET,
	}
	s.deliver(ctx, []uint64{request.PlayerId}, redDot)
	return &infov1.SetMailRedDotResponse{}, nil
}

func (s *Service) NotifyOwnerPlotStealable(
	ctx context.Context, request *infov1.NotifyOwnerPlotStealableRequest,
) (*infov1.NotifyOwnerPlotStealableResponse, error) {
	if request == nil || request.OwnerPlayerId == 0 || request.PlotId == 0 ||
		strings.TrimSpace(request.NotificationId) == "" {
		return &infov1.NotifyOwnerPlotStealableResponse{
			Error: &wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT},
		}, nil
	}
	if s.friends == nil {
		return &infov1.NotifyOwnerPlotStealableResponse{}, nil
	}
	friendIDs, err := s.friends.ListFriendPlayerIDs(ctx, request.OwnerPlayerId)
	if err != nil {
		s.logger.Warn("list friends for stealable red dot failed",
			"owner_player_id", request.OwnerPlayerId, "error", err)
		return &infov1.NotifyOwnerPlotStealableResponse{}, nil
	}
	ownerID := request.OwnerPlayerId
	redDot := &wsv1.RedDotChangedPush{
		NotificationId: request.NotificationId,
		Category:       wsv1.RedDotCategory_RED_DOT_CATEGORY_FRIEND_FARM,
		Operation:      wsv1.RedDotOperation_RED_DOT_OPERATION_SET,
		SourcePlayerId: &ownerID,
	}
	s.deliver(ctx, friendIDs, redDot)
	return &infov1.NotifyOwnerPlotStealableResponse{}, nil
}

func (s *Service) deliver(ctx context.Context, recipients []uint64, redDot *wsv1.RedDotChangedPush) {
	recipients = normalizeRecipients(recipients)
	if len(recipients) == 0 || redDot == nil {
		return
	}
	type routeGroup struct {
		route gateway.Route
		ids   []uint64
	}
	byEndpoint := make(map[string]*routeGroup)
	for _, playerID := range recipients {
		route, err := s.routes.Resolve(ctx, routing.ShardForPlayer(playerID))
		if err != nil {
			s.logger.Warn("red dot route resolve failed",
				"player_id", playerID,
				"notification_id", redDot.NotificationId,
				"error", err,
			)
			continue
		}
		group := byEndpoint[route.OwnerEndpoint]
		if group == nil {
			group = &routeGroup{route: route}
			byEndpoint[route.OwnerEndpoint] = group
		}
		group.ids = append(group.ids, playerID)
	}
	for _, group := range byEndpoint {
		s.dispatchWithRetry(ctx, group.route, normalizeRecipients(group.ids), redDot)
	}
}

func (s *Service) dispatchWithRetry(
	ctx context.Context, route gateway.Route, recipients []uint64, redDot *wsv1.RedDotChangedPush,
) {
	if len(recipients) == 0 {
		return
	}
	err := s.zones.DispatchRedDot(ctx, route, recipients, redDot)
	if err == nil {
		return
	}
	if !errors.Is(err, gateway.ErrNotOwner) {
		s.logger.Warn("red dot zone dispatch failed",
			"owner_zone_id", route.OwnerZoneID,
			"notification_id", redDot.NotificationId,
			"recipients", len(recipients),
			"error", err,
		)
		return
	}
	shardID := route.ShardID
	if invalidator, ok := s.routes.(gateway.RouteInvalidator); ok {
		invalidator.InvalidateIfVersion(shardID, route.RouteVersion)
	}
	// Recipients may spread across shards after migration; re-resolve each and
	// retry once with the same notification_id.
	retried := make(map[string]*struct {
		route gateway.Route
		ids   []uint64
	})
	for _, playerID := range recipients {
		next, resolveErr := s.routes.Resolve(ctx, routing.ShardForPlayer(playerID))
		if resolveErr != nil {
			s.logger.Warn("red dot route refresh failed",
				"player_id", playerID,
				"notification_id", redDot.NotificationId,
				"error", resolveErr,
			)
			continue
		}
		group := retried[next.OwnerEndpoint]
		if group == nil {
			group = &struct {
				route gateway.Route
				ids   []uint64
			}{route: next}
			retried[next.OwnerEndpoint] = group
		}
		group.ids = append(group.ids, playerID)
	}
	for _, group := range retried {
		if retryErr := s.zones.DispatchRedDot(ctx, group.route, normalizeRecipients(group.ids), redDot); retryErr != nil {
			s.logger.Warn("red dot zone dispatch retry failed",
				"owner_zone_id", group.route.OwnerZoneID,
				"notification_id", redDot.NotificationId,
				"recipients", len(group.ids),
				"error", retryErr,
			)
		}
	}
}

func normalizeRecipients(ids []uint64) []uint64 {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[uint64]struct{}, len(ids))
	out := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if len(out) > maxRecipientsPerRequest {
		out = out[:maxRecipientsPerRequest]
	}
	return out
}
