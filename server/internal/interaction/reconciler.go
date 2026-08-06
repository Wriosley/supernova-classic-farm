package interaction

import (
	"context"
	"errors"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"google.golang.org/protobuf/proto"
)

// interactionTraverser is implemented by Tcaplus clients (and
// testtcaplus.Client) that support a full-table scan, mirroring
// friend.sagaTraverser.
type interactionTraverser interface {
	Traverse(proto.Message) ([]proto.Message, error)
}

// RequestResolver rebuilds the ephemeral fields StealRequest needs
// (OwnerRoute, CropItemID, Quantity, VisitorOwnerEpoch) from a durable
// FriendInteraction record. These fields are deliberately not persisted on
// FriendInteraction (routes and crop configuration can move between the
// original attempt and a later reconcile), so both the original caller and
// the Reconciler resolve them the same way — from live routing/config
// state — rather than trusting a stale stored copy.
type RequestResolver interface {
	ResolveSteal(ctx context.Context, record *tcaplusv1.FriendInteraction) (StealRequest, error)
}

type ActionRequestResolver interface {
	ResolveAction(ctx context.Context, record *tcaplusv1.FriendInteraction) (ActionRequest, error)
}

// Reconciler drives incomplete or retry-due FriendInteraction records back
// toward a terminal state, recovering every crash window described in
// docs/plans/friend_design_plan/03-好友互动Saga详细设计.md §7: a visitor
// reservation saved but the record still INIT, an owner-applied receipt
// saved but the record still VISITOR_RESERVED, and a visitor-committed
// receipt saved but the record not yet COMPLETED. Every step the Saga
// takes is itself idempotent, so re-running advance from any status is
// always safe.
type Reconciler struct {
	store    Store
	saga     *StealSaga
	action   *ActionSaga
	resolver RequestResolver
	scanner  interactionTraverser
}

func (r *Reconciler) WithActionSaga(saga *ActionSaga) *Reconciler {
	r.action = saga
	return r
}

// NewReconciler builds a Reconciler. scanner may be nil; ReconcileDue then
// reports an error instead of silently doing nothing.
func NewReconciler(
	store Store, saga *StealSaga, resolver RequestResolver, scanner interactionTraverser,
) (*Reconciler, error) {
	if store == nil || saga == nil || resolver == nil {
		return nil, errors.New("interaction store, saga and resolver are required")
	}
	return &Reconciler{store: store, saga: saga, resolver: resolver, scanner: scanner}, nil
}

// ReconcileOne reloads the current durable record by interaction ID and
// drives it forward when it is not already terminal and is due (retry_at_ms
// has elapsed, or no retry was ever scheduled).
func (r *Reconciler) ReconcileOne(ctx context.Context, interactionID []byte, now time.Time) error {
	record, _, err := r.store.Get(ctx, interactionID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return r.reconcileRecord(ctx, record, now)
}

// ReconcileDue scans every FriendInteraction and reconciles the ones that
// are not yet terminal and whose retry_at_ms has elapsed. It requires a
// Tcaplus client that supports Traverse.
func (r *Reconciler) ReconcileDue(ctx context.Context, now time.Time) error {
	if r.scanner == nil {
		return errors.New("interaction Reconciler has no table scanner configured")
	}
	rows, err := r.scanner.Traverse(&tcaplusv1.FriendInteraction{})
	if err != nil {
		return err
	}
	for _, row := range rows {
		record, ok := row.(*tcaplusv1.FriendInteraction)
		if !ok {
			continue
		}
		if isTerminalInteractionStatus(record.Status) {
			continue
		}
		if record.RetryAtMs > now.UnixMilli() {
			continue
		}
		if err := r.reconcileRecord(ctx, record, now); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) reconcileRecord(ctx context.Context, record *tcaplusv1.FriendInteraction, now time.Time) error {
	if isTerminalInteractionStatus(record.Status) {
		return nil
	}
	var err error
	if datav1.FriendInteractionAction(record.Action) == datav1.FriendInteractionAction_STEAL_FRIEND_CROP {
		req, resolveErr := r.resolver.ResolveSteal(ctx, record)
		if resolveErr != nil {
			return resolveErr
		}
		req.InteractionID = record.InteractionId
		_, err = r.saga.Resume(ctx, req, now)
	} else {
		resolver, ok := r.resolver.(ActionRequestResolver)
		if !ok || r.action == nil {
			return errors.New("friend action reconciliation is unavailable")
		}
		req, resolveErr := resolver.ResolveAction(ctx, record)
		if resolveErr != nil {
			return resolveErr
		}
		req.InteractionID = record.InteractionId
		_, err = r.action.Resume(ctx, req, now)
	}
	if err != nil && (errors.Is(err, ErrOutcomeUnknown) || asAbortedError(err)) {
		// Both are expected terminal-for-now outcomes from the
		// Reconciler's point of view: ErrOutcomeUnknown means retry_at_ms
		// was durably rescheduled, and AbortedError means the interaction
		// already reached its terminal ABORTED status. Neither is a
		// reconciler failure.
		return nil
	}
	return err
}

func asAbortedError(err error) bool {
	var aborted *AbortedError
	return errors.As(err, &aborted)
}

func isTerminalInteractionStatus(status tcaplusv1.FriendInteractionStatus) bool {
	return status == tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_COMPLETED ||
		status == tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_ABORTED
}
