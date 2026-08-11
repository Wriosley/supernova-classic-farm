package mail

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	infov1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/info"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// InfoClient calls InfoSvr SetMailRedDot as caller "mail".
type InfoClient struct {
	client infov1.InfoServiceClient
	conn   *grpc.ClientConn
}

func NewInfoClient(key []byte, endpoint string) (*InfoClient, error) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("info rpc endpoint is required")
	}
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{
		Service: "mail",
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
	return &InfoClient{
		client: infov1.NewInfoServiceClient(conn),
		conn:   conn,
	}, nil
}

func (c *InfoClient) SetMailRedDot(ctx context.Context, playerID uint64, notificationID string) error {
	if c == nil || c.client == nil || playerID == 0 || strings.TrimSpace(notificationID) == "" {
		return errors.New("info mail red-dot client is not configured")
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
	}
	response, err := c.client.SetMailRedDot(ctx, &infov1.SetMailRedDotRequest{
		PlayerId:       playerID,
		NotificationId: notificationID,
	})
	if err != nil {
		return err
	}
	if response.GetError() != nil {
		return fmt.Errorf("set mail red-dot: %s", response.GetError().GetCode().String())
	}
	return nil
}

func (c *InfoClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
