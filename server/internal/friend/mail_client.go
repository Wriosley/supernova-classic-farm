package friend

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

// MailClient implements RewardMailer via MailSvr CreateSystemRewardMail.
type MailClient struct {
	client mailv1.MailServiceClient
	conn   *grpc.ClientConn
}

func NewMailClient(key []byte, endpoint string) (*MailClient, error) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("mail rpc endpoint is required")
	}
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{
		Service: "friend",
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
	return &MailClient{
		client: mailv1.NewMailServiceClient(conn),
		conn:   conn,
	}, nil
}

func (c *MailClient) CreateSystemRewardMail(
	ctx context.Context,
	sourceEventID string,
	recipientPlayerID uint64,
	title, content, senderDisplayName string,
	attachments []RewardMailAttachment,
	coinAmount int64,
) (string, bool, error) {
	if c == nil || c.client == nil {
		return "", false, errors.New("mail reward client is not configured")
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
	}
	views := make([]*mailv1.MailAttachmentView, 0, len(attachments))
	for _, attachment := range attachments {
		views = append(views, &mailv1.MailAttachmentView{
			ItemId: attachment.ItemID, Quantity: attachment.Quantity,
		})
	}
	response, err := c.client.CreateSystemRewardMail(ctx, &mailv1.CreateSystemRewardMailRequest{
		SourceEventId:     sourceEventID,
		RecipientPlayerId: recipientPlayerID,
		Title:             title,
		Content:           content,
		SenderDisplayName: senderDisplayName,
		Attachments:       views,
		CoinAmount:        coinAmount,
	})
	if err != nil {
		return "", false, err
	}
	if response.GetError() != nil {
		return "", false, fmt.Errorf(
			"create system reward mail: %s", response.GetError().GetCode().String(),
		)
	}
	return response.GetMailId(), response.GetAlreadyApplied(), nil
}

func (c *MailClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
