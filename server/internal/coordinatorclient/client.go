package coordinatorclient

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	coordinatorv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/coordinator"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Config struct {
	Endpoint, SubscriberID                string
	Kind                                  coordinatorv1.SubscriberKind
	HMACKey                               []byte
	DisconnectTTL, MinBackoff, MaxBackoff time.Duration
	Now                                   func() time.Time
	Dialer                                func(context.Context, string) (net.Conn, error)
	OnSnapshot                            func(routing.Snapshot) error
}
type Client struct {
	cfg           Config
	cache         *routeCache
	conn          *grpc.ClientConn
	rpc           coordinatorv1.CoordinatorServiceClient
	currentStream coordinatorv1.CoordinatorService_WatchRoutesClient
	streamCancel  context.CancelFunc
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	startMu       sync.Mutex
	started       bool
	resync        chan struct{}
}

func New(cfg Config) (*Client, error) {
	if cfg.Endpoint == "" || cfg.SubscriberID == "" {
		return nil, errors.New("coordinator endpoint and subscriber ID are required")
	}
	if cfg.Kind == coordinatorv1.SubscriberKind_SUBSCRIBER_KIND_UNSPECIFIED {
		return nil, errors.New("subscriber kind is required")
	}
	if cfg.DisconnectTTL == 0 {
		cfg.DisconnectTTL = 90 * time.Second
	}
	if cfg.MinBackoff == 0 {
		cfg.MinBackoff = 100 * time.Millisecond
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = 5 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.DisconnectTTL <= 0 || cfg.MinBackoff <= 0 || cfg.MaxBackoff < cfg.MinBackoff {
		return nil, errors.New("coordinator client durations are invalid")
	}
	if len(cfg.HMACKey) < 32 {
		return nil, errors.New("coordinator HMAC key is invalid")
	}
	return &Client{cfg: cfg, cache: newRouteCache(cfg.Now, cfg.DisconnectTTL), resync: make(chan struct{}, 1)}, nil
}
func (c *Client) Start(parent context.Context) error {
	c.startMu.Lock()
	defer c.startMu.Unlock()
	if c.started {
		return errors.New("coordinator client already started")
	}
	target, err := rpcnet.TargetFromHTTPURL(c.cfg.Endpoint)
	if err != nil {
		return err
	}
	service := authService(c.cfg)
	unary, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{Service: service, Key: c.cfg.HMACKey})
	if err != nil {
		return err
	}
	stream, err := rpcauth.NewClientStreamInterceptor(rpcauth.ClientConfig{Service: service, Key: c.cfg.HMACKey})
	if err != nil {
		return err
	}
	options := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithUnaryInterceptor(unary), grpc.WithStreamInterceptor(stream), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(8 << 20))}
	if c.cfg.Dialer != nil {
		options = append(options, grpc.WithContextDialer(c.cfg.Dialer))
	}
	conn, err := grpc.DialContext(parent, target, options...)
	if err != nil {
		return err
	}
	c.conn = conn
	c.rpc = coordinatorv1.NewCoordinatorServiceClient(conn)
	c.ctx, c.cancel = context.WithCancel(parent)
	if err := c.syncAndOpen(); err != nil {
		_ = conn.Close()
		c.cancel()
		return err
	}
	c.started = true
	c.wg.Add(1)
	go c.watchLoop()
	return nil
}
func authService(cfg Config) string {
	switch cfg.Kind {
	case coordinatorv1.SubscriberKind_SUBSCRIBER_KIND_GATE:
		return "gate"
	case coordinatorv1.SubscriberKind_SUBSCRIBER_KIND_INFO:
		return "info"
	case coordinatorv1.SubscriberKind_SUBSCRIBER_KIND_ZONE:
		return cfg.SubscriberID
	default:
		return cfg.SubscriberID
	}
}
func (c *Client) ResolvePlayer(playerID uint64) (routing.RouteEntry, error) {
	return c.ResolveShard(routing.ShardForPlayer(playerID))
}
func (c *Client) ResolveShard(shardID uint32) (routing.RouteEntry, error) {
	return c.cache.resolveShard(shardID)
}
func (c *Client) Snapshot() routing.Snapshot { return c.cache.getSnapshot() }
func (c *Client) notifySnapshot() error {
	if c.cfg.OnSnapshot == nil {
		return nil
	}
	return c.cfg.OnSnapshot(c.cache.effectiveSnapshot())
}
func (c *Client) ForceResync() {
	select {
	case c.resync <- struct{}{}:
	default:
	}
}
func (c *Client) Close() error {
	c.startMu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	conn := c.conn
	c.startMu.Unlock()
	c.wg.Wait()
	if conn != nil {
		return conn.Close()
	}
	return nil
}
