package mail

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/gateway"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// ZoneClient resolves the recipient Owner Zone and calls ApplyMailReward.
type ZoneClient struct {
	routes gateway.RouteResolver

	mu       sync.Mutex
	conns    map[string]*grpc.ClientConn
	clients  map[string]rpcv1.PlayerSocialServiceClient
	dialOpts []grpc.DialOption
}

func NewZoneClient(key []byte, routes gateway.RouteResolver) (*ZoneClient, error) {
	if routes == nil {
		return nil, errors.New("route resolver is required")
	}
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{
		Service: "mail", Key: key,
	})
	if err != nil {
		return nil, err
	}
	return &ZoneClient{
		routes: routes,
		conns:  make(map[string]*grpc.ClientConn), clients: make(map[string]rpcv1.PlayerSocialServiceClient),
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

func (c *ZoneClient) ApplyMailReward(
	ctx context.Context,
	playerID uint64,
	claimID []byte,
	mailID string,
	attachments []*tcaplusv1.MailClaimAttachment,
	coinAmount int64,
) (*rpcv1.ApplyMailRewardResponse, error) {
	route, err := c.resolveRoute(ctx, routing.ShardForPlayer(playerID))
	if err != nil {
		return nil, err
	}
	response, err := c.applyOnce(ctx, route, playerID, claimID, mailID, attachments, coinAmount)
	if status.Code(err) == codes.FailedPrecondition {
		if invalidator, ok := c.routes.(gateway.RouteInvalidator); ok {
			invalidator.InvalidateIfVersion(route.ShardID, route.RouteVersion)
		}
		route, err = c.resolveRoute(ctx, routing.ShardForPlayer(playerID))
		if err != nil {
			return nil, err
		}
		return c.applyOnce(ctx, route, playerID, claimID, mailID, attachments, coinAmount)
	}
	return response, err
}

func (c *ZoneClient) applyOnce(
	ctx context.Context,
	route gateway.Route,
	playerID uint64,
	claimID []byte,
	mailID string,
	attachments []*tcaplusv1.MailClaimAttachment,
	coinAmount int64,
) (*rpcv1.ApplyMailRewardResponse, error) {
	client, err := c.client(route.OwnerEndpoint)
	if err != nil {
		return nil, err
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
	}
	reqAttachments := make([]*rpcv1.MailRewardAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		reqAttachments = append(reqAttachments, &rpcv1.MailRewardAttachment{
			ItemId: attachment.GetItemId(), Quantity: attachment.GetQuantity(),
		})
	}
	return client.ApplyMailReward(ctx, &rpcv1.ApplyMailRewardRequest{
		PlayerRoute: &rpcv1.CommittedRoute{
			LogicalShardId: route.ShardID, OwnerZoneId: route.OwnerZoneID,
			OwnerEpoch: route.OwnerEpoch, RouteVersion: route.RouteVersion,
		},
		PlayerId: playerID, ClaimId: claimID, MailId: mailID,
		Attachments: reqAttachments, CoinAmount: coinAmount,
	})
}

func (c *ZoneClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result error
	for target, conn := range c.conns {
		if err := conn.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close Zone %s: %w", target, err))
		}
	}
	c.conns = make(map[string]*grpc.ClientConn)
	c.clients = make(map[string]rpcv1.PlayerSocialServiceClient)
	return result
}

func (c *ZoneClient) client(endpoint string) (rpcv1.PlayerSocialServiceClient, error) {
	target, err := rpcnet.TargetFromHTTPURL(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid Zone gRPC endpoint: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if client := c.clients[target]; client != nil {
		return client, nil
	}
	conn, err := grpc.NewClient(target, c.dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("create Zone gRPC client: %w", err)
	}
	client := rpcv1.NewPlayerSocialServiceClient(conn)
	c.conns[target] = conn
	c.clients[target] = client
	return client, nil
}

func (c *ZoneClient) resolveRoute(ctx context.Context, shardID uint32) (gateway.Route, error) {
	return c.routes.Resolve(ctx, shardID)
}
