package outbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	mailv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/mail"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// MailClient calls MailSvr CreateGiftMail as the zone caller identity.
type MailClient struct {
	client mailv1.MailServiceClient
	conn   *grpc.ClientConn
}

func NewMailClient(key []byte, serviceName, endpoint string) (*MailClient, error) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("mail rpc endpoint is required")
	}
	if strings.TrimSpace(serviceName) == "" {
		return nil, errors.New("mail rpc caller service name is required")
	}
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{
		Service: serviceName,
		Key:     key,
	})
	if err != nil {
		return nil, err
	}
	target, err := rpcnet.TargetFromEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	balancing, err := rpcnet.RoundRobinDialOption()
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
		return nil, err
	}
	return &MailClient{
		client: mailv1.NewMailServiceClient(conn),
		conn:   conn,
	}, nil
}

func (c *MailClient) CreateGiftMail(
	ctx context.Context, request *mailv1.CreateGiftMailRequest,
) (*mailv1.CreateGiftMailResponse, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("mail client is not configured")
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
	}
	response, err := c.client.CreateGiftMail(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("mail CreateGiftMail: %w", err)
	}
	return response, nil
}

func (c *MailClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
