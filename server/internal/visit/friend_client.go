package visit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	friendv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/friend"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// FriendChecker abstracts FriendSvr's CheckMutualFriend for Service, mainly
// so tests can stub it without a live FriendSvr process.
type FriendChecker interface {
	CheckMutualFriend(ctx context.Context, playerAID, playerBID uint64) (mutual bool, relationID []byte, err error)
}

// FriendRPCClient calls FriendSvr's FriendService.CheckMutualFriend with the
// internal HMAC contract, exactly like friend.ZoneTaskCreditClient calls
// PlayerSocialService, but against a single fixed endpoint (FriendSvr, unlike
// Zone, is not sharded so there is no Coordinator route to resolve).
type FriendRPCClient struct {
	conn   *grpc.ClientConn
	client friendv1.FriendServiceClient
}

// NewFriendRPCClient dials FriendSvr at endpoint (default FRIEND_RPC_URL
// convention is http://127.0.0.1:8085) using serviceName as the caller
// identity signed into every request (e.g. "zone-local", "zone-a"). A
// dns:/// endpoint resolves every Ready FriendSvr Pod and uses round_robin.
func NewFriendRPCClient(key []byte, serviceName, endpoint string) (*FriendRPCClient, error) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("FriendSvr endpoint is required")
	}
	target, err := rpcnet.TargetFromEndpoint(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid FriendSvr gRPC endpoint: %w", err)
	}
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{
		Service: serviceName, Key: key,
	})
	if err != nil {
		return nil, err
	}
	balancing, err := rpcnet.RoundRobinDialOption(friendv1.FriendService_CheckMutualFriend_FullMethodName)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(interceptor),
		balancing,
		grpc.WithDefaultCallOptions(
			grpc.MaxCallSendMsgSize(128<<10),
			grpc.MaxCallRecvMsgSize(128<<10),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create FriendSvr gRPC client: %w", err)
	}
	return &FriendRPCClient{conn: conn, client: friendv1.NewFriendServiceClient(conn)}, nil
}

func (c *FriendRPCClient) CheckMutualFriend(
	ctx context.Context, playerAID, playerBID uint64,
) (bool, []byte, error) {
	if c == nil || c.client == nil {
		return false, nil, errors.New("FriendSvr gRPC client is not configured")
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	response, err := c.client.CheckMutualFriend(ctx, &friendv1.CheckMutualFriendRequest{
		PlayerAId: playerAID, PlayerBId: playerBID,
	})
	if err != nil {
		return false, nil, fmt.Errorf("FriendSvr CheckMutualFriend gRPC call: %w", err)
	}
	if response.GetError() != nil {
		return false, nil, fmt.Errorf(
			"FriendSvr CheckMutualFriend failed: %s", response.GetError().GetCode(),
		)
	}
	return response.GetMutualFriend(), response.GetRelationId(), nil
}

func (c *FriendRPCClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
