package friend

import (
	"context"
	"time"

	infov1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/info"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type InfoQuickClient struct {
	client infov1.InfoServiceClient
	conn   *grpc.ClientConn
}

func NewInfoQuickClient(key []byte, endpoint string) (*InfoQuickClient, error) {
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{Service: "friend", Key: key})
	if err != nil {
		return nil, err
	}
	target, err := rpcnet.TargetFromHTTPURL(endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithUnaryInterceptor(interceptor), grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(256<<10), grpc.MaxCallRecvMsgSize(256<<10)))
	if err != nil {
		return nil, err
	}
	return &InfoQuickClient{client: infov1.NewInfoServiceClient(conn), conn: conn}, nil
}

func (c *InfoQuickClient) BatchGet(ctx context.Context, ids []uint64, viewer uint64) ([]*infov1.PlayerQuickInfo, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
	}
	response, err := c.client.BatchGetPlayerQuickInfo(ctx, &infov1.BatchGetPlayerQuickInfoRequest{PlayerIds: ids, ViewerPlayerId: viewer})
	if err != nil {
		return nil, err
	}
	return response.GetPlayers(), nil
}
func (c *InfoQuickClient) GetOfflineVisitors(ctx context.Context, owner uint64) ([]uint64, uint64, bool, error) {
	response, err := c.client.GetOfflineVisitors(ctx, &infov1.GetOfflineVisitorsRequest{OwnerPlayerId: owner})
	if err != nil {
		return nil, 0, false, err
	}
	return response.GetVisitorPlayerIds(), response.GetVisitorVersion(), response.GetTruncated(), nil
}
func (c *InfoQuickClient) AckOfflineVisitors(ctx context.Context, owner, version uint64) (bool, error) {
	response, err := c.client.AckOfflineVisitors(ctx, &infov1.AckOfflineVisitorsRequest{OwnerPlayerId: owner, VisitorVersion: version})
	if err != nil {
		return false, err
	}
	return response.GetApplied(), nil
}
func (c *InfoQuickClient) Close() error { return c.conn.Close() }
