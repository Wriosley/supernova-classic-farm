package reddot

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/gateway"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type ZoneClient struct {
	mu       sync.Mutex
	conns    map[string]*grpc.ClientConn
	clients  map[string]rpcv1.ZoneNotificationServiceClient
	dialOpts []grpc.DialOption
}

func NewZoneClient(key []byte, caller string) (*ZoneClient, error) {
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{Service: caller, Key: key})
	if err != nil {
		return nil, err
	}
	return &ZoneClient{conns: make(map[string]*grpc.ClientConn), clients: make(map[string]rpcv1.ZoneNotificationServiceClient), dialOpts: []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithUnaryInterceptor(interceptor),
		grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(128<<10), grpc.MaxCallRecvMsgSize(128<<10)),
	}}, nil
}

func (c *ZoneClient) DispatchRedDot(ctx context.Context, route gateway.Route, ids []uint64, redDot *wsv1.RedDotChangedPush) error {
	if c == nil || len(ids) == 0 || redDot == nil {
		return errors.New("zone red-dot dispatch is not configured")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
	}
	client, err := c.client(route.OwnerEndpoint)
	if err != nil {
		return err
	}
	_, err = client.DispatchRedDot(ctx, &rpcv1.DispatchRedDotRequest{RecipientRoute: &rpcv1.CommittedRoute{LogicalShardId: route.ShardID, OwnerZoneId: route.OwnerZoneID, OwnerEpoch: route.OwnerEpoch, RouteVersion: route.RouteVersion}, RecipientPlayerIds: ids, RedDot: redDot})
	if status.Code(err) == codes.FailedPrecondition {
		return gateway.ErrNotOwner
	}
	return err
}

func (c *ZoneClient) client(endpoint string) (rpcv1.ZoneNotificationServiceClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing := c.clients[endpoint]; existing != nil {
		return existing, nil
	}
	target, err := rpcnet.TargetFromHTTPURL(endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(target, c.dialOpts...)
	if err != nil {
		return nil, err
	}
	client := rpcv1.NewZoneNotificationServiceClient(conn)
	c.conns[endpoint] = conn
	c.clients[endpoint] = client
	return client, nil
}

func (c *ZoneClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result error
	for endpoint, conn := range c.conns {
		if err := conn.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close %s: %w", endpoint, err))
		}
	}
	return result
}
