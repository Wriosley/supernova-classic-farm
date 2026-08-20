package visit

import (
	"context"
	"errors"
	"fmt"
	"sync"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/gateway"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// OwnerFarmClient is the visitor Zone's view of the Owner Zone's
// OwnerFarmService, used by Service to enter/heartbeat/exit a friend farm
// visit. All three calls carry the domain Error inline (mirroring FriendSvr)
// rather than only a gRPC status, so Service can forward it straight into a
// WsEnvelope.Error without inventing a mapping.
type OwnerFarmClient interface {
	EnterVisitor(
		ctx context.Context,
		ownerPlayerID, visitorPlayerID uint64,
		gateID string,
		relationID []byte,
		requestID string,
	) (visitID []byte, expiresAtMs int64, snapshot *wsv1.FarmVisitSnapshot, wsErr *wsv1.Error, err error)

	RefreshVisitorHeartbeat(
		ctx context.Context,
		ownerPlayerID, visitorPlayerID uint64,
		visitID []byte,
		gateID string,
	) (expiresAtMs int64, wsErr *wsv1.Error, err error)

	ExitVisitor(
		ctx context.Context,
		ownerPlayerID, visitorPlayerID uint64,
		visitID []byte,
	) (wsErr *wsv1.Error, err error)

	// ApplyVisitorAction resolves the owner's current route and calls
	// OwnerFarmService.ApplyVisitorAction, filling in request.OwnerRoute.
	// Unlike the other three methods it takes/returns the raw proto
	// request/response: this exact signature also satisfies
	// interaction.OwnerFarmClient, so *ZoneOwnerFarmClient can be wired
	// directly into the interaction Saga with no adapter.
	ApplyVisitorAction(
		ctx context.Context, request *rpcv1.ApplyVisitorActionRequest,
	) (*rpcv1.ApplyVisitorActionResponse, error)
}

type TargetedOwnerFarmClient interface {
	EnterVisitorAt(context.Context, uint64, uint64, string, string, []byte, string) ([]byte, int64, *wsv1.FarmVisitSnapshot, *wsv1.Error, error)
	RefreshVisitorHeartbeatAt(context.Context, uint64, uint64, []byte, string, string) (int64, *wsv1.Error, error)
}

// ZoneOwnerFarmClient resolves the owner player's shard route through the
// Coordinator and calls OwnerFarmService on whichever Zone currently owns
// it, exactly like friend.ZoneTaskCreditClient does for PlayerSocialService.
type ZoneOwnerFarmClient struct {
	routes gateway.RouteResolver

	mu       sync.Mutex
	conns    map[string]*grpc.ClientConn
	clients  map[string]rpcv1.OwnerFarmServiceClient
	dialOpts []grpc.DialOption
}

func NewZoneOwnerFarmClient(
	key []byte,
	serviceName string,
	routes gateway.RouteResolver,
) (*ZoneOwnerFarmClient, error) {
	if routes == nil {
		return nil, errors.New("route resolver is required")
	}
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{
		Service: serviceName, Key: key,
	})
	if err != nil {
		return nil, err
	}
	return &ZoneOwnerFarmClient{
		routes: routes,
		conns:  make(map[string]*grpc.ClientConn), clients: make(map[string]rpcv1.OwnerFarmServiceClient),
		dialOpts: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithUnaryInterceptor(interceptor),
			grpc.WithDefaultCallOptions(
				grpc.MaxCallSendMsgSize(128<<10),
				grpc.MaxCallRecvMsgSize(128<<10),
			),
		},
	}, nil
}

func (z *ZoneOwnerFarmClient) Close() error {
	if z == nil {
		return nil
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	var result error
	for target, conn := range z.conns {
		if err := conn.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close Owner Zone %s: %w", target, err))
		}
	}
	z.conns = make(map[string]*grpc.ClientConn)
	z.clients = make(map[string]rpcv1.OwnerFarmServiceClient)
	return result
}

func (z *ZoneOwnerFarmClient) EnterVisitor(
	ctx context.Context,
	ownerPlayerID, visitorPlayerID uint64,
	gateID string,
	relationID []byte,
	requestID string,
) ([]byte, int64, *wsv1.FarmVisitSnapshot, *wsv1.Error, error) {
	return z.EnterVisitorAt(ctx, ownerPlayerID, visitorPlayerID, gateID, "http://legacy-gate:8081", relationID, requestID)
}

func (z *ZoneOwnerFarmClient) EnterVisitorAt(
	ctx context.Context, ownerPlayerID, visitorPlayerID uint64, gateID, gateEndpoint string,
	relationID []byte, requestID string,
) ([]byte, int64, *wsv1.FarmVisitSnapshot, *wsv1.Error, error) {
	route, err := z.resolve(ctx, ownerPlayerID)
	if err != nil {
		return nil, 0, nil, nil, err
	}
	response, err := z.enterVisitorOnce(ctx, route, ownerPlayerID, visitorPlayerID, gateID, gateEndpoint, relationID, requestID)
	if status.Code(err) == codes.FailedPrecondition && z.invalidate(route) {
		route, err = z.resolve(ctx, ownerPlayerID)
		if err == nil {
			response, err = z.enterVisitorOnce(ctx, route, ownerPlayerID, visitorPlayerID, gateID, gateEndpoint, relationID, requestID)
		}
	}
	if err != nil {
		return nil, 0, nil, nil, fmt.Errorf("Owner Zone EnterVisitor gRPC call: %w", err)
	}
	return response.GetVisitId(), response.GetExpiresAtMs(), response.GetSnapshot(), response.GetError(), nil
}

func (z *ZoneOwnerFarmClient) enterVisitorOnce(
	ctx context.Context, route gateway.Route, ownerPlayerID, visitorPlayerID uint64,
	gateID, gateEndpoint string, relationID []byte, requestID string,
) (*rpcv1.EnterVisitorResponse, error) {
	client, err := z.client(route.OwnerEndpoint)
	if err != nil {
		return nil, err
	}
	response, err := client.EnterVisitor(ctx, &rpcv1.EnterVisitorRequest{
		OwnerRoute: committedRoute(route), OwnerPlayerId: ownerPlayerID, VisitorPlayerId: visitorPlayerID,
		GateId: gateID, GateEndpoint: gateEndpoint, RelationId: relationID, RequestId: requestID,
	})
	return response, err
}

func (z *ZoneOwnerFarmClient) RefreshVisitorHeartbeat(
	ctx context.Context,
	ownerPlayerID, visitorPlayerID uint64,
	visitID []byte,
	gateID string,
) (int64, *wsv1.Error, error) {
	return z.RefreshVisitorHeartbeatAt(ctx, ownerPlayerID, visitorPlayerID, visitID, gateID, "http://legacy-gate:8081")
}

func (z *ZoneOwnerFarmClient) RefreshVisitorHeartbeatAt(
	ctx context.Context, ownerPlayerID, visitorPlayerID uint64, visitID []byte, gateID, gateEndpoint string,
) (int64, *wsv1.Error, error) {
	route, err := z.resolve(ctx, ownerPlayerID)
	if err != nil {
		return 0, nil, err
	}
	response, err := z.refreshVisitorHeartbeatOnce(ctx, route, ownerPlayerID, visitorPlayerID, visitID, gateID, gateEndpoint)
	if status.Code(err) == codes.FailedPrecondition && z.invalidate(route) {
		route, err = z.resolve(ctx, ownerPlayerID)
		if err == nil {
			response, err = z.refreshVisitorHeartbeatOnce(ctx, route, ownerPlayerID, visitorPlayerID, visitID, gateID, gateEndpoint)
		}
	}
	if err != nil {
		return 0, nil, fmt.Errorf("Owner Zone RefreshVisitorHeartbeat gRPC call: %w", err)
	}
	return response.GetExpiresAtMs(), response.GetError(), nil
}

func (z *ZoneOwnerFarmClient) refreshVisitorHeartbeatOnce(
	ctx context.Context, route gateway.Route, ownerPlayerID, visitorPlayerID uint64,
	visitID []byte, gateID, gateEndpoint string,
) (*rpcv1.RefreshVisitorHeartbeatResponse, error) {
	client, err := z.client(route.OwnerEndpoint)
	if err != nil {
		return nil, err
	}
	response, err := client.RefreshVisitorHeartbeat(ctx, &rpcv1.RefreshVisitorHeartbeatRequest{
		OwnerRoute: committedRoute(route), OwnerPlayerId: ownerPlayerID, VisitorPlayerId: visitorPlayerID,
		VisitId: visitID, GateId: gateID, GateEndpoint: gateEndpoint,
	})
	return response, err
}

func (z *ZoneOwnerFarmClient) ExitVisitor(
	ctx context.Context,
	ownerPlayerID, visitorPlayerID uint64,
	visitID []byte,
) (*wsv1.Error, error) {
	route, err := z.resolve(ctx, ownerPlayerID)
	if err != nil {
		return nil, err
	}
	response, err := z.exitVisitorOnce(ctx, route, ownerPlayerID, visitorPlayerID, visitID)
	if status.Code(err) == codes.FailedPrecondition && z.invalidate(route) {
		route, err = z.resolve(ctx, ownerPlayerID)
		if err == nil {
			response, err = z.exitVisitorOnce(ctx, route, ownerPlayerID, visitorPlayerID, visitID)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("Owner Zone ExitVisitor gRPC call: %w", err)
	}
	return response.GetError(), nil
}

func (z *ZoneOwnerFarmClient) exitVisitorOnce(
	ctx context.Context, route gateway.Route, ownerPlayerID, visitorPlayerID uint64, visitID []byte,
) (*rpcv1.ExitVisitorResponse, error) {
	client, err := z.client(route.OwnerEndpoint)
	if err != nil {
		return nil, err
	}
	response, err := client.ExitVisitor(ctx, &rpcv1.ExitVisitorRequest{
		OwnerRoute: committedRoute(route), OwnerPlayerId: ownerPlayerID, VisitorPlayerId: visitorPlayerID,
		VisitId: visitID,
	})
	return response, err
}

// ApplyVisitorAction resolves request.OwnerPlayerId's current route,
// stamps it onto request.OwnerRoute, and calls the owner Zone's
// OwnerFarmService.ApplyVisitorAction. Every domain outcome (including a
// deterministic STEAL_NOT_AVAILABLE rejection) arrives inline on
// response.Error; only transport/route-resolution failures return a Go
// error, matching the interaction Saga's transport-vs-domain distinction.
func (z *ZoneOwnerFarmClient) ApplyVisitorAction(
	ctx context.Context, request *rpcv1.ApplyVisitorActionRequest,
) (*rpcv1.ApplyVisitorActionResponse, error) {
	if request == nil {
		return nil, errors.New("apply visitor action request is required")
	}
	route, err := z.resolve(ctx, request.OwnerPlayerId)
	if err != nil {
		return nil, err
	}
	response, err := z.applyVisitorActionOnce(ctx, route, request)
	if status.Code(err) == codes.FailedPrecondition && z.invalidate(route) {
		route, err = z.resolve(ctx, request.OwnerPlayerId)
		if err == nil {
			response, err = z.applyVisitorActionOnce(ctx, route, request)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("Owner Zone ApplyVisitorAction gRPC call: %w", err)
	}
	return response, nil
}

func (z *ZoneOwnerFarmClient) applyVisitorActionOnce(
	ctx context.Context, route gateway.Route, request *rpcv1.ApplyVisitorActionRequest,
) (*rpcv1.ApplyVisitorActionResponse, error) {
	client, err := z.client(route.OwnerEndpoint)
	if err != nil {
		return nil, err
	}
	request.OwnerRoute = committedRoute(route)
	return client.ApplyVisitorAction(ctx, request)
}

func (z *ZoneOwnerFarmClient) resolve(
	ctx context.Context, ownerPlayerID uint64,
) (gateway.Route, error) {
	route, err := z.routes.Resolve(ctx, routing.ShardForPlayer(ownerPlayerID))
	if err != nil {
		return gateway.Route{}, fmt.Errorf("resolve Owner farm route: %w", err)
	}
	return route, nil
}

func (z *ZoneOwnerFarmClient) invalidate(route gateway.Route) bool {
	invalidator, ok := z.routes.(gateway.RouteInvalidator)
	if ok {
		invalidator.InvalidateIfVersion(route.ShardID, route.RouteVersion)
	}
	return ok
}

func committedRoute(route gateway.Route) *rpcv1.CommittedRoute {
	return &rpcv1.CommittedRoute{
		LogicalShardId: route.ShardID, OwnerZoneId: route.OwnerZoneID,
		OwnerEpoch: route.OwnerEpoch, RouteVersion: route.RouteVersion,
	}
}

func (z *ZoneOwnerFarmClient) client(endpoint string) (rpcv1.OwnerFarmServiceClient, error) {
	target, err := rpcnet.TargetFromHTTPURL(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid Owner Zone gRPC endpoint: %w", err)
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	if client := z.clients[target]; client != nil {
		return client, nil
	}
	conn, err := grpc.NewClient(target, z.dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("create Owner Zone gRPC client: %w", err)
	}
	client := rpcv1.NewOwnerFarmServiceClient(conn)
	z.conns[target] = conn
	z.clients[target] = client
	return client, nil
}
