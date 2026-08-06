package visit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/internalnet"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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

// ZoneOwnerFarmClient resolves the owner player's shard route through the
// Coordinator and calls OwnerFarmService on whichever Zone currently owns
// it, exactly like friend.ZoneTaskCreditClient does for PlayerSocialService.
type ZoneOwnerFarmClient struct {
	httpClient     *http.Client
	coordinatorURL string

	mu       sync.Mutex
	conns    map[string]*grpc.ClientConn
	clients  map[string]rpcv1.OwnerFarmServiceClient
	dialOpts []grpc.DialOption
}

func NewZoneOwnerFarmClient(
	key []byte,
	serviceName string,
	coordinatorURL string,
	httpClient *http.Client,
) (*ZoneOwnerFarmClient, error) {
	if strings.TrimSpace(coordinatorURL) == "" {
		return nil, errors.New("Coordinator URL is required")
	}
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{
		Service: serviceName, Key: key,
	})
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &ZoneOwnerFarmClient{
		httpClient: httpClient, coordinatorURL: strings.TrimRight(coordinatorURL, "/"),
		conns: make(map[string]*grpc.ClientConn), clients: make(map[string]rpcv1.OwnerFarmServiceClient),
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
	client, route, err := z.resolve(ctx, ownerPlayerID)
	if err != nil {
		return nil, 0, nil, nil, err
	}
	response, err := client.EnterVisitor(ctx, &rpcv1.EnterVisitorRequest{
		OwnerRoute: route, OwnerPlayerId: ownerPlayerID, VisitorPlayerId: visitorPlayerID,
		GateId: gateID, RelationId: relationID, RequestId: requestID,
	})
	if err != nil {
		return nil, 0, nil, nil, fmt.Errorf("Owner Zone EnterVisitor gRPC call: %w", err)
	}
	return response.GetVisitId(), response.GetExpiresAtMs(), response.GetSnapshot(), response.GetError(), nil
}

func (z *ZoneOwnerFarmClient) RefreshVisitorHeartbeat(
	ctx context.Context,
	ownerPlayerID, visitorPlayerID uint64,
	visitID []byte,
	gateID string,
) (int64, *wsv1.Error, error) {
	client, route, err := z.resolve(ctx, ownerPlayerID)
	if err != nil {
		return 0, nil, err
	}
	response, err := client.RefreshVisitorHeartbeat(ctx, &rpcv1.RefreshVisitorHeartbeatRequest{
		OwnerRoute: route, OwnerPlayerId: ownerPlayerID, VisitorPlayerId: visitorPlayerID,
		VisitId: visitID, GateId: gateID,
	})
	if err != nil {
		return 0, nil, fmt.Errorf("Owner Zone RefreshVisitorHeartbeat gRPC call: %w", err)
	}
	return response.GetExpiresAtMs(), response.GetError(), nil
}

func (z *ZoneOwnerFarmClient) ExitVisitor(
	ctx context.Context,
	ownerPlayerID, visitorPlayerID uint64,
	visitID []byte,
) (*wsv1.Error, error) {
	client, route, err := z.resolve(ctx, ownerPlayerID)
	if err != nil {
		return nil, err
	}
	response, err := client.ExitVisitor(ctx, &rpcv1.ExitVisitorRequest{
		OwnerRoute: route, OwnerPlayerId: ownerPlayerID, VisitorPlayerId: visitorPlayerID,
		VisitId: visitID,
	})
	if err != nil {
		return nil, fmt.Errorf("Owner Zone ExitVisitor gRPC call: %w", err)
	}
	return response.GetError(), nil
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
	client, route, err := z.resolve(ctx, request.OwnerPlayerId)
	if err != nil {
		return nil, err
	}
	request.OwnerRoute = route
	response, err := client.ApplyVisitorAction(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("Owner Zone ApplyVisitorAction gRPC call: %w", err)
	}
	return response, nil
}

func (z *ZoneOwnerFarmClient) resolve(
	ctx context.Context, ownerPlayerID uint64,
) (rpcv1.OwnerFarmServiceClient, *rpcv1.CommittedRoute, error) {
	route, err := z.resolveRoute(ctx, routing.ShardForPlayer(ownerPlayerID))
	if err != nil {
		return nil, nil, fmt.Errorf("resolve Owner farm route: %w", err)
	}
	client, err := z.client(route.ownerEndpoint)
	if err != nil {
		return nil, nil, err
	}
	return client, &rpcv1.CommittedRoute{
		LogicalShardId: route.shardID, OwnerZoneId: route.ownerZoneID,
		OwnerEpoch: route.ownerEpoch, RouteVersion: route.routeVersion,
	}, nil
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

type ownerTargetRoute struct {
	shardID       uint32
	ownerZoneID   string
	ownerEndpoint string
	ownerEpoch    uint64
	routeVersion  uint64
}

// resolveRoute mirrors friend.ZoneTaskCreditClient.resolveRoute exactly: it
// looks up one shard's committed route via the Coordinator's
// GET /internal/v1/routes/{shard}.
func (z *ZoneOwnerFarmClient) resolveRoute(ctx context.Context, shardID uint32) (ownerTargetRoute, error) {
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet,
		z.coordinatorURL+"/internal/v1/routes/"+strconv.FormatUint(uint64(shardID), 10),
		nil,
	)
	if err != nil {
		return ownerTargetRoute{}, err
	}
	response, err := z.httpClient.Do(request)
	if err != nil {
		return ownerTargetRoute{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return ownerTargetRoute{}, fmt.Errorf("route lookup returned %s", response.Status)
	}
	var result struct {
		ShardID       uint32 `json:"shard_id"`
		OwnerZoneID   string `json:"owner_zone_id"`
		OwnerEndpoint string `json:"owner_endpoint"`
		OwnerEpoch    string `json:"owner_epoch"`
		RouteVersion  string `json:"route_version"`
		State         string `json:"state"`
		Routable      bool   `json:"routable"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 16<<10))
	if err := decoder.Decode(&result); err != nil {
		return ownerTargetRoute{}, fmt.Errorf("decode route response: %w", err)
	}
	epoch, epochErr := strconv.ParseUint(result.OwnerEpoch, 10, 64)
	routeVersion, routeErr := strconv.ParseUint(result.RouteVersion, 10, 64)
	if epochErr != nil || routeErr != nil || epoch == 0 || routeVersion == 0 ||
		result.ShardID != shardID || result.State != string(routing.RouteStateActive) ||
		!result.Routable || result.OwnerZoneID == "" || result.OwnerEndpoint == "" {
		return ownerTargetRoute{}, errors.New("route is not a routable ACTIVE owner")
	}
	if err := internalnet.ValidateHTTPURL(result.OwnerEndpoint); err != nil {
		return ownerTargetRoute{}, fmt.Errorf("invalid owner endpoint: %w", err)
	}
	return ownerTargetRoute{
		shardID: result.ShardID, ownerZoneID: result.OwnerZoneID,
		ownerEndpoint: result.OwnerEndpoint, ownerEpoch: epoch, routeVersion: routeVersion,
	}, nil
}
