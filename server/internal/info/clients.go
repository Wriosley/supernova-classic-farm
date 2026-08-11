package info

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	friendv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/friend"
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

func NewZoneClient(key []byte) (*ZoneClient, error) {
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{
		Service: "info",
		Key:     key,
	})
	if err != nil {
		return nil, err
	}
	return &ZoneClient{
		conns:   make(map[string]*grpc.ClientConn),
		clients: make(map[string]rpcv1.ZoneNotificationServiceClient),
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

func (c *ZoneClient) DispatchRedDot(
	ctx context.Context,
	route gateway.Route,
	recipientPlayerIDs []uint64,
	redDot *wsv1.RedDotChangedPush,
) error {
	if c == nil || len(recipientPlayerIDs) == 0 || redDot == nil {
		return errors.New("zone red-dot dispatch is not configured")
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
	}
	client, err := c.client(route.OwnerEndpoint)
	if err != nil {
		return err
	}
	_, err = client.DispatchRedDot(ctx, &rpcv1.DispatchRedDotRequest{
		RecipientRoute: &rpcv1.CommittedRoute{
			LogicalShardId: route.ShardID,
			OwnerZoneId:    route.OwnerZoneID,
			OwnerEpoch:     route.OwnerEpoch,
			RouteVersion:   route.RouteVersion,
		},
		RecipientPlayerIds: recipientPlayerIDs,
		RedDot:             redDot,
	})
	if status.Code(err) == codes.FailedPrecondition {
		return gateway.ErrNotOwner
	}
	return err
}

func (c *ZoneClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var errs error
	for endpoint, conn := range c.conns {
		if err := conn.Close(); err != nil {
			errs = errors.Join(errs, fmt.Errorf("close %s: %w", endpoint, err))
		}
		delete(c.conns, endpoint)
		delete(c.clients, endpoint)
	}
	return errs
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

type FriendClient struct {
	client friendv1.FriendServiceClient
	conn   *grpc.ClientConn
}

func NewFriendClient(key []byte, endpoint string) (*FriendClient, error) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("friend rpc endpoint is required")
	}
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{
		Service: "info",
		Key:     key,
	})
	if err != nil {
		return nil, err
	}
	target, err := rpcnet.TargetFromHTTPURL(endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(interceptor),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallSendMsgSize(128<<10),
			grpc.MaxCallRecvMsgSize(128<<10),
		),
	)
	if err != nil {
		return nil, err
	}
	return &FriendClient{
		client: friendv1.NewFriendServiceClient(conn),
		conn:   conn,
	}, nil
}

func (c *FriendClient) ListFriendPlayerIDs(ctx context.Context, ownerPlayerID uint64) ([]uint64, error) {
	if c == nil || c.client == nil || ownerPlayerID == 0 {
		return nil, errors.New("friend client is not configured")
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
	}
	response, err := c.client.ListFriends(ctx, &friendv1.ListFriendsRequest{CallerPlayerId: ownerPlayerID})
	if err != nil {
		return nil, err
	}
	if response.GetError() != nil {
		return nil, fmt.Errorf("list friends: %s", response.GetError().GetCode().String())
	}
	out := make([]uint64, 0, len(response.GetFriends()))
	for _, friend := range response.GetFriends() {
		if friend.GetPlayerId() != 0 {
			out = append(out, friend.GetPlayerId())
		}
	}
	return out, nil
}

func (c *FriendClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
