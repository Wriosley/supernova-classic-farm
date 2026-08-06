package friend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
)

const (
	// maxFriendsPerPlayer bounds active_count + reserved_count.
	maxFriendsPerPlayer = 100
	// reservationTTL bounds how long an un-consumed slot reservation survives
	// before the Reconciler is allowed to release it back to the pool.
	reservationTTL = 5 * time.Minute
	// maxSagaSteps bounds one synchronous Advance call; the state machine has
	// far fewer than this many transitions, so hitting it means a bug.
	maxSagaSteps = 32
)

var (
	ErrCannotFriendSelf   = errors.New("cannot friend self")
	ErrFriendLimitReached = errors.New("friend limit reached")
)

// TaskCreditor idempotently advances one player's "add friend" task once a
// FriendRelation becomes ACTIVE. Implementations must be safe to call more
// than once for the same (playerID, relationID) pair.
type TaskCreditor interface {
	ApplyFriendTaskCredit(ctx context.Context, playerID uint64, relationID []byte) (newlyApplied bool, playerSeq uint64, err error)
}

// FriendLinker drives the bidirectional friend-link Saga described in
// docs/plans/friend_design_plan/01-FriendSvr详细设计.md section 4.
type FriendLinker struct {
	store  Store
	credit TaskCreditor
	now    func() time.Time
}

func NewFriendLinker(store Store, credit TaskCreditor, now func() time.Time) (*FriendLinker, error) {
	if store == nil || credit == nil {
		return nil, errors.New("friend store and task creditor are required")
	}
	if now == nil {
		now = time.Now
	}
	return &FriendLinker{store: store, credit: credit, now: now}, nil
}

// EstablishFriendship drives (or resumes) the link Saga for one
// (share code, redeemer) pair to completion or to a stable terminal state. It
// returns the relation ID and whether this call newly created the
// relationship (false when the two players were already ACTIVE friends).
func (l *FriendLinker) EstablishFriendship(
	ctx context.Context,
	codeOwnerPlayerID, redeemerPlayerID uint64,
	code string,
	now time.Time,
) ([]byte, bool, error) {
	if codeOwnerPlayerID == 0 || redeemerPlayerID == 0 {
		return nil, false, errors.New("player IDs are required")
	}
	if codeOwnerPlayerID == redeemerPlayerID {
		return nil, false, ErrCannotFriendSelf
	}
	low, high := sortedPlayerIDs(codeOwnerPlayerID, redeemerPlayerID)

	if relation, _, err := l.store.GetRelation(ctx, low, high); err == nil {
		if relation.Status == tcaplusv1.FriendRelationStatus_FRIEND_RELATION_STATUS_ACTIVE {
			// An ACTIVE relation already authorizes friendship. Still resume
			// the Saga when one exists so a prior TASK_CREDITING failure can
			// finish; otherwise return the durable relation as a no-op.
			id := linkID(code, redeemerPlayerID)
			if saga, version, sagaErr := l.store.GetSaga(ctx, id); sagaErr == nil {
				if saga.Status != tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_COMPLETED {
					final, advanceErr := l.advance(ctx, saga, version, now)
					if advanceErr != nil {
						return nil, false, advanceErr
					}
					if len(final.RelationId) > 0 {
						return final.RelationId, false, nil
					}
				}
			} else if !errors.Is(sagaErr, ErrNotFound) {
				return nil, false, sagaErr
			}
			return relation.RelationId, false, nil
		}
	} else if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}

	id := linkID(code, redeemerPlayerID)
	saga, version, err := l.store.GetSaga(ctx, id)
	if errors.Is(err, ErrNotFound) {
		saga, version, err = l.createSaga(ctx, id, code, codeOwnerPlayerID, redeemerPlayerID, low, high, now)
	}
	if err != nil {
		return nil, false, err
	}
	newlyCreated := saga.Status != tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_COMPLETED

	final, err := l.advance(ctx, saga, version, now)
	if err != nil {
		return nil, false, err
	}
	if final.Status == tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_ABORTED {
		return nil, false, ErrFriendLimitReached
	}
	return final.RelationId, newlyCreated, nil
}

func (l *FriendLinker) createSaga(
	ctx context.Context,
	id []byte,
	code string,
	codeOwnerPlayerID, redeemerPlayerID, low, high uint64,
	now time.Time,
) (*tcaplusv1.FriendLinkSaga, int32, error) {
	relationID, err := newRelationID()
	if err != nil {
		return nil, 0, err
	}
	saga := &tcaplusv1.FriendLinkSaga{
		LinkId: id, Code: code, CodeOwnerPlayerId: codeOwnerPlayerID,
		RedeemerPlayerId: redeemerPlayerID, PlayerLowId: low, PlayerHighId: high,
		RelationId:           relationID,
		Status:               tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_INIT,
		LowTaskCreditStatus:  tcaplusv1.FriendTaskCreditStatus_FRIEND_TASK_CREDIT_STATUS_PENDING,
		HighTaskCreditStatus: tcaplusv1.FriendTaskCreditStatus_FRIEND_TASK_CREDIT_STATUS_PENDING,
		CreatedAtMs:          now.UnixMilli(),
		UpdatedAtMs:          now.UnixMilli(),
	}
	version, err := l.store.InsertSaga(ctx, saga)
	if err == nil {
		return saga, version, nil
	}
	if !errors.Is(err, ErrAlreadyExists) {
		return nil, 0, err
	}
	return l.store.GetSaga(ctx, id)
}

// advance drives the Saga's state machine forward, persisting after every
// transition so a crash or CAS conflict can resume from the last committed
// step. It returns the Saga's terminal (or currently blocked) snapshot.
func (l *FriendLinker) advance(
	ctx context.Context,
	saga *tcaplusv1.FriendLinkSaga,
	version int32,
	now time.Time,
) (*tcaplusv1.FriendLinkSaga, error) {
	for step := 0; step < maxSagaSteps; step++ {
		switch saga.Status {
		case tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_INIT:
			ok, err := l.reserveSlot(ctx, saga.PlayerLowId, saga.LinkId, saga.PlayerHighId, now)
			if err != nil {
				return nil, err
			}
			if !ok {
				saga.Status = tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_RELEASING
			} else {
				saga.Status = tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_LOW_RESERVED
			}

		case tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_LOW_RESERVED:
			ok, err := l.reserveSlot(ctx, saga.PlayerHighId, saga.LinkId, saga.PlayerLowId, now)
			if err != nil {
				return nil, err
			}
			if !ok {
				saga.Status = tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_RELEASING
			} else {
				saga.Status = tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_BOTH_RESERVED
			}

		case tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_BOTH_RESERVED:
			if err := l.createRelation(ctx, saga, now); err != nil {
				return nil, err
			}
			saga.Status = tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_RELATION_ACTIVE

		case tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_RELATION_ACTIVE:
			if err := l.projectBothSides(ctx, saga, now); err != nil {
				return nil, err
			}
			saga.Status = tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_PROJECTIONS_APPLIED

		case tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_PROJECTIONS_APPLIED:
			saga.Status = tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_TASK_CREDITING

		case tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_TASK_CREDITING:
			var creditErr error
			saga, version, creditErr = l.creditBothSides(ctx, saga, version, now)
			if creditErr != nil {
				// Task credit failures are deliberately non-fatal to the
				// caller: the friend relationship is already ACTIVE. The
				// Saga stays at TASK_CREDITING with retry_at_ms/last_error
				// for the Reconciler to retry later.
				return saga, nil
			}
			saga.Status = tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_COMPLETED

		case tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_RELEASING:
			l.releaseBothSides(ctx, saga, now)
			saga.Status = tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_ABORTED

		case tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_COMPLETED,
			tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_ABORTED:
			return saga, nil

		default:
			return nil, fmt.Errorf("friend link Saga has an unknown status %d", saga.Status)
		}

		saga.UpdatedAtMs = now.UnixMilli()
		next, err := l.store.UpdateSaga(ctx, saga, version)
		if err != nil {
			return nil, err
		}
		version = next
	}
	return nil, errors.New("friend link Saga did not converge")
}

// creditBothSides applies task credit to whichever side has not yet been
// marked APPLIED, persisting the Saga after each side so a retry never
// double-credits. It returns the (possibly updated) saga/version even when it
// returns an error so the caller can persist the last-error state.
func (l *FriendLinker) creditBothSides(
	ctx context.Context,
	saga *tcaplusv1.FriendLinkSaga,
	version int32,
	now time.Time,
) (*tcaplusv1.FriendLinkSaga, int32, error) {
	sides := []struct {
		playerID uint64
		status   *tcaplusv1.FriendTaskCreditStatus
	}{
		{saga.PlayerLowId, &saga.LowTaskCreditStatus},
		{saga.PlayerHighId, &saga.HighTaskCreditStatus},
	}
	for _, side := range sides {
		if *side.status == tcaplusv1.FriendTaskCreditStatus_FRIEND_TASK_CREDIT_STATUS_APPLIED {
			continue
		}
		if _, _, err := l.credit.ApplyFriendTaskCredit(ctx, side.playerID, saga.RelationId); err != nil {
			saga.LastErrorCode = "TASK_CREDIT_FAILED"
			saga.RetryAtMs = now.Add(30 * time.Second).UnixMilli()
			saga.UpdatedAtMs = now.UnixMilli()
			next, updateErr := l.store.UpdateSaga(ctx, saga, version)
			if updateErr != nil {
				return saga, version, updateErr
			}
			return saga, next, err
		}
		*side.status = tcaplusv1.FriendTaskCreditStatus_FRIEND_TASK_CREDIT_STATUS_APPLIED
		saga.LastErrorCode = ""
		saga.RetryAtMs = 0
		saga.UpdatedAtMs = now.UnixMilli()
		next, err := l.store.UpdateSaga(ctx, saga, version)
		if err != nil {
			return saga, version, err
		}
		version = next
	}
	return saga, version, nil
}

// reserveSlot reserves one FriendList slot for friendPlayerID under linkID,
// bounded by the 100-friend limit. It is idempotent: an already-ACTIVE
// reservation for the same linkID is treated as success without re-counting.
func (l *FriendLinker) reserveSlot(
	ctx context.Context,
	playerID uint64,
	id []byte,
	friendPlayerID uint64,
	now time.Time,
) (bool, error) {
	for attempt := 0; attempt < tcaplusMaxCASAttempts; attempt++ {
		list, version, err := l.getOrCreateFriendList(ctx, playerID, now)
		if err != nil {
			return false, err
		}
		for _, entry := range list.Entries {
			if entry.FriendPlayerId == friendPlayerID {
				return true, nil
			}
		}
		for _, reservation := range list.Reservations {
			if bytes.Equal(reservation.LinkId, id) &&
				reservation.Status == tcaplusv1.FriendReservationStatus_FRIEND_RESERVATION_STATUS_ACTIVE {
				return true, nil
			}
		}
		if list.ActiveCount+list.ReservedCount >= maxFriendsPerPlayer {
			return false, nil
		}
		list.Reservations = append(list.Reservations, &tcaplusv1.FriendSlotReservation{
			LinkId: id, FriendPlayerId: friendPlayerID,
			Status:      tcaplusv1.FriendReservationStatus_FRIEND_RESERVATION_STATUS_ACTIVE,
			CreatedAtMs: now.UnixMilli(), ExpiresAtMs: now.Add(reservationTTL).UnixMilli(),
		})
		list.ReservedCount++
		list.UpdatedAtMs = now.UnixMilli()
		if _, err := l.store.UpdateFriendList(ctx, list, version); err != nil {
			continue
		}
		return true, nil
	}
	return false, errors.New("reserve friend slot conflicted too many times")
}

func (l *FriendLinker) getOrCreateFriendList(
	ctx context.Context, playerID uint64, now time.Time,
) (*tcaplusv1.FriendList, int32, error) {
	list, version, err := l.store.GetFriendList(ctx, playerID)
	if err == nil {
		return list, version, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, 0, err
	}
	list = &tcaplusv1.FriendList{PlayerId: playerID, UpdatedAtMs: now.UnixMilli()}
	version, err = l.store.InsertFriendList(ctx, list)
	if err == nil {
		return list, version, nil
	}
	if !errors.Is(err, ErrAlreadyExists) {
		return nil, 0, err
	}
	return l.store.GetFriendList(ctx, playerID)
}

func (l *FriendLinker) createRelation(
	ctx context.Context, saga *tcaplusv1.FriendLinkSaga, now time.Time,
) error {
	relation := &tcaplusv1.FriendRelation{
		PlayerLowId: saga.PlayerLowId, PlayerHighId: saga.PlayerHighId,
		RelationId:  saga.RelationId,
		Status:      tcaplusv1.FriendRelationStatus_FRIEND_RELATION_STATUS_ACTIVE,
		CreatedAtMs: now.UnixMilli(), UpdatedAtMs: now.UnixMilli(),
	}
	if _, err := l.store.InsertRelation(ctx, relation); err != nil {
		if !errors.Is(err, ErrAlreadyExists) {
			return err
		}
		existing, _, err := l.store.GetRelation(ctx, saga.PlayerLowId, saga.PlayerHighId)
		if err != nil {
			return err
		}
		// A concurrent Saga for a different code between the same pair won
		// the race; adopt its relation ID so this Saga's steps stay
		// idempotent against the record that actually exists.
		saga.RelationId = existing.RelationId
	}
	return nil
}

func (l *FriendLinker) projectBothSides(
	ctx context.Context, saga *tcaplusv1.FriendLinkSaga, now time.Time,
) error {
	if err := l.consumeReservation(ctx, saga.PlayerLowId, saga.LinkId, saga.PlayerHighId, saga.RelationId, now); err != nil {
		return err
	}
	if err := l.consumeReservation(ctx, saga.PlayerHighId, saga.LinkId, saga.PlayerLowId, saga.RelationId, now); err != nil {
		return err
	}
	return nil
}

// consumeReservation converts playerID's pending reservation into a durable
// FriendListEntry for friendPlayerID. It is idempotent on relationID.
func (l *FriendLinker) consumeReservation(
	ctx context.Context,
	playerID uint64,
	id []byte,
	friendPlayerID uint64,
	relationID []byte,
	now time.Time,
) error {
	name, found, err := l.store.AccountName(ctx, friendPlayerID)
	if err != nil {
		return err
	}
	if !found || name == "" {
		return fmt.Errorf("friend player %d has no active account name", friendPlayerID)
	}
	for attempt := 0; attempt < tcaplusMaxCASAttempts; attempt++ {
		list, version, err := l.getOrCreateFriendList(ctx, playerID, now)
		if err != nil {
			return err
		}
		alreadyProjected := false
		for _, entry := range list.Entries {
			if bytes.Equal(entry.RelationId, relationID) {
				alreadyProjected = true
				break
			}
		}
		if alreadyProjected {
			return nil
		}
		remaining := list.Reservations[:0:0]
		removed := false
		for _, reservation := range list.Reservations {
			if bytes.Equal(reservation.LinkId, id) {
				removed = true
				continue
			}
			remaining = append(remaining, reservation)
		}
		list.Reservations = remaining
		if removed && list.ReservedCount > 0 {
			list.ReservedCount--
		}
		list.Entries = append(list.Entries, &tcaplusv1.FriendListEntry{
			FriendPlayerId: friendPlayerID, AccountName: name,
			RelationId: relationID, CreatedAtMs: now.UnixMilli(),
		})
		list.ActiveCount++
		list.UpdatedAtMs = now.UnixMilli()
		if _, err := l.store.UpdateFriendList(ctx, list, version); err != nil {
			continue
		}
		return nil
	}
	return errors.New("project friend list conflicted too many times")
}

func (l *FriendLinker) releaseBothSides(ctx context.Context, saga *tcaplusv1.FriendLinkSaga, now time.Time) {
	_ = l.releaseReservation(ctx, saga.PlayerLowId, saga.LinkId, now)
	_ = l.releaseReservation(ctx, saga.PlayerHighId, saga.LinkId, now)
}

func (l *FriendLinker) releaseReservation(
	ctx context.Context, playerID uint64, id []byte, now time.Time,
) error {
	for attempt := 0; attempt < tcaplusMaxCASAttempts; attempt++ {
		list, version, err := l.store.GetFriendList(ctx, playerID)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		remaining := list.Reservations[:0:0]
		removed := false
		for _, reservation := range list.Reservations {
			if bytes.Equal(reservation.LinkId, id) {
				removed = true
				continue
			}
			remaining = append(remaining, reservation)
		}
		if !removed {
			return nil
		}
		list.Reservations = remaining
		if list.ReservedCount > 0 {
			list.ReservedCount--
		}
		list.UpdatedAtMs = now.UnixMilli()
		if _, err := l.store.UpdateFriendList(ctx, list, version); err != nil {
			continue
		}
		return nil
	}
	return errors.New("release friend slot conflicted too many times")
}
