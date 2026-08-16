package mail

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	mailv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/mail"
	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
)

// RedDotNotifier emits a best-effort mail indicator. Failures must not roll back mail writes.
type RedDotNotifier interface {
	SetMailRedDot(ctx context.Context, playerID uint64, notificationID string, count uint32) error
}

type MailboxQuickCache interface {
	ApplyMailEvent(context.Context, uint64, string, int64) (bool, uint32, error)
	SetMailbox(context.Context, uint64, uint32, int64, int64) error
	GetMailbox(context.Context, uint64) (bool, uint32, bool, error)
	AdvancePublicWatermark(context.Context, int64) error
}

// Service implements MailService and admin create helpers.
type Service struct {
	mailv1.UnimplementedMailServiceServer

	store        Store
	notifier     RedDotNotifier
	orchestrator *ClaimOrchestrator
	now          func() time.Time
	logger       *slog.Logger
	quickCache   MailboxQuickCache
}

func (s *Service) SetMailboxQuickCache(cache MailboxQuickCache) { s.quickCache = cache }

func NewService(store Store, notifier RedDotNotifier, now func() time.Time, logger *slog.Logger) (*Service, error) {
	return NewServiceWithOrchestrator(store, notifier, nil, now, logger)
}

func NewServiceWithOrchestrator(
	store Store,
	notifier RedDotNotifier,
	orchestrator *ClaimOrchestrator,
	now func() time.Time,
	logger *slog.Logger,
) (*Service, error) {
	if store == nil {
		return nil, errors.New("mail store is required")
	}
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	if orchestrator == nil {
		var err error
		orchestrator, err = NewClaimOrchestrator(store, nil, now)
		if err != nil {
			return nil, err
		}
	}
	return &Service{
		store: store, notifier: notifier, orchestrator: orchestrator, now: now, logger: logger,
	}, nil
}

func (s *Service) ClaimMail(
	ctx context.Context, request *mailv1.ClaimMailRequest,
) (*mailv1.ClaimMailResponse, error) {
	if s.orchestrator == nil {
		return &mailv1.ClaimMailResponse{
			Error: &wsv1.Error{Code: wsv1.ErrorCode_SERVICE_UNAVAILABLE, Retryable: true},
		}, nil
	}
	response, err := s.orchestrator.ClaimMailDirect(ctx, request)
	if err == nil && response.GetError() == nil {
		playerID := request.GetPlayerId()
		mailID := request.GetMailId()
		registeredAt := request.GetRegisteredAtMs()
		go s.finishDirectClaim(playerID, mailID, registeredAt)
	}
	return response, err
}

func (s *Service) finishDirectClaim(playerID uint64, mailID string, registeredAt int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.orchestrator.markMailClaimed(ctx, playerID, mailID, s.now().UnixMilli()); err != nil {
		s.logger.Error("async direct mail claim state write failed",
			"player_id", playerID, "mail_id", mailID, "error", err)
		return
	}
	s.refreshUnreadQuickCache(ctx, playerID, registeredAt, "direct claim")
}

func (s *Service) refreshUnreadQuickCache(
	ctx context.Context, playerID uint64, registeredAtHint int64, reason string,
) {
	if s.quickCache == nil || playerID == 0 {
		return
	}
	registeredAt, err := s.resolveRegisteredAt(ctx, playerID, registeredAtHint)
	if err != nil {
		s.logger.Warn("resolve registration for mailbox unread refresh failed",
			"player_id", playerID, "reason", reason, "error", err)
		return
	}
	count, err := s.countUnreadVisibleMails(ctx, playerID, registeredAt)
	if err != nil {
		s.logger.Warn("count mailbox unread refresh failed",
			"player_id", playerID, "reason", reason, "error", err)
		return
	}
	calculatedAt := s.now().UTC().UnixMilli()
	if err := s.quickCache.SetMailbox(ctx, playerID, count, calculatedAt, calculatedAt); err != nil {
		s.logger.Warn("set mailbox unread refresh failed",
			"player_id", playerID, "reason", reason, "error", err)
	}
}

func (s *Service) OpenMailbox(
	ctx context.Context, request *mailv1.OpenMailboxRequest,
) (*mailv1.OpenMailboxResponse, error) {
	playerID := request.GetPlayerId()
	if playerID == 0 {
		return &mailv1.OpenMailboxResponse{Error: invalidArg()}, nil
	}
	registeredAt, err := s.resolveRegisteredAt(ctx, playerID, request.GetRegisteredAtMs())
	if err != nil {
		return nil, err
	}
	pageSize := int(request.GetPageSize())
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}
	tokenCreated, tokenMailID, ok := decodePageToken(request.GetPageToken())
	if !ok {
		return &mailv1.OpenMailboxResponse{Error: invalidArg()}, nil
	}

	views, err := s.listVisibleMails(ctx, playerID, registeredAt)
	if err != nil {
		return nil, err
	}
	publicUnreadMailIDs := s.collectUnreadPublicMailIDs(views)
	markPublicViewsRead(views)
	page := make([]*mailv1.MailView, 0, pageSize)
	var nextToken string
	for _, view := range views {
		if !afterPageToken(view.CreatedAtMs, view.MailId, tokenCreated, tokenMailID) {
			continue
		}
		if len(page) == pageSize {
			last := page[len(page)-1]
			nextToken = encodePageToken(last.CreatedAtMs, last.MailId)
			break
		}
		page = append(page, view)
	}

	openedAt := s.now().UnixMilli()
	if s.quickCache != nil {
		_ = s.quickCache.SetMailbox(ctx, playerID, countUnreadMailViews(views), openedAt, openedAt)
	}
	if len(publicUnreadMailIDs) > 0 {
		s.schedulePublicMailReadBackfill(playerID, publicUnreadMailIDs)
	}
	return &mailv1.OpenMailboxResponse{
		Mails:                 page,
		NextPageToken:         nextToken,
		LastMailboxOpenedAtMs: openedAt,
	}, nil
}

func (s *Service) MarkMailRead(
	ctx context.Context, request *mailv1.MarkMailReadRequest,
) (*mailv1.MarkMailReadResponse, error) {
	playerID := request.GetPlayerId()
	mailID := strings.TrimSpace(request.GetMailId())
	if playerID == 0 || mailID == "" {
		return &mailv1.MarkMailReadResponse{Error: invalidArg()}, nil
	}
	registeredAt, err := s.resolveRegisteredAt(ctx, playerID, request.GetRegisteredAtMs())
	if err != nil {
		return nil, err
	}
	visible, err := s.mailVisibleToPlayer(ctx, playerID, registeredAt, mailID)
	if err != nil {
		return nil, err
	}
	if !visible {
		return &mailv1.MarkMailReadResponse{Error: invalidArg()}, nil
	}
	if err := s.markRead(ctx, playerID, mailID); err != nil {
		return nil, err
	}
	// Reading one mail changes the absolute unread projection. Recalculate from
	// the authoritative read states so duplicate MARK_MAIL_READ requests cannot
	// decrement the cache twice.
	if s.quickCache != nil {
		views, listErr := s.listVisibleMails(ctx, playerID, registeredAt)
		if listErr != nil {
			return nil, listErr
		}
		calculatedAt := s.now().UTC().UnixMilli()
		if cacheErr := s.quickCache.SetMailbox(
			ctx, playerID, countUnreadMailViews(views), calculatedAt, calculatedAt,
		); cacheErr != nil {
			s.logger.Warn("refresh mailbox unread count after mark read failed",
				"player_id", playerID, "mail_id", mailID, "error", cacheErr)
		}
	}
	return &mailv1.MarkMailReadResponse{}, nil
}

func (s *Service) CheckMailboxIndicator(
	ctx context.Context, request *mailv1.CheckMailboxIndicatorRequest,
) (*mailv1.CheckMailboxIndicatorResponse, error) {
	playerID := request.GetPlayerId()
	if playerID == 0 {
		return &mailv1.CheckMailboxIndicatorResponse{Error: invalidArg()}, nil
	}
	// Login is the repair boundary. InfoSvr is an acceleration projection and
	// may still contain a legacy "since mailbox open" zero, so never let that
	// cache suppress authoritative unread PlayerMailState records here.
	registeredAt, err := s.resolveRegisteredAt(ctx, playerID, request.GetRegisteredAtMs())
	if err != nil {
		return nil, err
	}
	count, err := s.countUnreadVisibleMails(ctx, playerID, registeredAt)
	if err != nil {
		s.logger.Error("mailbox indicator authoritative count failed",
			"player_id", playerID, "error", err)
		return nil, err
	}
	calculatedAt := s.now().UTC().UnixMilli()
	if s.quickCache != nil {
		if cacheErr := s.quickCache.SetMailbox(ctx, playerID, count, calculatedAt, calculatedAt); cacheErr != nil {
			s.logger.Warn("mailbox indicator quick cache repair failed",
				"player_id", playerID, "unread_count", count, "error", cacheErr)
		}
	}
	s.logger.Info("mailbox indicator refreshed",
		"player_id", playerID, "unread_count", count)
	return &mailv1.CheckMailboxIndicatorResponse{HasNewMail: count > 0, NewMailCount: count}, nil
}

func (s *Service) countUnreadVisibleMails(ctx context.Context, playerID uint64, registeredAt int64) (uint32, error) {
	views, err := s.listVisibleMails(ctx, playerID, registeredAt)
	if err != nil {
		return 0, err
	}
	return countUnreadMailViews(views), nil
}

func (s *Service) collectUnreadPublicMailIDs(views []*mailv1.MailView) []string {
	ids := make([]string, 0)
	for _, view := range views {
		if view == nil || view.GetKind() != mailv1.MailKind_MAIL_KIND_PUBLIC || view.GetRead() {
			continue
		}
		ids = append(ids, view.GetMailId())
	}
	return ids
}

func markPublicViewsRead(views []*mailv1.MailView) {
	for _, view := range views {
		if view == nil || view.GetKind() != mailv1.MailKind_MAIL_KIND_PUBLIC || view.GetRead() {
			continue
		}
		view.Read = true
	}
}

func (s *Service) schedulePublicMailReadBackfill(playerID uint64, mailIDs []string) {
	ids := append([]string(nil), mailIDs...)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		for _, mailID := range ids {
			if err := s.markRead(ctx, playerID, mailID); err != nil {
				s.logger.Warn("backfill public mail read failed", "player_id", playerID, "mail_id", mailID, "error", err)
				return
			}
		}
	}()
}

func countUnreadMailViews(views []*mailv1.MailView) uint32 {
	var count uint32
	for _, view := range views {
		if view != nil && !view.GetRead() {
			count++
		}
	}
	return count
}

func (s *Service) CreateGiftMail(
	ctx context.Context, request *mailv1.CreateGiftMailRequest,
) (*mailv1.CreateGiftMailResponse, error) {
	sourceRaw := request.GetSourceEventId()
	if len(sourceRaw) != 16 || request.GetSenderPlayerId() == 0 ||
		request.GetRecipientPlayerId() == 0 ||
		request.GetSenderPlayerId() == request.GetRecipientPlayerId() ||
		request.GetCropItemId() == 0 ||
		request.GetQuantity() < 1 || request.GetQuantity() > 10 {
		return &mailv1.CreateGiftMailResponse{Error: invalidArg()}, nil
	}
	sourceID := encodeSourceEventID(sourceRaw)
	if existing, err := s.store.GetSourceDedup(ctx, sourceID); err == nil {
		return &mailv1.CreateGiftMailResponse{
			MailId: existing.GetMailId(), AlreadyApplied: true,
		}, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	displayName := strings.TrimSpace(request.GetSenderDisplayName())
	if displayName == "" {
		displayName = "好友"
	}
	createdAt := request.GetCreatedAtMs()
	if createdAt <= 0 {
		createdAt = s.now().UnixMilli()
	}
	mailID, err := newMailID(s.now())
	if err != nil {
		return nil, err
	}
	if err := s.store.TryInsertSourceDedup(ctx, &tcaplusv1.MailSourceDedup{
		SourceEventId: sourceID, MailId: mailID, CreatedAtMs: createdAt,
	}); err != nil {
		if errors.Is(err, ErrAlreadyExists) {
			existing, getErr := s.store.GetSourceDedup(ctx, sourceID)
			if getErr != nil {
				return nil, getErr
			}
			return &mailv1.CreateGiftMailResponse{
				MailId: existing.GetMailId(), AlreadyApplied: true,
			}, nil
		}
		return nil, err
	}
	title := "好友赠礼"
	content := displayName + " 送给你一批作物，记得查收。"
	record := &tcaplusv1.PrivateMail{
		RecipientPlayerId: request.GetRecipientPlayerId(),
		MailId:            mailID,
		MailType:          tcaplusv1.MailType_MAIL_TYPE_GIFT,
		CreatedAtMs:       createdAt,
		PublishedAtMs:     createdAt,
		SenderType:        tcaplusv1.MailSenderType_MAIL_SENDER_TYPE_PLAYER,
		SenderPlayerId:    request.GetSenderPlayerId(),
		SenderDisplayName: displayName,
		Title:             title,
		Content:           content,
		Attachments: []*tcaplusv1.MailAttachment{{
			ItemId: request.GetCropItemId(), Quantity: request.GetQuantity(),
		}},
		SourceEventId: sourceID,
	}
	if err := s.store.InsertPrivateMail(ctx, record); err != nil {
		if errors.Is(err, ErrAlreadyExists) {
			return &mailv1.CreateGiftMailResponse{MailId: mailID, AlreadyApplied: true}, nil
		}
		return nil, err
	}
	count := s.mailCountAfterPrivateInsert(ctx, request.GetRecipientPlayerId(), mailID, record.GetCreatedAtMs())
	s.notifyMailRedDot(ctx, request.GetRecipientPlayerId(), mailID, count)
	return &mailv1.CreateGiftMailResponse{MailId: mailID}, nil
}

// CreatePublicMailInput is the admin payload for a single public mail copy.
type CreatePublicMailInput struct {
	Title             string
	Content           string
	SenderDisplayName string
	PublishedAtMS     int64
	SourceEventID     string
	Attachments       []*tcaplusv1.MailAttachment
	CoinAmount        int64
}

// CreatePrivateMailInput is the admin payload for one recipient private mail.
type CreatePrivateMailInput struct {
	RecipientPlayerID uint64
	Title             string
	Content           string
	SenderDisplayName string
	SourceEventID     string
	Attachments       []*tcaplusv1.MailAttachment
	CoinAmount        int64
}

func (s *Service) CreateSystemRewardMail(
	ctx context.Context, request *mailv1.CreateSystemRewardMailRequest,
) (*mailv1.CreateSystemRewardMailResponse, error) {
	sourceID := strings.TrimSpace(request.GetSourceEventId())
	recipient := request.GetRecipientPlayerId()
	title := strings.TrimSpace(request.GetTitle())
	content := strings.TrimSpace(request.GetContent())
	if sourceID == "" || recipient == 0 || title == "" || content == "" {
		return &mailv1.CreateSystemRewardMailResponse{Error: invalidArg()}, nil
	}
	attachments := make([]*tcaplusv1.MailAttachment, 0, len(request.GetAttachments()))
	for _, attachment := range request.GetAttachments() {
		if attachment == nil {
			continue
		}
		attachments = append(attachments, &tcaplusv1.MailAttachment{
			ItemId: attachment.GetItemId(), Quantity: attachment.GetQuantity(),
		})
	}
	coinAmount := request.GetCoinAmount()
	if len(attachments) == 0 && coinAmount <= 0 {
		return &mailv1.CreateSystemRewardMailResponse{Error: invalidArg()}, nil
	}
	mailID, err := s.CreatePrivateMail(ctx, CreatePrivateMailInput{
		RecipientPlayerID: recipient,
		Title:             title,
		Content:           content,
		SenderDisplayName: request.GetSenderDisplayName(),
		SourceEventID:     sourceID,
		Attachments:       attachments,
		CoinAmount:        coinAmount,
	})
	if err != nil {
		if errors.Is(err, ErrAlreadyExists) {
			existing, getErr := s.store.GetSourceDedup(ctx, sourceID)
			if getErr != nil {
				return nil, getErr
			}
			return &mailv1.CreateSystemRewardMailResponse{
				MailId: existing.GetMailId(), AlreadyApplied: true,
			}, nil
		}
		if errors.Is(err, ErrNotFound) {
			return &mailv1.CreateSystemRewardMailResponse{Error: invalidArg()}, nil
		}
		return nil, err
	}
	return &mailv1.CreateSystemRewardMailResponse{MailId: mailID}, nil
}

func (s *Service) CreatePublicMail(ctx context.Context, in CreatePublicMailInput) (string, error) {
	if err := validateMailBody(in.Title, in.Content, in.Attachments, in.CoinAmount); err != nil {
		return "", err
	}
	now := s.now()
	nowMS := now.UnixMilli()
	published := in.PublishedAtMS
	if published <= 0 {
		published = nowMS
	}
	mailID, err := newMailID(now)
	if err != nil {
		return "", err
	}
	if source := strings.TrimSpace(in.SourceEventID); source != "" {
		if err := s.store.TryInsertSourceDedup(ctx, &tcaplusv1.MailSourceDedup{
			SourceEventId: source, MailId: mailID, CreatedAtMs: nowMS,
		}); err != nil {
			if errors.Is(err, ErrAlreadyExists) {
				return "", ErrAlreadyExists
			}
			return "", err
		}
	}
	record := &tcaplusv1.PublicMail{
		MailId:            mailID,
		MailType:          tcaplusv1.MailType_MAIL_TYPE_PUBLIC,
		CreatedAtMs:       nowMS,
		PublishedAtMs:     published,
		SenderType:        tcaplusv1.MailSenderType_MAIL_SENDER_TYPE_SYSTEM,
		SenderDisplayName: strings.TrimSpace(in.SenderDisplayName),
		Title:             strings.TrimSpace(in.Title),
		Content:           strings.TrimSpace(in.Content),
		Attachments:       cloneAttachments(in.Attachments),
		SourceEventId:     strings.TrimSpace(in.SourceEventID),
		CoinAmount:        in.CoinAmount,
	}
	if record.SenderDisplayName == "" {
		record.SenderDisplayName = "系统"
	}
	if err := s.store.InsertPublicMail(ctx, record); err != nil {
		return "", err
	}
	if s.quickCache != nil {
		_ = s.quickCache.AdvancePublicWatermark(ctx, published)
	}
	return mailID, nil
}

func (s *Service) CreatePrivateMail(ctx context.Context, in CreatePrivateMailInput) (string, error) {
	if in.RecipientPlayerID == 0 {
		return "", fmt.Errorf("%w: recipient required", ErrNotFound)
	}
	if err := validateMailBody(in.Title, in.Content, in.Attachments, in.CoinAmount); err != nil {
		return "", err
	}
	now := s.now()
	nowMS := now.UnixMilli()
	mailID, err := newMailID(now)
	if err != nil {
		return "", err
	}
	if source := strings.TrimSpace(in.SourceEventID); source != "" {
		if err := s.store.TryInsertSourceDedup(ctx, &tcaplusv1.MailSourceDedup{
			SourceEventId: source, MailId: mailID, CreatedAtMs: nowMS,
		}); err != nil {
			if errors.Is(err, ErrAlreadyExists) {
				return "", ErrAlreadyExists
			}
			return "", err
		}
	}
	record := &tcaplusv1.PrivateMail{
		RecipientPlayerId: in.RecipientPlayerID,
		MailId:            mailID,
		MailType:          tcaplusv1.MailType_MAIL_TYPE_PRIVATE,
		CreatedAtMs:       nowMS,
		PublishedAtMs:     nowMS,
		SenderType:        tcaplusv1.MailSenderType_MAIL_SENDER_TYPE_SYSTEM,
		SenderDisplayName: strings.TrimSpace(in.SenderDisplayName),
		Title:             strings.TrimSpace(in.Title),
		Content:           strings.TrimSpace(in.Content),
		Attachments:       cloneAttachments(in.Attachments),
		SourceEventId:     strings.TrimSpace(in.SourceEventID),
		CoinAmount:        in.CoinAmount,
	}
	if record.SenderDisplayName == "" {
		record.SenderDisplayName = "系统"
	}
	if err := s.store.InsertPrivateMail(ctx, record); err != nil {
		return "", err
	}
	count := s.mailCountAfterPrivateInsert(ctx, in.RecipientPlayerID, mailID, nowMS)
	s.notifyMailRedDot(ctx, in.RecipientPlayerID, mailID, count)
	return mailID, nil
}

func (s *Service) mailCountAfterPrivateInsert(ctx context.Context, playerID uint64, mailID string, createdAtMS int64) uint32 {
	if s.quickCache != nil {
		known, cachedCount, err := s.quickCache.ApplyMailEvent(ctx, playerID, mailID, createdAtMS)
		if err != nil {
			s.logger.Warn("apply mailbox quick event failed", "player_id", playerID, "mail_id", mailID, "error", err)
		}
		if known {
			return cachedCount
		}
	}

	// InfoSvr is an acceleration cache, never the authority. A cold or failed
	// cache lookup must still produce an absolute count for the push; sending
	// zero here makes the H5 interpret a newly-created mail as a clear event.
	registeredAt, err := s.resolveRegisteredAt(ctx, playerID, 0)
	if err != nil {
		s.logger.Warn("resolve mailbox registration time failed", "player_id", playerID, "mail_id", mailID, "error", err)
		return 0
	}
	count, err := s.countUnreadVisibleMails(ctx, playerID, registeredAt)
	if err != nil {
		s.logger.Warn("count new mailbox items failed", "player_id", playerID, "mail_id", mailID, "error", err)
		return 0
	}
	if s.quickCache != nil {
		if err := s.quickCache.SetMailbox(ctx, playerID, count, createdAtMS, createdAtMS); err != nil {
			s.logger.Warn("repair mailbox quick count failed", "player_id", playerID, "mail_id", mailID, "error", err)
		}
	}
	return count
}

func (s *Service) notifyMailRedDot(ctx context.Context, playerID uint64, mailID string, count uint32) {
	if s.notifier == nil {
		return
	}
	if err := s.notifier.SetMailRedDot(ctx, playerID, mailID, count); err != nil {
		s.logger.Warn("mail red-dot notify failed",
			"player_id", playerID,
			"mail_id", mailID,
			"error", err,
		)
	}
}

func (s *Service) resolveRegisteredAt(ctx context.Context, playerID uint64, requested int64) (int64, error) {
	if requested > 0 {
		return requested, nil
	}
	at, found, err := s.store.RegisteredAtMS(ctx, playerID)
	if err != nil {
		return 0, err
	}
	if !found || at <= 0 {
		return 0, fmt.Errorf("registered_at_ms missing for player %d", playerID)
	}
	return at, nil
}

func (s *Service) listVisibleMails(
	ctx context.Context, playerID uint64, registeredAtMS int64,
) ([]*mailv1.MailView, error) {
	publicMails, err := s.store.ListPublicMails(ctx)
	if err != nil {
		return nil, err
	}
	privateMails, err := s.store.ListPrivateMails(ctx, playerID)
	if err != nil {
		return nil, err
	}
	states, err := s.store.ListMailStates(ctx, playerID)
	if err != nil {
		return nil, err
	}
	stateByMailID := make(map[string]*tcaplusv1.PlayerMailState, len(states))
	for _, state := range states {
		if state != nil {
			stateByMailID[state.GetMailId()] = state
		}
	}
	views := make([]*mailv1.MailView, 0, len(publicMails)+len(privateMails))
	for _, record := range publicMails {
		if record.GetPublishedAtMs() <= registeredAtMS {
			continue
		}
		views = append(views, toPublicView(record, stateByMailID[record.GetMailId()]))
	}
	for _, record := range privateMails {
		views = append(views, toPrivateView(record, stateByMailID[record.GetMailId()]))
	}
	sortMailViews(views)
	return views, nil
}

func (s *Service) mailVisibleToPlayer(
	ctx context.Context, playerID uint64, registeredAtMS int64, mailID string,
) (bool, error) {
	if public, err := s.store.GetPublicMail(ctx, mailID); err == nil {
		return public.GetPublishedAtMs() > registeredAtMS, nil
	} else if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	if _, err := s.store.GetPrivateMail(ctx, playerID, mailID); err == nil {
		return true, nil
	} else if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	return false, nil
}

func toPublicView(record *tcaplusv1.PublicMail, state *tcaplusv1.PlayerMailState) *mailv1.MailView {
	return &mailv1.MailView{
		MailId:            record.GetMailId(),
		Kind:              mailv1.MailKind_MAIL_KIND_PUBLIC,
		CreatedAtMs:       record.GetCreatedAtMs(),
		PublishedAtMs:     record.GetPublishedAtMs(),
		SenderDisplayName: record.GetSenderDisplayName(),
		SenderPlayerId:    record.GetSenderPlayerId(),
		Title:             record.GetTitle(),
		Content:           record.GetContent(),
		Attachments:       toAttachmentViews(record.GetAttachments()),
		Read:              state.GetRead(),
		Claimed:           state.GetClaimed(),
		CoinAmount:        record.GetCoinAmount(),
	}
}

func toPrivateView(record *tcaplusv1.PrivateMail, state *tcaplusv1.PlayerMailState) *mailv1.MailView {
	kind := mailv1.MailKind_MAIL_KIND_PRIVATE
	if record.GetMailType() == tcaplusv1.MailType_MAIL_TYPE_GIFT {
		kind = mailv1.MailKind_MAIL_KIND_GIFT
	}
	return &mailv1.MailView{
		MailId:            record.GetMailId(),
		Kind:              kind,
		CreatedAtMs:       record.GetCreatedAtMs(),
		PublishedAtMs:     record.GetPublishedAtMs(),
		SenderDisplayName: record.GetSenderDisplayName(),
		SenderPlayerId:    record.GetSenderPlayerId(),
		RecipientPlayerId: record.GetRecipientPlayerId(),
		Title:             record.GetTitle(),
		Content:           record.GetContent(),
		Attachments:       toAttachmentViews(record.GetAttachments()),
		Read:              state.GetRead(),
		Claimed:           state.GetClaimed(),
		CoinAmount:        record.GetCoinAmount(),
	}
}

func (s *Service) markRead(ctx context.Context, playerID uint64, mailID string) error {
	nowMS := s.now().UnixMilli()
	for attempt := 0; attempt < 8; attempt++ {
		state, version, err := s.store.GetMailState(ctx, playerID, mailID)
		if errors.Is(err, ErrNotFound) {
			_, err = s.store.InsertMailState(ctx, &tcaplusv1.PlayerMailState{
				PlayerId: playerID, MailId: mailID, Read: true, UpdatedAtMs: nowMS,
			})
			if errors.Is(err, ErrAlreadyExists) {
				continue
			}
			return err
		}
		if err != nil {
			return err
		}
		if state.GetRead() {
			return nil
		}
		state.Read = true
		state.UpdatedAtMs = nowMS
		_, err = s.store.UpdateMailState(ctx, state, version)
		if errors.Is(err, ErrConflict) {
			continue
		}
		return err
	}
	return ErrConflict
}

func validateMailBody(
	title, content string, attachments []*tcaplusv1.MailAttachment, coinAmount int64,
) error {
	title = strings.TrimSpace(title)
	content = strings.TrimSpace(content)
	if title == "" || utf8.RuneCountInString(title) > MaxTitleRunes {
		return fmt.Errorf("%w: title", ErrNotFound)
	}
	if content == "" || utf8.RuneCountInString(content) > MaxContentRunes {
		return fmt.Errorf("%w: content", ErrNotFound)
	}
	if coinAmount < 0 {
		return fmt.Errorf("%w: coin_amount", ErrNotFound)
	}
	if len(attachments) > MaxAttachments {
		return fmt.Errorf("%w: too many attachments", ErrNotFound)
	}
	for _, attachment := range attachments {
		if attachment.GetItemId() == 0 || attachment.GetQuantity() == 0 ||
			attachment.GetQuantity() > MaxAttachmentQty {
			return fmt.Errorf("%w: attachment", ErrNotFound)
		}
	}
	return nil
}

func cloneAttachments(in []*tcaplusv1.MailAttachment) []*tcaplusv1.MailAttachment {
	if len(in) == 0 {
		return nil
	}
	out := make([]*tcaplusv1.MailAttachment, 0, len(in))
	for _, attachment := range in {
		out = append(out, &tcaplusv1.MailAttachment{
			ItemId: attachment.GetItemId(), Quantity: attachment.GetQuantity(),
		})
	}
	return out
}

func toAttachmentViews(in []*tcaplusv1.MailAttachment) []*mailv1.MailAttachmentView {
	if len(in) == 0 {
		return nil
	}
	out := make([]*mailv1.MailAttachmentView, 0, len(in))
	for _, attachment := range in {
		out = append(out, &mailv1.MailAttachmentView{
			ItemId: attachment.GetItemId(), Quantity: attachment.GetQuantity(),
		})
	}
	return out
}

func sortMailViews(views []*mailv1.MailView) {
	sort.Slice(views, func(i, j int) bool {
		return mailLessDesc(
			views[i].CreatedAtMs, views[i].MailId,
			views[j].CreatedAtMs, views[j].MailId,
		)
	})
}

func invalidArg() *wsv1.Error {
	return &wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}
}

func encodeSourceEventID(eventID []byte) string {
	return hex.EncodeToString(eventID)
}
