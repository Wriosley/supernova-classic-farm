// Package gateway implements the local GateSvr WebSocket boundary.
package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"
)

const (
	ProtocolVersion  uint32 = 1
	MaxMessageBytes         = 64 << 10
	DefaultGatewayID        = "local-gateway"
	DefaultConfigURL        = "http://127.0.0.1:8080/v1/client-config/1"
	// visitCommandTimeout covers ENTER/HEARTBEAT/EXIT plus multi-step
	// interaction Sagas (steal/pest/catch/help) that each do several
	// synchronous Tcaplus SaveCAS round-trips. Keep this above worst-case
	// Tcaplus latency × step count; 3s was too tight under shared-table load.
	visitCommandTimeout  = 15 * time.Second
	friendCommandTimeout = 5 * time.Second
)

var (
	ErrNotOwner      = errors.New("zone is not owner")
	errUnknownAction = errors.New("unknown action")
	defaultConfigSHA = sha256.Sum256([]byte("classicfarm-client-config-v1"))
)

type TicketConsumer interface {
	Consume(context.Context, string) (uint64, error)
}

type Route struct {
	ShardID        uint32
	OwnerZoneID    string
	OwnerEpoch     uint64
	RouteVersion   uint64
	MapVersion     uint64
	LeaseExpiresAt time.Time
	OwnerEndpoint  string
}

type RouteResolver interface {
	Resolve(context.Context, uint32) (Route, error)
}

type ZoneCommander interface {
	Command(context.Context, Route, uint64, []byte) ([]byte, error)
}

type Config struct {
	Tickets           TicketConsumer
	Routes            RouteResolver
	Zone              ZoneCommander
	Visitor           VisitorZoneClient
	Friends           FriendClient
	AuthTimeout       time.Duration
	CommandTimeout    time.Duration
	HeartbeatInterval time.Duration
	ClientConfigURL   string
	ClientConfigSHA   []byte
	Now               func() time.Time
}

type Handler struct {
	tickets           TicketConsumer
	routes            RouteResolver
	zone              ZoneCommander
	visitor           VisitorZoneClient
	friends           FriendClient
	authTimeout       time.Duration
	commandTimeout    time.Duration
	heartbeatInterval time.Duration
	clientConfigURL   string
	clientConfigSHA   []byte
	now               func() time.Time
	pushHub           *PushHub
	failureStats      *commandFailureStats
}

type commandFailureStats struct {
	mu         sync.Mutex
	counts     map[string]uint64
	lastErrors map[string]string
}

func NewHandler(cfg Config) (*Handler, error) {
	if cfg.Tickets == nil || cfg.Routes == nil || cfg.Zone == nil {
		return nil, errors.New("ticket, route, and Zone adapters are required")
	}
	if cfg.AuthTimeout == 0 {
		cfg.AuthTimeout = 10 * time.Second
	}
	if cfg.CommandTimeout == 0 {
		cfg.CommandTimeout = 5 * time.Second
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = 30 * time.Second
	}
	if cfg.ClientConfigURL == "" {
		cfg.ClientConfigURL = DefaultConfigURL
	}
	if cfg.ClientConfigSHA == nil {
		cfg.ClientConfigSHA = defaultConfigSHA[:]
	}
	if len(cfg.ClientConfigSHA) != sha256.Size {
		return nil, fmt.Errorf("client config SHA-256 must be %d bytes", sha256.Size)
	}
	if cfg.AuthTimeout <= 0 || cfg.CommandTimeout <= 0 || cfg.HeartbeatInterval <= 0 {
		return nil, errors.New("timeouts and heartbeat interval must be positive")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Handler{
		tickets: cfg.Tickets, routes: cfg.Routes, zone: cfg.Zone,
		visitor: cfg.Visitor, friends: cfg.Friends,
		authTimeout: cfg.AuthTimeout, commandTimeout: cfg.CommandTimeout,
		heartbeatInterval: cfg.HeartbeatInterval,
		clientConfigURL:   cfg.ClientConfigURL,
		clientConfigSHA:   append([]byte(nil), cfg.ClientConfigSHA...),
		now:               cfg.Now,
		pushHub:           newPushHub(),
		failureStats: &commandFailureStats{
			counts: make(map[string]uint64), lastErrors: make(map[string]string),
		},
	}, nil
}

// DebugCommandFailuresHandler exposes aggregate local diagnostic counters.
func (h *Handler) DebugCommandFailuresHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h.failureStats.mu.Lock()
		counts := make(map[string]uint64, len(h.failureStats.counts))
		for source, count := range h.failureStats.counts {
			counts[source] = count
		}
		lastErrors := make(map[string]string, len(h.failureStats.lastErrors))
		for source, message := range h.failureStats.lastErrors {
			lastErrors[source] = message
		}
		h.failureStats.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Failures   map[string]uint64 `json:"failures"`
			LastErrors map[string]string `json:"last_errors"`
		}{Failures: counts, LastErrors: lastErrors})
	})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"localhost:5173", "127.0.0.1:5173"},
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(MaxMessageBytes)
	defer conn.CloseNow()
	h.serveConnection(r.Context(), conn)
}

type serializedWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *serializedWriter) write(ctx context.Context, body []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.Write(ctx, websocket.MessageBinary, body)
}

func (w *serializedWriter) writeBatch(ctx context.Context, bodies ...[]byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, body := range bodies {
		if err := w.conn.Write(ctx, websocket.MessageBinary, body); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) serveConnection(parent context.Context, conn *websocket.Conn) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	writer := &serializedWriter{conn: conn}
	authDeadline := h.now().Add(h.authTimeout)
	authTimer := time.AfterFunc(h.authTimeout, func() {
		_ = conn.Close(websocket.StatusCode(4401), "authentication timeout")
	})
	defer authTimer.Stop()
	var caller uint64
	var authenticated bool
	var subscription *connectionSubscription
	var workers sync.WaitGroup
	defer func() { h.pushHub.unsubscribe(subscription) }()

	for {
		messageType, body, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusMessageTooBig {
				_ = conn.Close(websocket.StatusMessageTooBig, "message exceeds 64 KiB")
			}
			break
		}
		if messageType != websocket.MessageBinary {
			_ = conn.Close(websocket.StatusProtocolError, "binary protobuf required")
			break
		}
		request := &wsv1.WsEnvelope{}
		if len(body) == 0 || proto.Unmarshal(body, request) != nil {
			_ = conn.Close(websocket.StatusProtocolError, "malformed protobuf")
			break
		}
		if request.ProtocolVersion != ProtocolVersion {
			_ = conn.Close(websocket.StatusCode(4406), "unsupported protocol version")
			break
		}
		if err := validateRequestTuple(request); errors.Is(err, errUnknownAction) {
			if !authenticated {
				_ = conn.Close(websocket.StatusCode(4401), "AUTH must be first non-heartbeat request")
				break
			}
			if writer.write(ctx, marshalResponse(errorResponse(request, wsv1.ErrorCode_UNKNOWN_ACTION, false, h.now))) != nil {
				break
			}
			continue
		} else if err != nil {
			_ = conn.Close(websocket.StatusProtocolError, "invalid envelope tuple")
			break
		}

		if request.Action == wsv1.Action_PING {
			workers.Add(1)
			go func(req *wsv1.WsEnvelope) {
				defer workers.Done()
				_ = writer.write(ctx, marshalResponse(pingResponse(req, h.now)))
			}(request)
			continue
		}

		if !authenticated {
			if request.Action != wsv1.Action_AUTH {
				_ = conn.Close(websocket.StatusCode(4401), "AUTH must be first non-heartbeat request")
				break
			}
			authCtx, authCancel := context.WithDeadline(ctx, authDeadline)
			playerID, err := h.tickets.Consume(authCtx, request.GetAuthRequest().GetWsTicket())
			authCancel()
			if err != nil || playerID == 0 {
				_ = conn.Close(websocket.StatusCode(4401), "invalid authentication ticket")
				break
			}
			caller = playerID
			authenticated = true
			subscription = h.pushHub.subscribe(playerID, writer, ctx)
			authTimer.Stop()
			if writer.write(ctx, marshalResponse(h.authResponse(request, playerID))) != nil {
				break
			}
			continue
		}
		if request.Action == wsv1.Action_AUTH {
			_ = conn.Close(websocket.StatusProtocolError, "AUTH may only occur once")
			break
		}

		workers.Add(1)
		go func(req *wsv1.WsEnvelope, raw []byte) {
			defer workers.Done()
			h.handleGame(ctx, writer, subscription, caller, req, raw)
		}(request, append([]byte(nil), body...))
	}
	cancel()
	workers.Wait()
}

func validateRequestTuple(request *wsv1.WsEnvelope) error {
	if request.MessageKind != wsv1.MessageKind_REQUEST || request.RequestId == "" ||
		request.StateVersion != nil || request.Error != nil || request.Replayed ||
		request.ServerTimeMs != 0 {
		return errors.New("invalid common request fields")
	}
	switch request.Action {
	case wsv1.Action_AUTH:
		if request.TargetPlayerId != 0 || request.GetAuthRequest() == nil ||
			request.GetAuthRequest().GetWsTicket() == "" {
			return errors.New("invalid AUTH")
		}
	case wsv1.Action_PING:
		if request.TargetPlayerId != 0 || request.GetPingRequest() == nil {
			return errors.New("invalid PING")
		}
	case wsv1.Action_GET_PLAYER_SNAPSHOT:
		if request.TargetPlayerId == 0 || request.GetGetPlayerSnapshotRequest() == nil {
			return errors.New("invalid snapshot request")
		}
	case wsv1.Action_GET_SHOP:
		if request.TargetPlayerId == 0 || request.GetGetShopRequest() == nil {
			return errors.New("invalid shop request")
		}
	case wsv1.Action_BUY_SEEDS:
		if request.TargetPlayerId == 0 || request.GetBuySeedsRequest() == nil {
			return errors.New("invalid buy request")
		}
	case wsv1.Action_BUY_FERTILIZER:
		if request.TargetPlayerId == 0 || request.GetBuyFertilizerRequest() == nil {
			return errors.New("invalid buy fertilizer request")
		}
	case wsv1.Action_PLANT:
		if request.TargetPlayerId == 0 || request.GetPlantRequest() == nil {
			return errors.New("invalid plant request")
		}
	case wsv1.Action_APPLY_FERTILIZER:
		if request.TargetPlayerId == 0 || request.GetApplyFertilizerRequest() == nil {
			return errors.New("invalid fertilizer request")
		}
	case wsv1.Action_HARVEST:
		if request.TargetPlayerId == 0 || request.GetHarvestRequest() == nil {
			return errors.New("invalid harvest request")
		}
	case wsv1.Action_CLEAN_PLOT:
		if request.TargetPlayerId == 0 || request.GetCleanPlotRequest() == nil {
			return errors.New("invalid clean request")
		}
	case wsv1.Action_CATCH_PEST:
		if request.TargetPlayerId == 0 || request.GetCatchPestRequest() == nil {
			return errors.New("invalid catch pest request")
		}
	case wsv1.Action_SELL_CROP:
		if request.TargetPlayerId == 0 || request.GetSellCropRequest() == nil {
			return errors.New("invalid sell request")
		}
	case wsv1.Action_CLAIM_CHAPTER_REWARD:
		if request.TargetPlayerId == 0 || request.GetClaimChapterRewardRequest() == nil {
			return errors.New("invalid claim request")
		}
	case wsv1.Action_GET_PET_PANEL:
		if request.TargetPlayerId == 0 || request.GetGetPetPanelRequest() == nil {
			return errors.New("invalid get pet panel request")
		}
	case wsv1.Action_BUY_PET:
		if request.TargetPlayerId == 0 || request.GetBuyPetRequest() == nil {
			return errors.New("invalid buy pet request")
		}
	case wsv1.Action_DEPLOY_PET:
		if request.TargetPlayerId == 0 || request.GetDeployPetRequest() == nil {
			return errors.New("invalid deploy pet request")
		}
	case wsv1.Action_BUY_PET_FOOD:
		if request.TargetPlayerId == 0 || request.GetBuyPetFoodRequest() == nil {
			return errors.New("invalid buy pet food request")
		}
	case wsv1.Action_FEED_PET:
		if request.TargetPlayerId == 0 || request.GetFeedPetRequest() == nil {
			return errors.New("invalid feed pet request")
		}
	case wsv1.Action_CREATE_FRIEND_CODE:
		if request.TargetPlayerId == 0 || request.GetCreateFriendCodeRequest() == nil {
			return errors.New("invalid create friend code request")
		}
	case wsv1.Action_REDEEM_FRIEND_CODE:
		if request.TargetPlayerId == 0 || request.GetRedeemFriendCodeRequest() == nil {
			return errors.New("invalid redeem friend code request")
		}
	case wsv1.Action_LIST_FRIENDS:
		if request.TargetPlayerId == 0 || request.GetListFriendsRequest() == nil {
			return errors.New("invalid list friends request")
		}
	case wsv1.Action_ENTER_FRIEND_FARM:
		if request.TargetPlayerId == 0 || request.GetEnterFriendFarmRequest() == nil {
			return errors.New("invalid enter friend farm request")
		}
	case wsv1.Action_FARM_HEARTBEAT:
		if request.TargetPlayerId == 0 || request.GetFarmHeartbeatRequest() == nil {
			return errors.New("invalid farm heartbeat request")
		}
	case wsv1.Action_EXIT_FRIEND_FARM:
		if request.TargetPlayerId == 0 || request.GetExitFriendFarmRequest() == nil {
			return errors.New("invalid exit friend farm request")
		}
	case wsv1.Action_STEAL_FRIEND_CROP:
		steal := request.GetStealFriendCropRequest()
		if request.TargetPlayerId == 0 || steal == nil || steal.OwnerPlayerId == 0 ||
			len(steal.VisitId) != 16 || steal.PlotId == 0 {
			return errors.New("invalid steal friend crop request")
		}
	case wsv1.Action_APPLY_PEST_TO_FRIEND:
		apply := request.GetApplyPestToFriendRequest()
		if request.TargetPlayerId == 0 || apply == nil || apply.OwnerPlayerId == 0 ||
			len(apply.VisitId) != 16 || apply.PlotId == 0 || apply.PestId == 0 {
			return errors.New("invalid apply pest to friend request")
		}
	case wsv1.Action_CATCH_PEST_FOR_FRIEND:
		catch := request.GetCatchPestForFriendRequest()
		if request.TargetPlayerId == 0 || catch == nil || catch.OwnerPlayerId == 0 ||
			len(catch.VisitId) != 16 || catch.PlotId == 0 {
			return errors.New("invalid catch pest for friend request")
		}
	case wsv1.Action_HELP_CLEAN_FRIEND_PLOT:
		help := request.GetHelpCleanFriendPlotRequest()
		if request.TargetPlayerId == 0 || help == nil || help.OwnerPlayerId == 0 ||
			len(help.VisitId) != 16 || help.PlotId == 0 {
			return errors.New("invalid help clean friend plot request")
		}
	default:
		return errUnknownAction
	}
	return nil
}

func (h *Handler) authResponse(request *wsv1.WsEnvelope, playerID uint64) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: wsv1.Action_AUTH, RequestId: request.RequestId,
		ServerTimeMs: h.now().UnixMilli(),
		Payload: &wsv1.WsEnvelope_AuthResponse{AuthResponse: &wsv1.AuthResponse{
			PlayerId:            playerID,
			HeartbeatIntervalMs: uint32(h.heartbeatInterval / time.Millisecond),
			ClientConfigVersion: 1, ClientConfigUrl: h.clientConfigURL,
			ClientConfigSha256: append([]byte(nil), h.clientConfigSHA...),
			ProtocolMin:        ProtocolVersion, ProtocolMax: ProtocolVersion,
		}},
	}
}

func pingResponse(request *wsv1.WsEnvelope, now func() time.Time) *wsv1.WsEnvelope {
	ping := request.GetPingRequest()
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: wsv1.Action_PING, RequestId: request.RequestId,
		ServerTimeMs: now().UnixMilli(),
		Payload: &wsv1.WsEnvelope_PingResponse{PingResponse: &wsv1.PingResponse{
			PingId: ping.GetPingId(), ClientSentAtMs: ping.GetClientSentAtMs(),
		}},
	}
}

func (h *Handler) handleGame(
	parent context.Context,
	writer *serializedWriter,
	subscription *connectionSubscription,
	caller uint64,
	request *wsv1.WsEnvelope,
	raw []byte,
) {
	if request.TargetPlayerId != caller {
		_ = writer.write(parent, marshalResponse(errorResponse(request, wsv1.ErrorCode_FORBIDDEN, false, h.now)))
		return
	}
	switch request.Action {
	case wsv1.Action_CREATE_FRIEND_CODE, wsv1.Action_REDEEM_FRIEND_CODE, wsv1.Action_LIST_FRIENDS:
		h.handleFriendAction(parent, writer, caller, request)
		return
	case wsv1.Action_ENTER_FRIEND_FARM, wsv1.Action_FARM_HEARTBEAT, wsv1.Action_EXIT_FRIEND_FARM,
		wsv1.Action_STEAL_FRIEND_CROP, wsv1.Action_APPLY_PEST_TO_FRIEND,
		wsv1.Action_CATCH_PEST_FOR_FRIEND, wsv1.Action_HELP_CLEAN_FRIEND_PLOT:
		h.handleVisitAction(parent, writer, caller, request)
		return
	}

	ctx, cancel := context.WithTimeout(parent, h.commandTimeout)
	defer cancel()
	isSnapshot := request.Action == wsv1.Action_GET_PLAYER_SNAPSHOT
	if isSnapshot {
		subscription.beginSnapshot()
		defer subscription.abortSnapshot()
	}
	failureSource := "route_resolve"
	shardID := routing.ShardForPlayer(request.TargetPlayerId)
	route, err := h.routes.Resolve(ctx, shardID)
	if err == nil {
		failureSource = "zone_command"
		var response []byte
		response, err = h.zone.Command(ctx, route, caller, raw)
		var zoneFailure *zoneCommandError
		if errors.As(err, &zoneFailure) {
			failureSource = "zone_command_" + zoneFailure.kind
		}
		if errors.Is(err, ErrNotOwner) {
			if invalidator, ok := h.routes.(RouteInvalidator); ok {
				invalidator.InvalidateIfVersion(shardID, route.RouteVersion)
			}
			route, err = h.routes.Resolve(ctx, shardID)
			if err == nil {
				response, err = h.zone.Command(ctx, route, caller, raw)
			}
		}
		if err == nil {
			failureSource = "zone_response_validation"
			if validateZoneResponse(response, request) == nil {
				if isSnapshot {
					envelope := &wsv1.WsEnvelope{}
					if proto.Unmarshal(response, envelope) != nil || envelope.StateVersion == nil {
						err = errors.New("snapshot response lacks state version")
					} else {
						_ = subscription.finishSnapshot(parent, response, envelope.StateVersion)
						return
					}
				} else {
					_ = writer.write(parent, response)
					return
				}
			}
			if err == nil {
				err = errors.New("invalid Zone response")
			}
		}
	}
	h.recordFailure(failureSource, err)
	_ = writer.write(parent, marshalResponse(errorResponse(request, failureErrorCode(err), true, h.now)))
}

// handleFriendAction routes CREATE_FRIEND_CODE, REDEEM_FRIEND_CODE and
// LIST_FRIENDS straight to FriendSvr: unlike Zone actions there is no Shard
// route to resolve and no NOT_OWNER retry, since FriendSvr is not Sharded.
func (h *Handler) handleFriendAction(
	parent context.Context, writer *serializedWriter, caller uint64, request *wsv1.WsEnvelope,
) {
	if h.friends == nil {
		_ = writer.write(parent, marshalResponse(errorResponse(request, wsv1.ErrorCode_SERVICE_UNAVAILABLE, true, h.now)))
		return
	}
	ctx, cancel := context.WithTimeout(parent, friendCommandTimeout)
	defer cancel()
	failureSource := "friend_command"
	var response []byte
	var err error
	switch request.Action {
	case wsv1.Action_CREATE_FRIEND_CODE:
		response, err = h.friends.CreateCode(ctx, caller, request)
	case wsv1.Action_REDEEM_FRIEND_CODE:
		response, err = h.friends.RedeemCode(ctx, caller, request)
	case wsv1.Action_LIST_FRIENDS:
		response, err = h.friends.List(ctx, caller, request)
	}
	var zoneFailure *zoneCommandError
	if errors.As(err, &zoneFailure) {
		failureSource = "friend_command_" + zoneFailure.kind
	}
	h.writeDomainResult(parent, writer, request, failureSource, response, err)
}

// handleVisitAction routes ENTER_FRIEND_FARM, FARM_HEARTBEAT,
// EXIT_FRIEND_FARM and STEAL_FRIEND_CROP to whichever Zone owns the
// caller's own Shard (that Zone runs VisitorZoneService), retrying once
// after NOT_OWNER exactly like an ordinary game command.
func (h *Handler) handleVisitAction(
	parent context.Context, writer *serializedWriter, caller uint64, request *wsv1.WsEnvelope,
) {
	if h.visitor == nil {
		_ = writer.write(parent, marshalResponse(errorResponse(request, wsv1.ErrorCode_SERVICE_UNAVAILABLE, true, h.now)))
		return
	}
	ctx, cancel := context.WithTimeout(parent, visitCommandTimeout)
	defer cancel()
	failureSource := "route_resolve"
	shardID := routing.ShardForPlayer(request.TargetPlayerId)
	route, err := h.routes.Resolve(ctx, shardID)
	var response []byte
	if err == nil {
		failureSource = "visitor_command"
		response, err = h.callVisitor(ctx, route, caller, request)
		var zoneFailure *zoneCommandError
		if errors.As(err, &zoneFailure) {
			failureSource = "visitor_command_" + zoneFailure.kind
		}
		if errors.Is(err, ErrNotOwner) {
			if invalidator, ok := h.routes.(RouteInvalidator); ok {
				invalidator.InvalidateIfVersion(shardID, route.RouteVersion)
			}
			route, err = h.routes.Resolve(ctx, shardID)
			if err == nil {
				response, err = h.callVisitor(ctx, route, caller, request)
			}
		}
	}
	h.writeDomainResult(parent, writer, request, failureSource, response, err)
}

func (h *Handler) callVisitor(
	ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope,
) ([]byte, error) {
	switch request.Action {
	case wsv1.Action_ENTER_FRIEND_FARM:
		return h.visitor.Enter(ctx, route, caller, request)
	case wsv1.Action_FARM_HEARTBEAT:
		return h.visitor.Heartbeat(ctx, route, caller, request)
	case wsv1.Action_EXIT_FRIEND_FARM:
		return h.visitor.Exit(ctx, route, caller, request)
	case wsv1.Action_STEAL_FRIEND_CROP:
		return h.visitor.Steal(ctx, route, caller, request)
	case wsv1.Action_APPLY_PEST_TO_FRIEND:
		return h.visitor.ApplyPest(ctx, route, caller, request)
	case wsv1.Action_CATCH_PEST_FOR_FRIEND:
		return h.visitor.CatchPest(ctx, route, caller, request)
	case wsv1.Action_HELP_CLEAN_FRIEND_PLOT:
		return h.visitor.HelpClean(ctx, route, caller, request)
	default:
		return nil, errors.New("unsupported visit action")
	}
}

// writeDomainResult shares the response-validation and failure-accounting
// tail of handleFriendAction/handleVisitAction with handleGame's Zone
// command path: a well-formed, correlated response is written as-is,
// otherwise the failure is recorded and a generic retryable error replies.
func (h *Handler) writeDomainResult(
	parent context.Context,
	writer *serializedWriter,
	request *wsv1.WsEnvelope,
	failureSource string,
	response []byte,
	err error,
) {
	if err == nil {
		if validateErr := validateZoneResponse(response, request); validateErr == nil {
			_ = writer.write(parent, response)
			return
		} else {
			failureSource, err = "domain_response_validation", validateErr
		}
	}
	h.recordFailure(failureSource, err)
	_ = writer.write(parent, marshalResponse(errorResponse(request, failureErrorCode(err), true, h.now)))
}

func (h *Handler) recordFailure(source string, err error) {
	h.failureStats.mu.Lock()
	h.failureStats.counts[source]++
	if err != nil {
		h.failureStats.lastErrors[source] = err.Error()
	}
	h.failureStats.mu.Unlock()
}

func failureErrorCode(err error) wsv1.ErrorCode {
	if errors.Is(err, context.DeadlineExceeded) {
		return wsv1.ErrorCode_REQUEST_OUTCOME_UNKNOWN
	}
	return wsv1.ErrorCode_SERVICE_UNAVAILABLE
}

func validateZoneResponse(body []byte, request *wsv1.WsEnvelope) error {
	response := &wsv1.WsEnvelope{}
	if len(body) == 0 || len(body) > MaxMessageBytes || proto.Unmarshal(body, response) != nil {
		return errors.New("malformed Zone response")
	}
	if response.ProtocolVersion != ProtocolVersion ||
		response.MessageKind != wsv1.MessageKind_RESPONSE ||
		response.Action != request.Action ||
		response.RequestId != request.RequestId {
		return errors.New("uncorrelated Zone response")
	}
	return nil
}

func errorResponse(request *wsv1.WsEnvelope, code wsv1.ErrorCode, retryable bool, now func() time.Time) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId,
		TargetPlayerId: request.TargetPlayerId, ServerTimeMs: now().UnixMilli(),
		Error: &wsv1.Error{Code: code, Retryable: retryable},
	}
}

func marshalResponse(response *wsv1.WsEnvelope) []byte {
	body, _ := proto.Marshal(response)
	return body
}
