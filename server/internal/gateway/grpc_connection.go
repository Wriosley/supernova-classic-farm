package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	connectionRPCTimeout = 2 * time.Second
	ConnectionLeaseTTL   = 90 * time.Second
	ConnectionRefreshEvery = 30 * time.Second
)

// PlayerConnectionClient registers live WebSocket sessions with the Owner Zone.
type PlayerConnectionClient interface {
	Register(ctx context.Context, route Route, playerID uint64, connectionID string, expiresAt time.Time) error
	Refresh(ctx context.Context, route Route, playerID uint64, connectionID string, expiresAt time.Time) error
	Unregister(ctx context.Context, route Route, playerID uint64, connectionID string) error
	Close() error
}

// ErrConnectionNotRegistered means Zone has no matching lease (restart / eviction).
var ErrConnectionNotRegistered = errors.New("player connection not registered")

type GRPCPlayerConnectionClient struct {
	mu        sync.Mutex
	conns     map[string]*grpc.ClientConn
	clients   map[string]rpcv1.PlayerConnectionServiceClient
	dialOpts  []grpc.DialOption
	gatewayID string
}

func NewGRPCPlayerConnectionClient(key []byte, gatewayID string) (*GRPCPlayerConnectionClient, error) {
	if strings.TrimSpace(gatewayID) == "" {
		gatewayID = DefaultGatewayID
	}
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{
		Service: "gate",
		Key:     key,
	})
	if err != nil {
		return nil, err
	}
	return &GRPCPlayerConnectionClient{
		conns:   make(map[string]*grpc.ClientConn),
		clients: make(map[string]rpcv1.PlayerConnectionServiceClient),
		dialOpts: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithUnaryInterceptor(interceptor),
			grpc.WithDefaultCallOptions(
				grpc.MaxCallSendMsgSize(128<<10),
				grpc.MaxCallRecvMsgSize(128<<10),
			),
		},
		gatewayID: gatewayID,
	}, nil
}

func (c *GRPCPlayerConnectionClient) Register(
	ctx context.Context, route Route, playerID uint64, connectionID string, expiresAt time.Time,
) error {
	client, err := c.client(route.OwnerEndpoint)
	if err != nil {
		return err
	}
	_, err = client.RegisterPlayerConnection(ctx, &rpcv1.RegisterPlayerConnectionRequest{
		PlayerId: playerID, GateId: c.gatewayID, ConnectionId: connectionID,
		Route: committedRoute(route), ExpiresAtMs: expiresAt.UnixMilli(),
	})
	return mapConnectionErr(err)
}

func (c *GRPCPlayerConnectionClient) Refresh(
	ctx context.Context, route Route, playerID uint64, connectionID string, expiresAt time.Time,
) error {
	client, err := c.client(route.OwnerEndpoint)
	if err != nil {
		return err
	}
	_, err = client.RefreshPlayerConnection(ctx, &rpcv1.RefreshPlayerConnectionRequest{
		PlayerId: playerID, GateId: c.gatewayID, ConnectionId: connectionID,
		Route: committedRoute(route), ExpiresAtMs: expiresAt.UnixMilli(),
	})
	return mapConnectionErr(err)
}

func (c *GRPCPlayerConnectionClient) Unregister(
	ctx context.Context, route Route, playerID uint64, connectionID string,
) error {
	client, err := c.client(route.OwnerEndpoint)
	if err != nil {
		return err
	}
	_, err = client.UnregisterPlayerConnection(ctx, &rpcv1.UnregisterPlayerConnectionRequest{
		PlayerId: playerID, GateId: c.gatewayID, ConnectionId: connectionID,
		Route: committedRoute(route),
	})
	return mapConnectionErr(err)
}

func (c *GRPCPlayerConnectionClient) Close() error {
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

func (c *GRPCPlayerConnectionClient) client(endpoint string) (rpcv1.PlayerConnectionServiceClient, error) {
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
	client := rpcv1.NewPlayerConnectionServiceClient(conn)
	c.conns[endpoint] = conn
	c.clients[endpoint] = client
	return client, nil
}

func committedRoute(route Route) *rpcv1.CommittedRoute {
	return &rpcv1.CommittedRoute{
		LogicalShardId: route.ShardID,
		OwnerZoneId:    route.OwnerZoneID,
		OwnerEpoch:     route.OwnerEpoch,
		RouteVersion:   route.RouteVersion,
	}
}

func mapConnectionErr(err error) error {
	if err == nil {
		return nil
	}
	switch status.Code(err) {
	case codes.FailedPrecondition:
		return ErrNotOwner
	case codes.NotFound:
		return ErrConnectionNotRegistered
	default:
		return err
	}
}
