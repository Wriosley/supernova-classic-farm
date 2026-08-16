package gateway

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	mailv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/mail"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// MailClient routes mailbox WS actions to MailSvr.
type MailClient interface {
	OpenMailbox(ctx context.Context, caller uint64, request *wsv1.WsEnvelope) ([]byte, error)
	MarkMailRead(ctx context.Context, caller uint64, request *wsv1.WsEnvelope) ([]byte, error)
	ClaimMail(ctx context.Context, caller uint64, request *wsv1.WsEnvelope) ([]byte, error)
	CheckMailboxIndicator(ctx context.Context, caller uint64, request *wsv1.WsEnvelope) ([]byte, error)
}

// GRPCMailCommander dials MailSvr as caller "gate".
type GRPCMailCommander struct {
	conn   *grpc.ClientConn
	client mailv1.MailServiceClient
}

func NewGRPCMailCommander(key []byte, endpoint string) (*GRPCMailCommander, error) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("MailSvr endpoint is required")
	}
	target, err := rpcnet.TargetFromHTTPURL(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid MailSvr gRPC endpoint: %w", err)
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
		return nil, fmt.Errorf("create MailSvr gRPC client: %w", err)
	}
	return &GRPCMailCommander{conn: conn, client: mailv1.NewMailServiceClient(conn)}, nil
}

func (c *GRPCMailCommander) OpenMailbox(
	ctx context.Context, caller uint64, request *wsv1.WsEnvelope,
) ([]byte, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("MailSvr gRPC client is not configured")
	}
	open := request.GetOpenMailboxRequest()
	if open == nil {
		return nil, errors.New("invalid OPEN_MAILBOX request")
	}
	response, err := c.client.OpenMailbox(ctx, &mailv1.OpenMailboxRequest{
		PlayerId: caller, PageSize: open.PageSize, PageToken: open.PageToken,
	})
	if err != nil {
		return nil, fmt.Errorf("MailSvr OpenMailbox gRPC call: %w", err)
	}
	return buildDomainResponse(request, response.GetError(), func(envelope *wsv1.WsEnvelope) {
		envelope.Payload = &wsv1.WsEnvelope_OpenMailboxResponse{
			OpenMailboxResponse: &wsv1.OpenMailboxResponse{
				Mails:                 toWSMailViews(response.GetMails()),
				NextPageToken:         response.GetNextPageToken(),
				LastMailboxOpenedAtMs: response.GetLastMailboxOpenedAtMs(),
			},
		}
	})
}

func (c *GRPCMailCommander) MarkMailRead(
	ctx context.Context, caller uint64, request *wsv1.WsEnvelope,
) ([]byte, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("MailSvr gRPC client is not configured")
	}
	mark := request.GetMarkMailReadRequest()
	if mark == nil || mark.MailId == "" {
		return nil, errors.New("invalid MARK_MAIL_READ request")
	}
	response, err := c.client.MarkMailRead(ctx, &mailv1.MarkMailReadRequest{
		PlayerId: caller, MailId: mark.MailId,
	})
	if err != nil {
		return nil, fmt.Errorf("MailSvr MarkMailRead gRPC call: %w", err)
	}
	return buildDomainResponse(request, response.GetError(), func(envelope *wsv1.WsEnvelope) {
		envelope.Payload = &wsv1.WsEnvelope_MarkMailReadResponse{
			MarkMailReadResponse: &wsv1.MarkMailReadResponse{},
		}
	})
}

func (c *GRPCMailCommander) ClaimMail(
	ctx context.Context, caller uint64, request *wsv1.WsEnvelope,
) ([]byte, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("MailSvr gRPC client is not configured")
	}
	claim := request.GetClaimMailRequest()
	if claim == nil || claim.MailId == "" {
		return nil, errors.New("invalid CLAIM_MAIL request")
	}
	claimID, err := parseMailClaimID(request.RequestId)
	if err != nil {
		return nil, err
	}
	response, err := c.client.ClaimMail(ctx, &mailv1.ClaimMailRequest{
		PlayerId: caller, MailId: claim.MailId, ClaimId: claimID,
	})
	if err != nil {
		return nil, fmt.Errorf("MailSvr ClaimMail gRPC call: %w", err)
	}
	return buildDomainResponse(request, response.GetError(), func(envelope *wsv1.WsEnvelope) {
		// A claim mutates the recipient Actor, so H5 needs the version that
		// produced patch. MailSvr omits it when an earlier attempt already
		// completed the claim; H5 then reloads a snapshot instead.
		envelope.StateVersion = response.GetStateVersion()
		envelope.Payload = &wsv1.WsEnvelope_ClaimMailResponse{ClaimMailResponse: &wsv1.ClaimMailResponse{
			MailId: response.GetMailId(), ItemsAdded: response.GetItemsAdded(),
			CoinsAdded: response.GetCoinsAdded(), Patch: response.GetPatch(),
		}}
	})
}

func (c *GRPCMailCommander) CheckMailboxIndicator(
	ctx context.Context, caller uint64, request *wsv1.WsEnvelope,
) ([]byte, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("MailSvr gRPC client is not configured")
	}
	if request.GetCheckMailboxIndicatorRequest() == nil {
		return nil, errors.New("invalid CHECK_MAILBOX_INDICATOR request")
	}
	response, err := c.client.CheckMailboxIndicator(ctx, &mailv1.CheckMailboxIndicatorRequest{
		PlayerId: caller,
	})
	if err != nil {
		return nil, fmt.Errorf("MailSvr CheckMailboxIndicator gRPC call: %w", err)
	}
	return buildDomainResponse(request, response.GetError(), func(envelope *wsv1.WsEnvelope) {
		envelope.Payload = &wsv1.WsEnvelope_CheckMailboxIndicatorResponse{
			CheckMailboxIndicatorResponse: toWSMailboxIndicator(response),
		}
	})
}

func toWSMailboxIndicator(response *mailv1.CheckMailboxIndicatorResponse) *wsv1.CheckMailboxIndicatorResponse {
	if response == nil {
		return &wsv1.CheckMailboxIndicatorResponse{}
	}
	return &wsv1.CheckMailboxIndicatorResponse{
		HasNewMail:   response.GetHasNewMail(),
		NewMailCount: response.GetNewMailCount(),
	}
}

func (c *GRPCMailCommander) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func toWSMailViews(in []*mailv1.MailView) []*wsv1.MailView {
	out := make([]*wsv1.MailView, 0, len(in))
	for _, mail := range in {
		if mail == nil {
			continue
		}
		atts := make([]*wsv1.MailAttachmentView, 0, len(mail.GetAttachments()))
		for _, attachment := range mail.GetAttachments() {
			if attachment == nil {
				continue
			}
			atts = append(atts, &wsv1.MailAttachmentView{
				ItemId: attachment.GetItemId(), Quantity: attachment.GetQuantity(),
				CoinAmount: attachment.GetCoinAmount(),
			})
		}
		out = append(out, &wsv1.MailView{
			MailId:            mail.GetMailId(),
			Kind:              wsv1.MailKind(mail.GetKind()),
			CreatedAtMs:       mail.GetCreatedAtMs(),
			PublishedAtMs:     mail.GetPublishedAtMs(),
			SenderDisplayName: mail.GetSenderDisplayName(),
			SenderPlayerId:    mail.GetSenderPlayerId(),
			RecipientPlayerId: mail.GetRecipientPlayerId(),
			Title:             mail.GetTitle(),
			Content:           mail.GetContent(),
			Attachments:       atts,
			Read:              mail.GetRead(),
			Claimed:           mail.GetClaimed(),
			CoinAmount:        mail.GetCoinAmount(),
		})
	}
	return out
}

func parseMailClaimID(value string) ([]byte, error) {
	compact := strings.ReplaceAll(value, "-", "")
	if len(compact) != 32 {
		return nil, errors.New("request_id must be a UUID")
	}
	decoded, err := hex.DecodeString(compact)
	if err != nil {
		return nil, errors.New("request_id must be a UUID")
	}
	return decoded, nil
}
