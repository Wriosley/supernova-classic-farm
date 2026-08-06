package friend

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
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/internalnet"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ZoneTaskCreditClient implements TaskCreditor by resolving the owning Zone
// for a player's shard through the Coordinator and calling its
// PlayerSocialService with the internal HMAC contract, exactly like Gate's
// GRPCZoneCommander does for game commands.
type ZoneTaskCreditClient struct {
	httpClient     *http.Client
	coordinatorURL string

	mu       sync.Mutex
	conns    map[string]*grpc.ClientConn
	clients  map[string]rpcv1.PlayerSocialServiceClient
	dialOpts []grpc.DialOption
}

func NewZoneTaskCreditClient(
	key []byte,
	coordinatorURL string,
	httpClient *http.Client,
) (*ZoneTaskCreditClient, error) {
	if strings.TrimSpace(coordinatorURL) == "" {
		return nil, errors.New("Coordinator URL is required")
	}
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{
		Service: "friend", Key: key,
	})
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &ZoneTaskCreditClient{
		httpClient: httpClient, coordinatorURL: strings.TrimRight(coordinatorURL, "/"),
		conns: make(map[string]*grpc.ClientConn), clients: make(map[string]rpcv1.PlayerSocialServiceClient),
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

func (z *ZoneTaskCreditClient) Close() error {
	if z == nil {
		return nil
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	var result error
	for target, conn := range z.conns {
		if err := conn.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close Zone %s: %w", target, err))
		}
	}
	z.conns = make(map[string]*grpc.ClientConn)
	z.clients = make(map[string]rpcv1.PlayerSocialServiceClient)
	return result
}

func (z *ZoneTaskCreditClient) ApplyFriendTaskCredit(
	ctx context.Context, playerID uint64, relationID []byte,
) (bool, uint64, error) {
	route, err := z.resolveRoute(ctx, routing.ShardForPlayer(playerID))
	if err != nil {
		return false, 0, fmt.Errorf("resolve friend task credit route: %w", err)
	}
	client, err := z.client(route.ownerEndpoint)
	if err != nil {
		return false, 0, err
	}
	response, err := client.ApplyFriendTaskCredit(ctx, &rpcv1.ApplyFriendTaskCreditRequest{
		PlayerRoute: &rpcv1.CommittedRoute{
			LogicalShardId: route.shardID, OwnerZoneId: route.ownerZoneID,
			OwnerEpoch: route.ownerEpoch, RouteVersion: route.routeVersion,
		},
		PlayerId: playerID, RelationId: relationID,
	})
	if err != nil {
		return false, 0, fmt.Errorf("Zone friend task credit gRPC call: %w", err)
	}
	if response.GetError() != nil {
		return false, 0, fmt.Errorf("Zone friend task credit failed: %s", response.GetError().GetCode())
	}
	return response.GetNewlyApplied(), response.GetPlayerSeq(), nil
}

func (z *ZoneTaskCreditClient) client(endpoint string) (rpcv1.PlayerSocialServiceClient, error) {
	target, err := rpcnet.TargetFromHTTPURL(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid Zone gRPC endpoint: %w", err)
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	if client := z.clients[target]; client != nil {
		return client, nil
	}
	conn, err := grpc.NewClient(target, z.dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("create Zone gRPC client: %w", err)
	}
	client := rpcv1.NewPlayerSocialServiceClient(conn)
	z.conns[target] = conn
	z.clients[target] = client
	return client, nil
}

type friendTargetRoute struct {
	shardID       uint32
	ownerZoneID   string
	ownerEndpoint string
	ownerEpoch    uint64
	routeVersion  uint64
}

// resolveRoute looks up one shard's committed route via the Coordinator's
// GET /internal/v1/routes/{shard}, matching gateway.HTTPRouteResolver.
func (z *ZoneTaskCreditClient) resolveRoute(ctx context.Context, shardID uint32) (friendTargetRoute, error) {
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet,
		z.coordinatorURL+"/internal/v1/routes/"+strconv.FormatUint(uint64(shardID), 10),
		nil,
	)
	if err != nil {
		return friendTargetRoute{}, err
	}
	response, err := z.httpClient.Do(request)
	if err != nil {
		return friendTargetRoute{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return friendTargetRoute{}, fmt.Errorf("route lookup returned %s", response.Status)
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
		return friendTargetRoute{}, fmt.Errorf("decode route response: %w", err)
	}
	epoch, epochErr := strconv.ParseUint(result.OwnerEpoch, 10, 64)
	routeVersion, routeErr := strconv.ParseUint(result.RouteVersion, 10, 64)
	if epochErr != nil || routeErr != nil || epoch == 0 || routeVersion == 0 ||
		result.ShardID != shardID || result.State != string(routing.RouteStateActive) ||
		!result.Routable || result.OwnerZoneID == "" || result.OwnerEndpoint == "" {
		return friendTargetRoute{}, errors.New("route is not a routable ACTIVE owner")
	}
	if err := internalnet.ValidateHTTPURL(result.OwnerEndpoint); err != nil {
		return friendTargetRoute{}, fmt.Errorf("invalid owner endpoint: %w", err)
	}
	return friendTargetRoute{
		shardID: result.ShardID, ownerZoneID: result.OwnerZoneID,
		ownerEndpoint: result.OwnerEndpoint, ownerEpoch: epoch, routeVersion: routeVersion,
	}, nil
}
