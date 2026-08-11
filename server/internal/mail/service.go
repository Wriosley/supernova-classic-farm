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

// RedDotNotifier is InfoSvr SetMailRedDot. Failures must not roll back mail writes.
type RedDotNotifier interface {
	SetMailRedDot(ctx context.Context, playerID uint64, notificationID string) error
}

// Service implements MailService and admin create helpers.
type Service struct {
	mailv1.UnimplementedMailServiceServer

	store        Store
	notifier     RedDotNotifier
	orchestrator *ClaimOrchestrator
	now          func() time.Time
	logger       *slog.Logger
}

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
	return s.orchestrator.ClaimMail(ctx, request)
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
	if err := s.saveCursor(ctx, playerID, openedAt); err != nil {
		return nil, err
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
	return &mailv1.MarkMailReadResponse{}, nil
}

func (s *Service) CheckMailboxIndicator(
	ctx context.Context, request *mailv1.CheckMailboxIndicatorRequest,
) (*mailv1.CheckMailboxIndicatorResponse, error) {
	playerID := request.GetPlayerId()
	if playerID == 0 {
		return &mailv1.CheckMailboxIndicatorResponse{Error: invalidArg()}, nil
	}
	registeredAt, err := s.resolveRegisteredAt(ctx, playerID, request.GetRegisteredAtMs())
	if err != nil {
		return nil, err
	}
	cursorMS := int64(0)
	cursor, _, err := s.store.GetCursor(ctx, playerID)
	if err == nil {
		cursorMS = cursor.GetLastMailboxOpenedAtMs()
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	views, err := s.listVisibleMails(ctx, playerID, registeredAt)
	if err != nil {
		return nil, err
	}
	for _, view := range views {
		indicatorAt := view.CreatedAtMs
		if view.Kind == mailv1.MailKind_MAIL_KIND_PUBLIC && view.PublishedAtMs > 0 {
			indicatorAt = view.PublishedAtMs
		}
		if indicatorAt > cursorMS {
			return &mailv1.CheckMailboxIndicatorResponse{HasNewMail: true}, nil
		}
	}
	return &mailv1.CheckMailboxIndicatorResponse{HasNewMail: false}, nil
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
	s.notifyMailRedDot(ctx, request.GetRecipientPlayerId(), mailID)
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
}

// CreatePrivateMailInput is the admin payload for one recipient private mail.
type CreatePrivateMailInput struct {
	RecipientPlayerID uint64
	Title             string
	Content           string
	SenderDisplayName string
	SourceEventID     string
	Attachments       []*tcaplusv1.MailAttachment
}

func (s *Service) CreatePublicMail(ctx context.Context, in CreatePublicMailInput) (string, error) {
	if err := validateMailBody(in.Title, in.Content, in.Attachments); err != nil {
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
	}
	if record.SenderDisplayName == "" {
		record.SenderDisplayName = "系统"
	}
	if err := s.store.InsertPublicMail(ctx, record); err != nil {
		return "", err
	}
	return mailID, nil
}

func (s *Service) CreatePrivateMail(ctx context.Context, in CreatePrivateMailInput) (string, error) {
	if in.RecipientPlayerID == 0 {
		return "", fmt.Errorf("%w: recipient required", ErrNotFound)
	}
	if err := validateMailBody(in.Title, in.Content, in.Attachments); err != nil {
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
	}
	if record.SenderDisplayName == "" {
		record.SenderDisplayName = "系统"
	}
	if err := s.store.InsertPrivateMail(ctx, record); err != nil {
		return "", err
	}
	s.notifyMailRedDot(ctx, in.RecipientPlayerID, mailID)
	return mailID, nil
}

func (s *Service) notifyMailRedDot(ctx context.Context, playerID uint64, mailID string) {
	if s.notifier == nil {
		return
	}
	if err := s.notifier.SetMailRedDot(ctx, playerID, mailID); err != nil {
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
	views := make([]*mailv1.MailView, 0, len(publicMails)+len(privateMails))
	for _, record := range publicMails {
		if record.GetPublishedAtMs() <= registeredAtMS {
			continue
		}
		view, err := s.toPublicView(ctx, playerID, record)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	for _, record := range privateMails {
		view, err := s.toPrivateView(ctx, playerID, record)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
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

func (s *Service) toPublicView(
	ctx context.Context, playerID uint64, record *tcaplusv1.PublicMail,
) (*mailv1.MailView, error) {
	read, claimed, err := s.readState(ctx, playerID, record.GetMailId())
	if err != nil {
		return nil, err
	}
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
		Read:              read,
		Claimed:           claimed,
	}, nil
}

func (s *Service) toPrivateView(
	ctx context.Context, playerID uint64, record *tcaplusv1.PrivateMail,
) (*mailv1.MailView, error) {
	read, claimed, err := s.readState(ctx, playerID, record.GetMailId())
	if err != nil {
		return nil, err
	}
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
		Read:              read,
		Claimed:           claimed,
	}, nil
}

func (s *Service) readState(ctx context.Context, playerID uint64, mailID string) (bool, bool, error) {
	state, _, err := s.store.GetMailState(ctx, playerID, mailID)
	if errors.Is(err, ErrNotFound) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return state.GetRead(), state.GetClaimed(), nil
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

func (s *Service) saveCursor(ctx context.Context, playerID uint64, openedAtMS int64) error {
	for attempt := 0; attempt < 8; attempt++ {
		cursor, version, err := s.store.GetCursor(ctx, playerID)
		if errors.Is(err, ErrNotFound) {
			_, err = s.store.InsertCursor(ctx, &tcaplusv1.PlayerMailboxCursor{
				PlayerId:               playerID,
				LastMailboxOpenedAtMs:  openedAtMS,
				UpdatedAtMs:            openedAtMS,
			})
			if errors.Is(err, ErrAlreadyExists) {
				continue
			}
			return err
		}
		if err != nil {
			return err
		}
		if openedAtMS < cursor.GetLastMailboxOpenedAtMs() {
			return nil
		}
		cursor.LastMailboxOpenedAtMs = openedAtMS
		cursor.UpdatedAtMs = openedAtMS
		_, err = s.store.UpdateCursor(ctx, cursor, version)
		if errors.Is(err, ErrConflict) {
			continue
		}
		return err
	}
	return ErrConflict
}

func validateMailBody(title, content string, attachments []*tcaplusv1.MailAttachment) error {
	title = strings.TrimSpace(title)
	content = strings.TrimSpace(content)
	if title == "" || utf8.RuneCountInString(title) > MaxTitleRunes {
		return fmt.Errorf("%w: title", ErrNotFound)
	}
	if content == "" || utf8.RuneCountInString(content) > MaxContentRunes {
		return fmt.Errorf("%w: content", ErrNotFound)
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
