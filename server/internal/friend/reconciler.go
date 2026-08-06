package friend

import (
	"context"
	"errors"
	"time"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"google.golang.org/protobuf/proto"
)

// sagaTraverser is implemented by Tcaplus clients (and testtcaplus.Client)
// that support a full-table scan. Production Tcaplus supports Traverse, so
// FriendSvr can enable the ticking Reconciler; it is optional so unit tests
// can exercise ReconcileSaga directly without a table scan.
type sagaTraverser interface {
	Traverse(proto.Message) ([]proto.Message, error)
}

// Reconciler drives incomplete or stuck FriendLinkSagas back toward a
// terminal state: continuing a Saga interrupted mid-reservation, retrying
// task credit after RetryAtMs, or releasing reservations on an aborted path.
type Reconciler struct {
	store   Store
	linker  *FriendLinker
	scanner sagaTraverser
}

// NewReconciler builds a Reconciler. scanner may be nil; ReconcileDue then
// reports an error instead of silently doing nothing, so callers notice a
// misconfiguration instead of a Zone that never reconciles.
func NewReconciler(store Store, linker *FriendLinker, scanner sagaTraverser) (*Reconciler, error) {
	if store == nil || linker == nil {
		return nil, errors.New("friend store and linker are required")
	}
	return &Reconciler{store: store, linker: linker, scanner: scanner}, nil
}

// ReconcileSaga reloads the current durable Saga by link_id and drives it
// forward when it is not already terminal, respecting the TASK_CREDITING
// backoff recorded in retry_at_ms.
func (r *Reconciler) ReconcileSaga(ctx context.Context, saga *tcaplusv1.FriendLinkSaga, now time.Time) error {
	if saga == nil || len(saga.LinkId) == 0 {
		return errors.New("saga link_id is required")
	}
	current, version, err := r.store.GetSaga(ctx, saga.LinkId)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if isTerminalSagaStatus(current.Status) {
		return nil
	}
	if current.Status == tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_TASK_CREDITING &&
		current.RetryAtMs > now.UnixMilli() {
		return nil
	}
	_, err = r.linker.advance(ctx, current, version, now)
	return err
}

// ReconcileDue scans every FriendLinkSaga and reconciles the ones that are
// not yet terminal. It requires a Tcaplus client that supports Traverse.
func (r *Reconciler) ReconcileDue(ctx context.Context, now time.Time) error {
	if r.scanner == nil {
		return errors.New("friend Reconciler has no table scanner configured")
	}
	rows, err := r.scanner.Traverse(&tcaplusv1.FriendLinkSaga{})
	if err != nil {
		return err
	}
	for _, row := range rows {
		saga, ok := row.(*tcaplusv1.FriendLinkSaga)
		if !ok {
			continue
		}
		if err := r.ReconcileSaga(ctx, saga, now); err != nil {
			return err
		}
	}
	return nil
}

func isTerminalSagaStatus(status tcaplusv1.FriendLinkSagaStatus) bool {
	return status == tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_COMPLETED ||
		status == tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_ABORTED
}
