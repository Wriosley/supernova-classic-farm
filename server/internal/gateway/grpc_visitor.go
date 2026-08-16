package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// VisitorZoneClient lets Handler route ENTER_FRIEND_FARM, FARM_HEARTBEAT,
// EXIT_FRIEND_FARM and STEAL_FRIEND_CROP to whichever Zone owns the
// caller's own Shard (that Zone runs VisitorZoneService, exactly like
// GameCommandService), returning an already-marshaled RESPONSE WsEnvelope
// correlated to the request's Action and RequestId.
type VisitorZoneClient interface {
	Enter(ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope) ([]byte, error)
	Heartbeat(ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope) ([]byte, error)
	Exit(ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope) ([]byte, error)
	Steal(ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope) ([]byte, error)
	ApplyPest(ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope) ([]byte, error)
	CatchPest(ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope) ([]byte, error)
	HelpClean(ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope) ([]byte, error)
}

// GRPCVisitorZoneCommander dials whichever Zone endpoint Handler resolves
// for the caller's Shard, authenticating as "gate" exactly like
// GRPCZoneCommander does for GameCommandService.
type GRPCVisitorZoneCommander struct {
	mu              sync.Mutex
	conns           map[string]*grpc.ClientConn
	clients         map[string]rpcv1.VisitorZoneServiceClient
	dialOpts        []grpc.DialOption
	gatewayID       string
	gatewayEndpoint string
}

func NewGRPCVisitorZoneCommander(key []byte, gatewayID, gatewayEndpoint string) (*GRPCVisitorZoneCommander, error) {
	if strings.TrimSpace(gatewayID) == "" {
		gatewayID = DefaultGatewayID
	}
	if strings.TrimSpace(gatewayEndpoint) == "" {
		return nil, errors.New("gate advertised endpoint is required")
	}
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{
		Service: "gate",
		Key:     key,
	})
	if err != nil {
		return nil, err
	}
	return &GRPCVisitorZoneCommander{
		conns:   make(map[string]*grpc.ClientConn),
		clients: make(map[string]rpcv1.VisitorZoneServiceClient),
		dialOpts: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithUnaryInterceptor(interceptor),
			grpc.WithDefaultCallOptions(
				grpc.MaxCallSendMsgSize(128<<10),
				grpc.MaxCallRecvMsgSize(128<<10),
			),
		},
		gatewayID: gatewayID, gatewayEndpoint: strings.TrimSpace(gatewayEndpoint),
	}, nil
}

func (z *GRPCVisitorZoneCommander) Enter(
	ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope,
) ([]byte, error) {
	body := request.GetEnterFriendFarmRequest()
	if body == nil || body.OwnerPlayerId == 0 {
		return nil, &zoneCommandError{kind: "request", err: errors.New("invalid ENTER_FRIEND_FARM request")}
	}
	client, err := z.client(route.OwnerEndpoint)
	if err != nil {
		return nil, &zoneCommandError{kind: "target", err: err}
	}
	response, err := client.EnterFriendFarm(ctx, &rpcv1.EnterFriendFarmRequest{
		CallerPlayerId: caller, OwnerPlayerId: body.OwnerPlayerId,
		GateId: z.gatewayID, GateEndpoint: z.gatewayEndpoint, RequestId: request.RequestId,
	})
	if err != nil {
		return nil, visitorGRPCError("EnterFriendFarm", err)
	}
	return buildDomainResponse(request, response.GetError(), func(envelope *wsv1.WsEnvelope) {
		result := response.GetResult()
		envelope.Payload = &wsv1.WsEnvelope_EnterFriendFarmResponse{EnterFriendFarmResponse: &wsv1.EnterFriendFarmResponse{
			VisitId: result.GetVisitId(), ExpiresAtMs: result.GetExpiresAtMs(), Snapshot: result.GetSnapshot(),
		}}
	})
}

func (z *GRPCVisitorZoneCommander) Heartbeat(
	ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope,
) ([]byte, error) {
	body := request.GetFarmHeartbeatRequest()
	if body == nil || body.OwnerPlayerId == 0 || len(body.VisitId) != 16 {
		return nil, &zoneCommandError{kind: "request", err: errors.New("invalid FARM_HEARTBEAT request")}
	}
	client, err := z.client(route.OwnerEndpoint)
	if err != nil {
		return nil, &zoneCommandError{kind: "target", err: err}
	}
	response, err := client.HeartbeatFriendFarm(ctx, &rpcv1.HeartbeatFriendFarmRequest{
		CallerPlayerId: caller, OwnerPlayerId: body.OwnerPlayerId, VisitId: body.VisitId,
		GateId: z.gatewayID, GateEndpoint: z.gatewayEndpoint, RequestId: request.RequestId,
	})
	if err != nil {
		return nil, visitorGRPCError("HeartbeatFriendFarm", err)
	}
	return buildDomainResponse(request, response.GetError(), func(envelope *wsv1.WsEnvelope) {
		envelope.Payload = &wsv1.WsEnvelope_FarmHeartbeatResponse{FarmHeartbeatResponse: &wsv1.FarmHeartbeatResponse{
			ExpiresAtMs: response.GetResult().GetExpiresAtMs(),
		}}
	})
}

func (z *GRPCVisitorZoneCommander) Exit(
	ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope,
) ([]byte, error) {
	body := request.GetExitFriendFarmRequest()
	if body == nil || body.OwnerPlayerId == 0 || len(body.VisitId) != 16 {
		return nil, &zoneCommandError{kind: "request", err: errors.New("invalid EXIT_FRIEND_FARM request")}
	}
	client, err := z.client(route.OwnerEndpoint)
	if err != nil {
		return nil, &zoneCommandError{kind: "target", err: err}
	}
	response, err := client.ExitFriendFarm(ctx, &rpcv1.ExitFriendFarmRequest{
		CallerPlayerId: caller, OwnerPlayerId: body.OwnerPlayerId, VisitId: body.VisitId,
		GateId: z.gatewayID, RequestId: request.RequestId,
	})
	if err != nil {
		return nil, visitorGRPCError("ExitFriendFarm", err)
	}
	return buildDomainResponse(request, response.GetError(), func(envelope *wsv1.WsEnvelope) {
		envelope.Payload = &wsv1.WsEnvelope_ExitFriendFarmResponse{ExitFriendFarmResponse: &wsv1.ExitFriendFarmResponse{}}
	})
}

// Steal drives ExecuteFriendAction with FriendInteractionAction
// STEAL_FRIEND_CROP.
func (z *GRPCVisitorZoneCommander) Steal(
	ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope,
) ([]byte, error) {
	body := request.GetStealFriendCropRequest()
	if body == nil || body.OwnerPlayerId == 0 || len(body.VisitId) != 16 || body.PlotId == 0 ||
		body.ExpectedCropItemId == 0 || len(body.FarmViewEpoch) != 16 {
		return nil, &zoneCommandError{kind: "request", err: errors.New("invalid STEAL_FRIEND_CROP request")}
	}
	return z.executeFriendAction(
		ctx, route, caller, request,
		body.OwnerPlayerId, body.VisitId, body.PlotId, 0,
		body.ExpectedCropItemId, body.FarmViewEpoch, body.FarmViewSeq,
		datav1.FriendInteractionAction_STEAL_FRIEND_CROP,
		func(envelope *wsv1.WsEnvelope, result *wsv1.FriendActionResponse) {
			envelope.Payload = &wsv1.WsEnvelope_StealFriendCropResponse{StealFriendCropResponse: result}
		},
	)
}

// ApplyPest drives ExecuteFriendAction with APPLY_PEST_TO_FRIEND.
func (z *GRPCVisitorZoneCommander) ApplyPest(
	ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope,
) ([]byte, error) {
	body := request.GetApplyPestToFriendRequest()
	if body == nil || body.OwnerPlayerId == 0 || len(body.VisitId) != 16 ||
		body.PlotId == 0 || body.PestId == 0 {
		return nil, &zoneCommandError{kind: "request", err: errors.New("invalid APPLY_PEST_TO_FRIEND request")}
	}
	return z.executeFriendAction(
		ctx, route, caller, request,
		body.OwnerPlayerId, body.VisitId, body.PlotId, body.PestId,
		0, nil, 0,
		datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND,
		func(envelope *wsv1.WsEnvelope, result *wsv1.FriendActionResponse) {
			envelope.Payload = &wsv1.WsEnvelope_ApplyPestToFriendResponse{ApplyPestToFriendResponse: result}
		},
	)
}

// CatchPest drives ExecuteFriendAction with CATCH_PEST_FOR_FRIEND.
func (z *GRPCVisitorZoneCommander) CatchPest(
	ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope,
) ([]byte, error) {
	body := request.GetCatchPestForFriendRequest()
	if body == nil || body.OwnerPlayerId == 0 || len(body.VisitId) != 16 || body.PlotId == 0 {
		return nil, &zoneCommandError{kind: "request", err: errors.New("invalid CATCH_PEST_FOR_FRIEND request")}
	}
	return z.executeFriendAction(
		ctx, route, caller, request,
		body.OwnerPlayerId, body.VisitId, body.PlotId, 0,
		0, nil, 0,
		datav1.FriendInteractionAction_CATCH_PEST_FOR_FRIEND,
		func(envelope *wsv1.WsEnvelope, result *wsv1.FriendActionResponse) {
			envelope.Payload = &wsv1.WsEnvelope_CatchPestForFriendResponse{CatchPestForFriendResponse: result}
		},
	)
}

// HelpClean drives ExecuteFriendAction with HELP_CLEAN_FRIEND_PLOT.
func (z *GRPCVisitorZoneCommander) HelpClean(
	ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope,
) ([]byte, error) {
	body := request.GetHelpCleanFriendPlotRequest()
	if body == nil || body.OwnerPlayerId == 0 || len(body.VisitId) != 16 || body.PlotId == 0 {
		return nil, &zoneCommandError{kind: "request", err: errors.New("invalid HELP_CLEAN_FRIEND_PLOT request")}
	}
	return z.executeFriendAction(
		ctx, route, caller, request,
		body.OwnerPlayerId, body.VisitId, body.PlotId, 0,
		0, nil, 0,
		datav1.FriendInteractionAction_HELP_CLEAN_FRIEND_PLOT,
		func(envelope *wsv1.WsEnvelope, result *wsv1.FriendActionResponse) {
			envelope.Payload = &wsv1.WsEnvelope_HelpCleanFriendPlotResponse{HelpCleanFriendPlotResponse: result}
		},
	)
}

func (z *GRPCVisitorZoneCommander) executeFriendAction(
	ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope,
	ownerPlayerID uint64, visitID []byte, plotID, pestID uint32,
	expectedCropItemID uint32, farmViewEpoch []byte, farmViewSeq uint64,
	action datav1.FriendInteractionAction,
	setPayload func(*wsv1.WsEnvelope, *wsv1.FriendActionResponse),
) ([]byte, error) {
	client, err := z.client(route.OwnerEndpoint)
	if err != nil {
		return nil, &zoneCommandError{kind: "target", err: err}
	}
	rpcRequest := &rpcv1.ExecuteFriendActionRequest{
		CallerPlayerId: caller, OwnerPlayerId: ownerPlayerID, VisitId: visitID,
		GateId: z.gatewayID, RequestId: request.RequestId,
		Action: action, PlotId: plotID,
		ExpectedCropItemId: expectedCropItemID,
		FarmViewEpoch:      farmViewEpoch,
		FarmViewSeq:        farmViewSeq,
	}
	if pestID != 0 {
		rpcRequest.PestId = &pestID
	}
	response, err := client.ExecuteFriendAction(ctx, rpcRequest)
	if err != nil {
		return nil, visitorGRPCError("ExecuteFriendAction", err)
	}
	return buildDomainResponse(request, response.GetError(), func(envelope *wsv1.WsEnvelope) {
		setPayload(envelope, response.GetResult())
	})
}

func (z *GRPCVisitorZoneCommander) Close() error {
	if z == nil {
		return nil
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	var result error
	for target, conn := range z.conns {
		if err := conn.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close Visitor Zone %s: %w", target, err))
		}
	}
	z.conns = make(map[string]*grpc.ClientConn)
	z.clients = make(map[string]rpcv1.VisitorZoneServiceClient)
	return result
}

func (z *GRPCVisitorZoneCommander) client(endpoint string) (rpcv1.VisitorZoneServiceClient, error) {
	target, err := rpcnet.TargetFromHTTPURL(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid Visitor Zone gRPC endpoint: %w", err)
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	if client := z.clients[target]; client != nil {
		return client, nil
	}
	conn, err := grpc.NewClient(target, z.dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("create Visitor Zone gRPC client: %w", err)
	}
	client := rpcv1.NewVisitorZoneServiceClient(conn)
	z.conns[target] = conn
	z.clients[target] = client
	return client, nil
}

// visitorGRPCError mirrors GRPCZoneCommander.Command's status-to-error
// mapping: FailedPrecondition means the resolved Zone is not (or is no
// longer) the caller's Shard owner, which Handler retries once exactly like
// an ordinary game command's NOT_OWNER.
func visitorGRPCError(rpcName string, err error) error {
	if status.Code(err) == codes.FailedPrecondition {
		return ErrNotOwner
	}
	return &zoneCommandError{
		kind: "grpc_" + strings.ToLower(status.Code(err).String()),
		err:  fmt.Errorf("Visitor Zone %s gRPC call: %w", rpcName, err),
	}
}

// buildDomainResponse assembles one correlated RESPONSE WsEnvelope shared by
// VisitorZoneClient and FriendClient implementations: a non-nil domain wsErr
// always wins (mirroring FriendSvr/visit.Service's inline-Error contract),
// otherwise setPayload fills in the successful oneof.
func buildDomainResponse(
	request *wsv1.WsEnvelope, wsErr *wsv1.Error, setPayload func(*wsv1.WsEnvelope),
) ([]byte, error) {
	envelope := &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: request.TargetPlayerId,
		ServerTimeMs: time.Now().UnixMilli(),
	}
	if wsErr != nil {
		envelope.Error = wsErr
	} else if setPayload != nil {
		setPayload(envelope)
	}
	encoded, err := proto.Marshal(envelope)
	if err != nil {
		return nil, &zoneCommandError{kind: "encode", err: err}
	}
	if len(encoded) > MaxMessageBytes {
		return nil, &zoneCommandError{kind: "too_large", err: errors.New("domain response exceeds 64 KiB")}
	}
	return encoded, nil
}
