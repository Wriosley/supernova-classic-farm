package friend

import (
	"context"
	"errors"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
)

// ErrNotFound reports a missing record for any Store lookup.
var ErrNotFound = errors.New("friend record not found")

// ErrAlreadyExists reports a primary-key conflict on Store insertion.
var ErrAlreadyExists = errors.New("friend record already exists")

// Store is the sole durable dependency of the Friend Service and its link
// Saga. Every method maps to one Tcaplus table with an explicit CAS version
// so callers own their own retry loops; Store never retries internally.
type Store interface {
	// AccountName reports the active account_name for a player, or
	// found == false when the player has no active account.
	AccountName(ctx context.Context, playerID uint64) (name string, found bool, err error)

	GetCodeCurrent(ctx context.Context, ownerPlayerID uint64) (*tcaplusv1.FriendCodeCurrent, int32, error)
	InsertCodeCurrent(ctx context.Context, record *tcaplusv1.FriendCodeCurrent) (int32, error)
	UpdateCodeCurrent(ctx context.Context, record *tcaplusv1.FriendCodeCurrent, expectedVersion int32) (int32, error)

	GetCodeLookup(ctx context.Context, code string) (*tcaplusv1.FriendCodeLookup, int32, error)
	InsertCodeLookup(ctx context.Context, record *tcaplusv1.FriendCodeLookup) (int32, error)
	UpdateCodeLookup(ctx context.Context, record *tcaplusv1.FriendCodeLookup, expectedVersion int32) (int32, error)

	GetRelation(ctx context.Context, playerLowID, playerHighID uint64) (*tcaplusv1.FriendRelation, int32, error)
	InsertRelation(ctx context.Context, record *tcaplusv1.FriendRelation) (int32, error)

	GetFriendList(ctx context.Context, playerID uint64) (*tcaplusv1.FriendList, int32, error)
	InsertFriendList(ctx context.Context, record *tcaplusv1.FriendList) (int32, error)
	UpdateFriendList(ctx context.Context, record *tcaplusv1.FriendList, expectedVersion int32) (int32, error)

	GetSaga(ctx context.Context, linkID []byte) (*tcaplusv1.FriendLinkSaga, int32, error)
	InsertSaga(ctx context.Context, record *tcaplusv1.FriendLinkSaga) (int32, error)
	UpdateSaga(ctx context.Context, record *tcaplusv1.FriendLinkSaga, expectedVersion int32) (int32, error)

	// TryClaimFirstFriendReward inserts the invitee's first-friend row.
	// claimed=true means this relation won the race; claimed=false with
	// err=nil means another relation already claimed. Store errors must not
	// be collapsed into claimed=false.
	TryClaimFirstFriendReward(
		ctx context.Context,
		inviteePlayerID, inviterPlayerID uint64,
		relationID []byte,
		friendCode string,
		claimedAtMS int64,
	) (claimed bool, err error)
}
