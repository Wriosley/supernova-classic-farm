package player

import (
	"context"
	"fmt"

	"github.com/Wriosley/supernova-classic-farm/server/internal/actor"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

// AwaitFriendOwnerCall is the experimental friend-action bridge. It releases
// the visitor Actor mailbox while an external Owner Zone RPC is in flight,
// then resumes on the same mailbox. It deliberately does not add durable
// pending receipts or UNKNOWN reconciliation; this is for controlled
// performance comparison only.
func (r *Runtime) AwaitFriendOwnerCall(
	ctx context.Context,
	visitorID, ownerEpoch uint64,
	call func(context.Context) ([]byte, error),
) ([]byte, error) {
	if call == nil || visitorID == 0 || ownerEpoch == 0 {
		return nil, fmt.Errorf("invalid friend owner await request")
	}
	shardID := routing.ShardForPlayer(visitorID)
	r.shardLocks[shardID].RLock()
	defer r.shardLocks[shardID].RUnlock()
	a, err := r.actorFor(ctx, visitorID, ownerEpoch)
	if err != nil {
		return nil, err
	}

	var payload []byte
	handle, err := a.mailbox.BeginAwait(ctx, func(h *actor.AwaitHandle) error {
		h.Suspend()
		go func() {
			var callErr error
			payload, callErr = call(r.backgroundCtx)
			if callErr != nil {
				h.Complete(callErr)
				return
			}
			if resumeErr := h.Resume(r.backgroundCtx, func() error { return nil }); resumeErr != nil {
				h.Complete(resumeErr)
			}
		}()
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("begin friend owner await: %w", err)
	}
	if err := handle.Wait(ctx); err != nil {
		return nil, err
	}
	return payload, nil
}
