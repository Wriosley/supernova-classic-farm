package mail

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
	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/internalnet"
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
	httpClient     *http.Client
	coordinatorURL string

	mu       sync.Mutex
	conns    map[string]*grpc.ClientConn
	clients  map[string]rpcv1.PlayerSocialServiceClient
	dialOpts []grpc.DialOption
}

func NewZoneClient(key []byte, coordinatorURL string, httpClient *http.Client) (*ZoneClient, error) {
	if strings.TrimSpace(coordinatorURL) == "" {
		return nil, errors.New("coordinator url is required")
	}
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{
		Service: "mail", Key: key,
	})
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &ZoneClient{
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
	route mailTargetRoute,
	playerID uint64,
	claimID []byte,
	mailID string,
	attachments []*tcaplusv1.MailClaimAttachment,
	coinAmount int64,
) (*rpcv1.ApplyMailRewardResponse, error) {
	client, err := c.client(route.ownerEndpoint)
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
			LogicalShardId: route.shardID, OwnerZoneId: route.ownerZoneID,
			OwnerEpoch: route.ownerEpoch, RouteVersion: route.routeVersion,
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

type mailTargetRoute struct {
	shardID       uint32
	ownerZoneID   string
	ownerEndpoint string
	ownerEpoch    uint64
	routeVersion  uint64
}

func (c *ZoneClient) resolveRoute(ctx context.Context, shardID uint32) (mailTargetRoute, error) {
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet,
		c.coordinatorURL+"/internal/v1/routes/"+strconv.FormatUint(uint64(shardID), 10),
		nil,
	)
	if err != nil {
		return mailTargetRoute{}, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return mailTargetRoute{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return mailTargetRoute{}, fmt.Errorf("route lookup returned %s", response.Status)
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
		return mailTargetRoute{}, fmt.Errorf("decode route response: %w", err)
	}
	epoch, epochErr := strconv.ParseUint(result.OwnerEpoch, 10, 64)
	routeVersion, routeErr := strconv.ParseUint(result.RouteVersion, 10, 64)
	if epochErr != nil || routeErr != nil || epoch == 0 || routeVersion == 0 ||
		result.ShardID != shardID || result.State != string(routing.RouteStateActive) ||
		!result.Routable || result.OwnerZoneID == "" || result.OwnerEndpoint == "" {
		return mailTargetRoute{}, errors.New("route is not a routable ACTIVE owner")
	}
	if err := internalnet.ValidateHTTPURL(result.OwnerEndpoint); err != nil {
		return mailTargetRoute{}, fmt.Errorf("invalid owner endpoint: %w", err)
	}
	return mailTargetRoute{
		shardID: result.ShardID, ownerZoneID: result.OwnerZoneID,
		ownerEndpoint: result.OwnerEndpoint, ownerEpoch: epoch, routeVersion: routeVersion,
	}, nil
}
