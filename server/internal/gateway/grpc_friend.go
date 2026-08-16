package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"

	friendv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/friend"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// FriendClient lets Handler route CREATE_FRIEND_CODE, REDEEM_FRIEND_CODE and
// LIST_FRIENDS straight to FriendSvr (which is not Sharded, unlike Zone), so
// there is no route resolution or NOT_OWNER retry on this path.
type FriendClient interface {
	CreateCode(ctx context.Context, caller uint64, request *wsv1.WsEnvelope) ([]byte, error)
	RedeemCode(ctx context.Context, caller uint64, request *wsv1.WsEnvelope) ([]byte, error)
	List(ctx context.Context, caller uint64, request *wsv1.WsEnvelope) ([]byte, error)
	GetOfflineVisitors(ctx context.Context, caller uint64, request *wsv1.WsEnvelope) ([]byte, error)
	AckOfflineVisitors(ctx context.Context, caller uint64, request *wsv1.WsEnvelope) ([]byte, error)
	CheckMutualFriend(ctx context.Context, playerAID, playerBID uint64) (mutual bool, err error)
}

func (c *GRPCFriendCommander) GetOfflineVisitors(ctx context.Context, caller uint64, request *wsv1.WsEnvelope) ([]byte, error) {
	response, err := c.client.GetOfflineVisitors(ctx, &friendv1.GetOfflineVisitorsRequest{CallerPlayerId: caller})
	if err != nil {
		return nil, fmt.Errorf("FriendSvr GetOfflineVisitors gRPC call: %w", err)
	}
	views := make([]*wsv1.FriendView, 0, len(response.GetVisitors()))
	for _, visitor := range response.GetVisitors() {
		views = append(views, friendView(visitor))
	}
	return buildDomainResponse(request, response.GetError(), func(envelope *wsv1.WsEnvelope) {
		envelope.Payload = &wsv1.WsEnvelope_GetOfflineVisitorsResponse{GetOfflineVisitorsResponse: &wsv1.GetOfflineVisitorsResponse{Visitors: views, VisitorVersion: response.GetVisitorVersion(), Truncated: response.GetTruncated()}}
	})
}

func (c *GRPCFriendCommander) AckOfflineVisitors(ctx context.Context, caller uint64, request *wsv1.WsEnvelope) ([]byte, error) {
	body := request.GetAckOfflineVisitorsRequest()
	response, err := c.client.AckOfflineVisitors(ctx, &friendv1.AckOfflineVisitorsRequest{CallerPlayerId: caller, VisitorVersion: body.GetVisitorVersion()})
	if err != nil {
		return nil, fmt.Errorf("FriendSvr AckOfflineVisitors gRPC call: %w", err)
	}
	return buildDomainResponse(request, response.GetError(), func(envelope *wsv1.WsEnvelope) {
		envelope.Payload = &wsv1.WsEnvelope_AckOfflineVisitorsResponse{AckOfflineVisitorsResponse: &wsv1.AckOfflineVisitorsResponse{Applied: response.GetApplied()}}
	})
}

// GRPCFriendCommander dials FriendSvr's FriendService using the internal
// HMAC contract, authenticating as "gate" (FriendSvr's allowlist for these
// three RPCs is exactly {"gate"}; see server/cmd/friend/main.go).
type GRPCFriendCommander struct {
	conn   *grpc.ClientConn
	client friendv1.FriendServiceClient
}

func NewGRPCFriendCommander(key []byte, endpoint string) (*GRPCFriendCommander, error) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("FriendSvr endpoint is required")
	}
	target, err := rpcnet.TargetFromHTTPURL(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid FriendSvr gRPC endpoint: %w", err)
	}
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{
		Service: "gate", Key: key,
	})
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
		return nil, fmt.Errorf("create FriendSvr gRPC client: %w", err)
	}
	return &GRPCFriendCommander{conn: conn, client: friendv1.NewFriendServiceClient(conn)}, nil
}

func (c *GRPCFriendCommander) CreateCode(
	ctx context.Context, caller uint64, request *wsv1.WsEnvelope,
) ([]byte, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("FriendSvr gRPC client is not configured")
	}
	response, err := c.client.CreateShareCode(ctx, &friendv1.CreateShareCodeRequest{CallerPlayerId: caller})
	if err != nil {
		return nil, fmt.Errorf("FriendSvr CreateShareCode gRPC call: %w", err)
	}
	return buildDomainResponse(request, response.GetError(), func(envelope *wsv1.WsEnvelope) {
		envelope.Payload = &wsv1.WsEnvelope_CreateFriendCodeResponse{CreateFriendCodeResponse: &wsv1.CreateFriendCodeResponse{
			Code: response.GetCode(), CreatedAtMs: response.GetCreatedAtMs(), ExpiresAtMs: response.GetExpiresAtMs(),
			ShareUrl: response.GetShareUrl(),
		}}
	})
}

func (c *GRPCFriendCommander) RedeemCode(
	ctx context.Context, caller uint64, request *wsv1.WsEnvelope,
) ([]byte, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("FriendSvr gRPC client is not configured")
	}
	body := request.GetRedeemFriendCodeRequest()
	if body == nil || body.Code == "" {
		return nil, errors.New("invalid REDEEM_FRIEND_CODE request")
	}
	response, err := c.client.RedeemShareCode(ctx, &friendv1.RedeemShareCodeRequest{
		CallerPlayerId: caller, Code: body.Code,
	})
	if err != nil {
		return nil, fmt.Errorf("FriendSvr RedeemShareCode gRPC call: %w", err)
	}
	return buildDomainResponse(request, response.GetError(), func(envelope *wsv1.WsEnvelope) {
		envelope.Payload = &wsv1.WsEnvelope_RedeemFriendCodeResponse{RedeemFriendCodeResponse: &wsv1.RedeemFriendCodeResponse{
			Friend: friendView(response.GetFriend()), NewlyCreated: response.GetNewlyCreated(),
		}}
	})
}

func (c *GRPCFriendCommander) List(
	ctx context.Context, caller uint64, request *wsv1.WsEnvelope,
) ([]byte, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("FriendSvr gRPC client is not configured")
	}
	response, err := c.client.ListFriends(ctx, &friendv1.ListFriendsRequest{CallerPlayerId: caller})
	if err != nil {
		return nil, fmt.Errorf("FriendSvr ListFriends gRPC call: %w", err)
	}
	views := make([]*wsv1.FriendView, 0, len(response.GetFriends()))
	for _, friend := range response.GetFriends() {
		views = append(views, friendView(friend))
	}
	return buildDomainResponse(request, response.GetError(), func(envelope *wsv1.WsEnvelope) {
		envelope.Payload = &wsv1.WsEnvelope_ListFriendsResponse{ListFriendsResponse: &wsv1.ListFriendsResponse{
			Friends: views,
		}}
	})
}

func (c *GRPCFriendCommander) CheckMutualFriend(
	ctx context.Context, playerAID, playerBID uint64,
) (bool, error) {
	if c == nil || c.client == nil {
		return false, errors.New("FriendSvr gRPC client is not configured")
	}
	response, err := c.client.CheckMutualFriend(ctx, &friendv1.CheckMutualFriendRequest{
		PlayerAId: playerAID, PlayerBId: playerBID,
	})
	if err != nil {
		return false, fmt.Errorf("FriendSvr CheckMutualFriend gRPC call: %w", err)
	}
	if response.GetError() != nil {
		return false, fmt.Errorf("FriendSvr CheckMutualFriend: %s", response.GetError().GetCode().String())
	}
	return response.GetMutualFriend(), nil
}

func (c *GRPCFriendCommander) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func friendView(view *friendv1.FriendView) *wsv1.FriendView {
	if view == nil {
		return nil
	}
	return &wsv1.FriendView{
		PlayerId: view.PlayerId, AccountName: view.AccountName, CreatedAtMs: view.CreatedAtMs,
		PresenceKnown: view.PresenceKnown, Online: view.Online, LastSeenAtMs: view.LastSeenAtMs,
		FarmSummaryKnown: view.FarmSummaryKnown, EarliestMatureAtMs: view.EarliestMatureAtMs,
		MayHaveStealableCrop: view.MayHaveStealableCrop,
	}
}
