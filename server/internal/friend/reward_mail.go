package friend

import (
	"context"
	"fmt"
)

const (
	// firstFriendRewardCoins matches plan 04-4.
	firstFriendRewardCoins int64 = 10
	// grapeSeedItemID is CropCatalog 葡萄 seed (config.go).
	grapeSeedItemID uint32 = 1014
	grapeSeedQty    uint32 = 4
	firstFriendMailTitle = "首次好友奖励"
)

// RewardMailer creates the system reward mails for a first-friend claim.
// Implementations must be idempotent on source_event_id.
type RewardMailer interface {
	CreateSystemRewardMail(
		ctx context.Context,
		sourceEventID string,
		recipientPlayerID uint64,
		title, content, senderDisplayName string,
		attachments []RewardMailAttachment,
		coinAmount int64,
	) (mailID string, alreadyApplied bool, err error)
}

// RewardMailAttachment is one item stack on a system reward mail.
type RewardMailAttachment struct {
	ItemID   uint32
	Quantity uint32
}

func firstFriendInviterSourceID(inviteePlayerID uint64) string {
	return fmt.Sprintf("first-friend:%d:inviter", inviteePlayerID)
}

func firstFriendInviteeSourceID(inviteePlayerID uint64) string {
	return fmt.Sprintf("first-friend:%d:invitee", inviteePlayerID)
}

func firstFriendAttachments() []RewardMailAttachment {
	return []RewardMailAttachment{{ItemID: grapeSeedItemID, Quantity: grapeSeedQty}}
}
