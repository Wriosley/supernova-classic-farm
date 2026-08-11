package player

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

var (
	ErrMailInventoryCapacity = errors.New("mail reward exceeds inventory capacity")
	ErrMailClaimConflict     = errors.New("mail claim attachment conflict")
)

// MailRewardAttachment is one attachment applied by ApplyMailReward.
type MailRewardAttachment struct {
	ItemID   uint32
	Quantity uint32
}

// ApplyMailRewardResult is returned after a durable ApplyMailReward step.
type ApplyMailRewardResult struct {
	NewlyApplied bool
	PlayerSeq    uint64
	ItemsAdded   []*wsv1.ItemStackView
	Patch        *wsv1.PlayerStatePatch
}

// ApplyMailReward idempotently grants mail attachments, keyed by
// (mail_id, claim_id). Inventory is checked all-or-nothing; success requires
// a synchronous SaveCAS before returning.
func (r *Runtime) ApplyMailReward(
	ctx context.Context,
	playerID uint64,
	ownerEpoch uint64,
	claimID []byte,
	mailID string,
	attachments []MailRewardAttachment,
) (ApplyMailRewardResult, error) {
	var empty ApplyMailRewardResult
	if ownerEpoch == 0 {
		return empty, ErrNotOwner
	}
	if len(claimID) != 16 || mailID == "" || len(attachments) == 0 {
		return empty, errors.New("invalid mail reward request")
	}
	shardID := routing.ShardForPlayer(playerID)
	r.shardLocks[shardID].RLock()
	defer r.shardLocks[shardID].RUnlock()

	a, err := r.actorFor(ctx, playerID, ownerEpoch)
	if err != nil {
		return empty, err
	}
	now := r.now().UTC()
	stepKey := syncStepKey(syncStepApplyMailReward, claimID)
	var result ApplyMailRewardResult
	var mailboxErr error
	if err := a.mailbox.Do(ctx, func() {
		beforeRevision := a.state.CheckpointRevision
		result, mailboxErr = applyMailReward(a.state, claimID, mailID, attachments, now)
		if mailboxErr == nil && a.state.CheckpointRevision != beforeRevision {
			a.markSyncPending(stepKey, pendingSyncStep{revision: a.state.CheckpointRevision})
		}
	}); err != nil {
		return empty, fmt.Errorf("execute mail reward mailbox: %w", err)
	}
	if mailboxErr != nil {
		return empty, mailboxErr
	}
	if _, err := r.settleSyncStepLocked(ctx, playerID, a, stepKey); err != nil {
		return empty, fmt.Errorf("flush mail reward: %w", err)
	}
	return result, nil
}

func applyMailReward(
	state *State,
	claimID []byte,
	mailID string,
	attachments []MailRewardAttachment,
	now time.Time,
) (ApplyMailRewardResult, error) {
	var empty ApplyMailRewardResult
	if state == nil {
		return empty, errors.New("player state is required")
	}
	normalized, digest, err := normalizeMailAttachments(attachments)
	if err != nil {
		return empty, err
	}
	for _, receipt := range state.MailClaimReceipts {
		if receipt.GetMailId() != mailID || !bytes.Equal(receipt.GetClaimId(), claimID) {
			continue
		}
		if !sameMailAttachmentStacks(receipt.GetAttachments(), normalized) {
			return empty, ErrMailClaimConflict
		}
		return replayMailReward(state, receipt), nil
	}
	for _, receipt := range state.MailClaimReceipts {
		if receipt.GetMailId() == mailID {
			return empty, ErrMailClaimConflict
		}
	}
	projected := cloneInventory(state.Inventory)
	if err := projectMailAttachments(projected, normalized); err != nil {
		return empty, err
	}

	itemsAdded := make([]*wsv1.ItemStackView, 0, len(normalized))
	upserts := make([]*wsv1.ItemStackView, 0, len(normalized))
	for _, attachment := range normalized {
		before := state.Inventory[attachment.ItemId]
		after := projected[attachment.ItemId]
		state.Inventory[attachment.ItemId] = after
		itemsAdded = append(itemsAdded, &wsv1.ItemStackView{
			ItemId: attachment.ItemId, Quantity: attachment.Quantity,
		})
		upserts = append(upserts, &wsv1.ItemStackView{
			ItemId: attachment.ItemId, Quantity: after,
		})
		_ = before
	}
	receipt := &datav1.MailClaimReceipt{
		MailId: mailID, ClaimId: append([]byte(nil), claimID...),
		AppliedAtMs: now.UnixMilli(), Attachments: normalized,
	}
	_ = digest
	state.MailClaimReceipts = append(state.MailClaimReceipts, receipt)
	state.PlayerSeq++
	state.CheckpointRevision++
	state.UpdatedAtMS = now.UnixMilli()
	return ApplyMailRewardResult{
		NewlyApplied: true,
		PlayerSeq:    state.PlayerSeq,
		ItemsAdded:   itemsAdded,
		Patch:        &wsv1.PlayerStatePatch{InventoryUpserts: upserts},
	}, nil
}

func replayMailReward(state *State, receipt *datav1.MailClaimReceipt) ApplyMailRewardResult {
	items := make([]*wsv1.ItemStackView, 0, len(receipt.GetAttachments()))
	upserts := make([]*wsv1.ItemStackView, 0, len(receipt.GetAttachments()))
	for _, attachment := range receipt.GetAttachments() {
		items = append(items, &wsv1.ItemStackView{
			ItemId: attachment.ItemId, Quantity: attachment.Quantity,
		})
		upserts = append(upserts, &wsv1.ItemStackView{
			ItemId: attachment.ItemId, Quantity: state.Inventory[attachment.ItemId],
		})
	}
	return ApplyMailRewardResult{
		NewlyApplied: false,
		PlayerSeq:    state.PlayerSeq,
		ItemsAdded:   items,
		Patch:        &wsv1.PlayerStatePatch{InventoryUpserts: upserts},
	}
}

func normalizeMailAttachments(in []MailRewardAttachment) ([]*datav1.InventoryStack, []byte, error) {
	if len(in) == 0 || len(in) > 8 {
		return nil, nil, errors.New("invalid mail attachment count")
	}
	merged := make(map[uint32]uint32, len(in))
	order := make([]uint32, 0, len(in))
	for _, attachment := range in {
		if attachment.ItemID == 0 || attachment.Quantity == 0 || attachment.Quantity > inventoryStackLimit {
			return nil, nil, errors.New("invalid mail attachment")
		}
		if _, exists := merged[attachment.ItemID]; !exists {
			order = append(order, attachment.ItemID)
		}
		next := merged[attachment.ItemID] + attachment.Quantity
		if next < merged[attachment.ItemID] || next > inventoryStackLimit {
			return nil, nil, ErrMailInventoryCapacity
		}
		merged[attachment.ItemID] = next
	}
	for i := 0; i < len(order); i++ {
		for j := i + 1; j < len(order); j++ {
			if order[i] > order[j] {
				order[i], order[j] = order[j], order[i]
			}
		}
	}
	out := make([]*datav1.InventoryStack, 0, len(order))
	hash := sha256.New()
	for _, itemID := range order {
		qty := merged[itemID]
		out = append(out, &datav1.InventoryStack{ItemId: itemID, Quantity: qty})
		_, _ = hash.Write([]byte{byte(itemID >> 24), byte(itemID >> 16), byte(itemID >> 8), byte(itemID)})
		_, _ = hash.Write([]byte{byte(qty >> 24), byte(qty >> 16), byte(qty >> 8), byte(qty)})
	}
	return out, hash.Sum(nil), nil
}

func sameMailAttachmentStacks(left, right []*datav1.InventoryStack) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].GetItemId() != right[i].GetItemId() ||
			left[i].GetQuantity() != right[i].GetQuantity() {
			return false
		}
	}
	return true
}

func cloneInventory(in map[uint32]uint32) map[uint32]uint32 {
	out := make(map[uint32]uint32, len(in))
	for itemID, quantity := range in {
		out[itemID] = quantity
	}
	return out
}

func projectMailAttachments(inventory map[uint32]uint32, attachments []*datav1.InventoryStack) error {
	typeCount := len(inventory)
	for _, attachment := range attachments {
		current := inventory[attachment.ItemId]
		if current == 0 {
			if typeCount >= inventoryTypeLimit {
				return ErrMailInventoryCapacity
			}
			typeCount++
		}
		next := current + attachment.Quantity
		if next < current || next > inventoryStackLimit {
			return ErrMailInventoryCapacity
		}
		inventory[attachment.ItemId] = next
	}
	return nil
}
