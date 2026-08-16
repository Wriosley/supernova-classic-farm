package mail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	mailv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/mail"
	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
)

var (
	ErrClaimInventoryFull = errors.New("mail claim inventory capacity exceeded")
	ErrClaimConflict      = errors.New("mail claim conflict")
	ErrClaimNotReady      = errors.New("mail claim not ready")
)

// ZoneRewardApplier applies attachments on the recipient Owner Zone.
type ZoneRewardApplier interface {
	ApplyMailReward(
		ctx context.Context,
		playerID uint64,
		claimID []byte,
		mailID string,
		attachments []*tcaplusv1.MailClaimAttachment,
		coinAmount int64,
	) (*rpcv1.ApplyMailRewardResponse, error)
}

// ClaimOrchestrator owns Begin/Apply/Complete/Cancel for mail claims.
type ClaimOrchestrator struct {
	store Store
	zone  ZoneRewardApplier
	now   func() time.Time
}

func NewClaimOrchestrator(store Store, zone ZoneRewardApplier, now func() time.Time) (*ClaimOrchestrator, error) {
	if store == nil {
		return nil, errors.New("mail claim store is required")
	}
	if now == nil {
		now = time.Now
	}
	return &ClaimOrchestrator{store: store, zone: zone, now: now}, nil
}

func (o *ClaimOrchestrator) ClaimMail(
	ctx context.Context, request *mailv1.ClaimMailRequest,
) (*mailv1.ClaimMailResponse, error) {
	if request.GetPlayerId() == 0 || strings.TrimSpace(request.GetMailId()) == "" ||
		len(request.GetClaimId()) != 16 {
		return &mailv1.ClaimMailResponse{Error: invalidArg()}, nil
	}
	saga, _, err := o.BeginClaim(ctx, request)
	if err != nil {
		return mapClaimError(err), nil
	}
	if saga.GetState() == tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_COMPLETED {
		items := claimAttachmentsToViews(saga.GetAttachments())
		return &mailv1.ClaimMailResponse{
			MailId: saga.GetMailId(), ItemsAdded: items, CoinsAdded: saga.GetCoinAmount(),
			Patch: &wsv1.PlayerStatePatch{InventoryUpserts: items},
		}, nil
	}
	applied, err := o.Advance(ctx, saga.GetClaimId())
	if err != nil {
		return mapClaimError(err), nil
	}
	final, _, err := o.store.GetClaimSaga(ctx, request.GetClaimId())
	if err != nil {
		return nil, err
	}
	if final.GetState() != tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_COMPLETED {
		return mapClaimError(ErrClaimNotReady), nil
	}
	items := claimAttachmentsToViews(final.GetAttachments())
	coinsAdded := final.GetCoinAmount()
	patch := &wsv1.PlayerStatePatch{InventoryUpserts: items}
	var version *wsv1.StateVersion
	if applied != nil {
		if len(applied.GetItemsAdded()) > 0 {
			items = applied.GetItemsAdded()
		}
		if applied.GetCoinsAdded() > 0 {
			coinsAdded = applied.GetCoinsAdded()
		}
		if applied.GetPatch() != nil {
			patch = applied.GetPatch()
		}
		version = appliedStateVersion(applied)
	}
	return &mailv1.ClaimMailResponse{
		MailId: final.GetMailId(), ItemsAdded: items, CoinsAdded: coinsAdded,
		Patch: patch, StateVersion: version,
	}, nil
}

// ClaimMailDirect is the low-latency online path. It validates the authoritative
// mail, checks the persisted claimed bit, and asks the Owner Zone to apply the
// reward in Actor memory. It deliberately creates or advances no MailClaimSaga;
// legacy Saga records and the reconciler remain only for work created by older
// binaries.
func (o *ClaimOrchestrator) ClaimMailDirect(
	ctx context.Context, request *mailv1.ClaimMailRequest,
) (*mailv1.ClaimMailResponse, error) {
	if request.GetPlayerId() == 0 || strings.TrimSpace(request.GetMailId()) == "" ||
		len(request.GetClaimId()) != 16 {
		return &mailv1.ClaimMailResponse{Error: invalidArg()}, nil
	}
	if o.zone == nil {
		return nil, errors.New("zone mail reward client is required")
	}
	playerID := request.GetPlayerId()
	mailID := strings.TrimSpace(request.GetMailId())
	attachments, coinAmount, err := o.loadDirectClaimableAttachments(
		ctx, playerID, request.GetRegisteredAtMs(), mailID,
	)
	if err != nil {
		return mapClaimError(err), nil
	}
	state, _, err := o.store.GetMailState(ctx, playerID, mailID)
	if err == nil && state.GetClaimed() {
		return mapClaimError(ErrClaimConflict), nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	applied, err := o.zone.ApplyMailReward(
		ctx, playerID, request.GetClaimId(), mailID, attachments, coinAmount,
	)
	if err != nil {
		return nil, err
	}
	if applied.GetError() != nil {
		code := applied.GetError().GetCode()
		if code == wsv1.ErrorCode_INVENTORY_CAPACITY_EXCEEDED ||
			code == wsv1.ErrorCode_INVENTORY_TYPE_LIMIT ||
			code == wsv1.ErrorCode_INVENTORY_STACK_LIMIT {
			return mapClaimError(ErrClaimInventoryFull), nil
		}
		return &mailv1.ClaimMailResponse{Error: applied.GetError()}, nil
	}
	items := applied.GetItemsAdded()
	if len(items) == 0 {
		items = claimAttachmentsToViews(attachments)
	}
	patch := applied.GetPatch()
	if patch == nil {
		patch = &wsv1.PlayerStatePatch{InventoryUpserts: items}
	}
	return &mailv1.ClaimMailResponse{
		MailId: mailID, ItemsAdded: items, CoinsAdded: applied.GetCoinsAdded(),
		Patch: patch, StateVersion: appliedStateVersion(applied),
	}, nil
}

func (o *ClaimOrchestrator) loadDirectClaimableAttachments(
	ctx context.Context, playerID uint64, registeredAtHint int64, mailID string,
) ([]*tcaplusv1.MailClaimAttachment, int64, error) {
	// Private/gift mail is the common claim path and its composite key already
	// proves recipient visibility, so resolve it with one point read. Only a miss
	// falls back to public mail and registration-time filtering.
	if private, err := o.store.GetPrivateMail(ctx, playerID, mailID); err == nil {
		attachments := toClaimAttachments(private.GetAttachments())
		coins := private.GetCoinAmount()
		if len(attachments) == 0 && coins <= 0 {
			return nil, 0, fmt.Errorf("%w: no claimable reward", ErrNotFound)
		}
		return attachments, coins, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, 0, err
	}
	public, err := o.store.GetPublicMail(ctx, mailID)
	if err != nil {
		return nil, 0, err
	}
	registeredAt, err := o.resolveRegistered(ctx, playerID, registeredAtHint)
	if err != nil {
		return nil, 0, err
	}
	if public.GetPublishedAtMs() <= registeredAt {
		return nil, 0, fmt.Errorf("%w: mail not visible", ErrNotFound)
	}
	attachments := toClaimAttachments(public.GetAttachments())
	coins := public.GetCoinAmount()
	if len(attachments) == 0 && coins <= 0 {
		return nil, 0, fmt.Errorf("%w: no claimable reward", ErrNotFound)
	}
	return attachments, coins, nil
}

func (o *ClaimOrchestrator) BeginClaim(
	ctx context.Context, request *mailv1.ClaimMailRequest,
) (*tcaplusv1.MailClaimSaga, int32, error) {
	playerID := request.GetPlayerId()
	mailID := strings.TrimSpace(request.GetMailId())
	claimID := request.GetClaimId()
	registeredAt, err := o.resolveRegistered(ctx, playerID, request.GetRegisteredAtMs())
	if err != nil {
		return nil, 0, err
	}
	attachments, coinAmount, err := o.loadClaimableAttachments(ctx, playerID, registeredAt, mailID)
	if err != nil {
		return nil, 0, err
	}
	digest := attachmentsDigest(attachments)

	if existing, version, err := o.store.GetClaimSaga(ctx, claimID); err == nil {
		if existing.GetPlayerId() != playerID || existing.GetMailId() != mailID {
			return nil, 0, ErrClaimConflict
		}
		if !bytes.Equal(existing.GetAttachmentsDigestSha256(), digest) ||
			existing.GetCoinAmount() != coinAmount {
			return nil, 0, ErrClaimConflict
		}
		return existing, version, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, 0, err
	}

	if err := o.ensureNoActiveClaim(ctx, playerID, mailID, claimID); err != nil {
		return nil, 0, err
	}
	state, _, err := o.store.GetMailState(ctx, playerID, mailID)
	if err == nil && state.GetClaimed() {
		return nil, 0, ErrClaimConflict
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, 0, err
	}

	nowMS := o.now().UnixMilli()
	record := &tcaplusv1.MailClaimSaga{
		ClaimId: append([]byte(nil), claimID...),
		MailId:  mailID, PlayerId: playerID,
		Attachments:             attachments,
		CoinAmount:              coinAmount,
		State:                   tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_CLAIMING,
		RetryAtMs:               nowMS,
		CreatedAtMs:             nowMS,
		UpdatedAtMs:             nowMS,
		AttachmentsDigestSha256: digest,
	}
	version, err := o.store.InsertClaimSaga(ctx, record)
	if errors.Is(err, ErrAlreadyExists) {
		return o.store.GetClaimSaga(ctx, claimID)
	}
	if err != nil {
		return nil, 0, err
	}
	return record, version, nil
}

func (o *ClaimOrchestrator) Advance(ctx context.Context, claimID []byte) (*rpcv1.ApplyMailRewardResponse, error) {
	var lastApplied *rpcv1.ApplyMailRewardResponse
	for attempt := 0; attempt < 8; attempt++ {
		saga, version, err := o.store.GetClaimSaga(ctx, claimID)
		if err != nil {
			return nil, err
		}
		switch saga.GetState() {
		case tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_COMPLETED,
			tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_AVAILABLE:
			return lastApplied, nil
		case tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_CLAIMING,
			tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_INIT:
			if o.zone == nil {
				return nil, errors.New("zone mail reward client is required")
			}
			response, applyErr := o.zone.ApplyMailReward(
				ctx, saga.GetPlayerId(), saga.GetClaimId(), saga.GetMailId(),
				saga.GetAttachments(), saga.GetCoinAmount(),
			)
			if applyErr != nil {
				return nil, applyErr
			}
			if response.GetError() != nil {
				code := response.GetError().GetCode()
				if code == wsv1.ErrorCode_INVENTORY_CAPACITY_EXCEEDED ||
					code == wsv1.ErrorCode_INVENTORY_TYPE_LIMIT ||
					code == wsv1.ErrorCode_INVENTORY_STACK_LIMIT {
					return nil, o.cancelClaim(ctx, saga, version, ErrClaimInventoryFull)
				}
				return nil, fmt.Errorf("apply mail reward: %s", code.String())
			}
			lastApplied = response
			saga.State = tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_PLAYER_APPLIED
			saga.UpdatedAtMs = o.now().UnixMilli()
			saga.LastError = ""
			if _, err := o.store.UpdateClaimSaga(ctx, saga, version); errors.Is(err, ErrConflict) {
				continue
			} else if err != nil {
				return nil, err
			}
			continue
		case tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_PLAYER_APPLIED:
			if err := o.completeClaim(ctx, saga, version); errors.Is(err, ErrConflict) {
				continue
			} else if err != nil {
				return nil, err
			}
			return lastApplied, nil
		case tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_CANCELLING:
			if err := o.finishCancel(ctx, saga, version); errors.Is(err, ErrConflict) {
				continue
			} else {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported claim saga state %v", saga.GetState())
		}
	}
	return nil, ErrConflict
}

func (o *ClaimOrchestrator) completeClaim(
	ctx context.Context, saga *tcaplusv1.MailClaimSaga, version int32,
) error {
	nowMS := o.now().UnixMilli()
	if err := o.markMailClaimed(ctx, saga.GetPlayerId(), saga.GetMailId(), nowMS); err != nil {
		return err
	}
	saga.State = tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_COMPLETED
	saga.UpdatedAtMs = nowMS
	saga.LastError = ""
	_, err := o.store.UpdateClaimSaga(ctx, saga, version)
	return err
}

func (o *ClaimOrchestrator) cancelClaim(
	ctx context.Context, saga *tcaplusv1.MailClaimSaga, version int32, cause error,
) error {
	saga.State = tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_CANCELLING
	saga.UpdatedAtMs = o.now().UnixMilli()
	saga.LastError = cause.Error()
	if _, err := o.store.UpdateClaimSaga(ctx, saga, version); err != nil {
		return err
	}
	_, err := o.Advance(ctx, saga.GetClaimId())
	if err == nil {
		return ErrClaimInventoryFull
	}
	return err
}

func (o *ClaimOrchestrator) finishCancel(
	ctx context.Context, saga *tcaplusv1.MailClaimSaga, version int32,
) error {
	saga.State = tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_AVAILABLE
	saga.UpdatedAtMs = o.now().UnixMilli()
	if _, err := o.store.UpdateClaimSaga(ctx, saga, version); err != nil {
		return err
	}
	return ErrClaimInventoryFull
}

func (o *ClaimOrchestrator) markMailClaimed(
	ctx context.Context, playerID uint64, mailID string, nowMS int64,
) error {
	for attempt := 0; attempt < 8; attempt++ {
		state, version, err := o.store.GetMailState(ctx, playerID, mailID)
		if errors.Is(err, ErrNotFound) {
			_, err = o.store.InsertMailState(ctx, &tcaplusv1.PlayerMailState{
				PlayerId: playerID, MailId: mailID, Read: true, Claimed: true, UpdatedAtMs: nowMS,
			})
			if errors.Is(err, ErrAlreadyExists) {
				continue
			}
			return err
		}
		if err != nil {
			return err
		}
		if state.GetClaimed() {
			return nil
		}
		state.Claimed = true
		state.Read = true
		state.UpdatedAtMs = nowMS
		_, err = o.store.UpdateMailState(ctx, state, version)
		if errors.Is(err, ErrConflict) {
			continue
		}
		return err
	}
	return ErrConflict
}

func (o *ClaimOrchestrator) ensureNoActiveClaim(
	ctx context.Context, playerID uint64, mailID string, claimID []byte,
) error {
	rows, err := o.store.ListClaimSagas(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.GetPlayerId() != playerID || row.GetMailId() != mailID {
			continue
		}
		if bytes.Equal(row.GetClaimId(), claimID) {
			continue
		}
		switch row.GetState() {
		case tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_CLAIMING,
			tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_PLAYER_APPLIED,
			tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_CANCELLING,
			tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_INIT:
			return ErrClaimConflict
		case tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_COMPLETED:
			return ErrClaimConflict
		}
	}
	return nil
}

func (o *ClaimOrchestrator) loadClaimableAttachments(
	ctx context.Context, playerID uint64, registeredAtMS int64, mailID string,
) ([]*tcaplusv1.MailClaimAttachment, int64, error) {
	visible, err := o.mailVisible(ctx, playerID, registeredAtMS, mailID)
	if err != nil {
		return nil, 0, err
	}
	if !visible {
		return nil, 0, fmt.Errorf("%w: mail not visible", ErrNotFound)
	}
	if public, err := o.store.GetPublicMail(ctx, mailID); err == nil {
		atts := toClaimAttachments(public.GetAttachments())
		coins := public.GetCoinAmount()
		if len(atts) == 0 && coins <= 0 {
			return nil, 0, fmt.Errorf("%w: no claimable reward", ErrNotFound)
		}
		return atts, coins, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, 0, err
	}
	private, err := o.store.GetPrivateMail(ctx, playerID, mailID)
	if err != nil {
		return nil, 0, err
	}
	atts := toClaimAttachments(private.GetAttachments())
	coins := private.GetCoinAmount()
	if len(atts) == 0 && coins <= 0 {
		return nil, 0, fmt.Errorf("%w: no claimable reward", ErrNotFound)
	}
	return atts, coins, nil
}

func (o *ClaimOrchestrator) mailVisible(
	ctx context.Context, playerID uint64, registeredAtMS int64, mailID string,
) (bool, error) {
	if public, err := o.store.GetPublicMail(ctx, mailID); err == nil {
		return public.GetPublishedAtMs() > registeredAtMS, nil
	} else if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	if _, err := o.store.GetPrivateMail(ctx, playerID, mailID); err == nil {
		return true, nil
	} else if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	return false, nil
}

func (o *ClaimOrchestrator) resolveRegistered(
	ctx context.Context, playerID uint64, requested int64,
) (int64, error) {
	if requested > 0 {
		return requested, nil
	}
	at, found, err := o.store.RegisteredAtMS(ctx, playerID)
	if err != nil {
		return 0, err
	}
	if !found || at <= 0 {
		return 0, fmt.Errorf("registered_at_ms missing for player %d", playerID)
	}
	return at, nil
}

func toClaimAttachments(in []*tcaplusv1.MailAttachment) []*tcaplusv1.MailClaimAttachment {
	out := make([]*tcaplusv1.MailClaimAttachment, 0, len(in))
	for _, attachment := range in {
		if attachment.GetItemId() == 0 || attachment.GetQuantity() == 0 {
			continue
		}
		out = append(out, &tcaplusv1.MailClaimAttachment{
			ItemId: attachment.GetItemId(), Quantity: attachment.GetQuantity(),
		})
	}
	return out
}

// appliedStateVersion rebuilds the recipient Actor version behind a reward
// application. An older Zone that predates owner_epoch reports zero, which is
// not a usable version, so the claim degrades to a snapshot reload instead.
func appliedStateVersion(applied *rpcv1.ApplyMailRewardResponse) *wsv1.StateVersion {
	if applied.GetOwnerEpoch() == 0 || applied.GetPlayerSeq() == 0 {
		return nil
	}
	return &wsv1.StateVersion{
		OwnerEpoch: applied.GetOwnerEpoch(), PlayerSeq: applied.GetPlayerSeq(),
	}
}

func claimAttachmentsToViews(in []*tcaplusv1.MailClaimAttachment) []*wsv1.ItemStackView {
	out := make([]*wsv1.ItemStackView, 0, len(in))
	for _, attachment := range in {
		out = append(out, &wsv1.ItemStackView{
			ItemId: attachment.GetItemId(), Quantity: attachment.GetQuantity(),
		})
	}
	return out
}

func attachmentsDigest(attachments []*tcaplusv1.MailClaimAttachment) []byte {
	hash := sha256.New()
	for _, attachment := range attachments {
		itemID := attachment.GetItemId()
		qty := attachment.GetQuantity()
		_, _ = hash.Write([]byte{byte(itemID >> 24), byte(itemID >> 16), byte(itemID >> 8), byte(itemID)})
		_, _ = hash.Write([]byte{byte(qty >> 24), byte(qty >> 16), byte(qty >> 8), byte(qty)})
	}
	return hash.Sum(nil)
}

func mapClaimError(err error) *mailv1.ClaimMailResponse {
	switch {
	case errors.Is(err, ErrClaimInventoryFull):
		return &mailv1.ClaimMailResponse{
			Error: &wsv1.Error{Code: wsv1.ErrorCode_INVENTORY_CAPACITY_EXCEEDED},
		}
	case errors.Is(err, ErrClaimConflict), errors.Is(err, ErrAlreadyExists):
		return &mailv1.ClaimMailResponse{Error: &wsv1.Error{Code: wsv1.ErrorCode_REQUEST_ID_CONFLICT}}
	case errors.Is(err, ErrNotFound):
		return &mailv1.ClaimMailResponse{Error: invalidArg()}
	default:
		return &mailv1.ClaimMailResponse{Error: &wsv1.Error{Code: wsv1.ErrorCode_SERVICE_UNAVAILABLE, Retryable: true}}
	}
}
