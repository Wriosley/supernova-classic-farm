// Package interaction implements the cross-Actor friend-interaction Saga
// described in docs/plans/friend_design_plan/03-好友互动Saga详细设计.md. Phase 5
// implements only STEAL_FRIEND_CROP; pest/catch/help are left to Phase 6,
// but Store, the persisted FriendInteractionStatus state machine and the
// Reconciler's scan loop are already generic across every friend action.
package interaction

import (
	"context"
	"errors"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
)

// ErrNotFound reports a missing FriendInteraction record.
var ErrNotFound = errors.New("interaction record not found")

// ErrAlreadyExists reports a primary-key conflict on Store insertion.
var ErrAlreadyExists = errors.New("interaction record already exists")

// Store is the sole durable dependency of the interaction Saga and its
// Reconciler. Every method maps to the single FriendInteraction Tcaplus
// table with an explicit CAS version so callers own their own retry loops;
// Store never retries internally, exactly like friend.Store.
type Store interface {
	Get(ctx context.Context, interactionID []byte) (*tcaplusv1.FriendInteraction, int32, error)
	Insert(ctx context.Context, record *tcaplusv1.FriendInteraction) (int32, error)
	Update(ctx context.Context, record *tcaplusv1.FriendInteraction, expectedVersion int32) (int32, error)
}
