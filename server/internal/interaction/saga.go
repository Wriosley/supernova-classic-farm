package interaction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"google.golang.org/protobuf/proto"
)

// maxSagaSteps bounds one synchronous advance call; STEAL_FRIEND_CROP's
// state machine has far fewer transitions, so hitting it means a bug.
const maxSagaSteps = 32

// defaultRetryBackoff is how long a transport/unknown-outcome step defers
// retry_at_ms before the Reconciler (or a client retry with the same
// interaction ID) tries again.
const defaultRetryBackoff = 30 * time.Second

// transportErrorCode is stored in FriendInteraction.error_code while a step
// is retryable-unknown; it is never surfaced to a client as a stable
// ws.ErrorCode (see AbortedError for that).
const transportErrorCode = "TRANSPORT_ERROR"

var (
	// ErrDigestConflict reports that req's canonical request digest does
	// not match the digest already stored for this interaction ID: a
	// REQUEST_ID_CONFLICT retry (docs/contracts/idempotency-and-errors.md §3.1).
	ErrDigestConflict = errors.New("interaction request digest conflicts with an existing record")
	// ErrOutcomeUnknown reports that a durable step's outcome could not be
	// confirmed (a transport failure, not a domain rejection). The caller
	// must surface INTERACTION_OUTCOME_UNKNOWN and preserve the same
	// interaction ID for a later retry; the Saga never guesses success or
	// failure for this case.
	ErrOutcomeUnknown = errors.New("interaction outcome is unknown; retry with the same interaction ID")
)

// AbortedError reports that a steal interaction durably reached ABORTED
// with a stable, client-facing ws.ErrorCode: RELEASING already ran (or
// needed nothing to release), so the visitor holds no live reservation.
type AbortedError struct {
	Code wsv1.ErrorCode
}

func (e *AbortedError) Error() string {
	return fmt.Sprintf("friend interaction aborted: %s", e.Code)
}

// VisitorSteps is the visitor Actor's synchronous Saga steps
// (player.Runtime's ReserveSteal/CommitSteal/ReleaseSteal), narrowed to an
// interface so this package never imports internal/player and stays free
// of any import cycle with it.
type VisitorSteps interface {
	ReserveSteal(
		ctx context.Context,
		visitorID, ownerEpoch uint64,
		interactionID []byte,
		cropItemID, quantity uint32,
	) (alreadyReserved bool, err error)

	CommitSteal(
		ctx context.Context,
		visitorID, ownerEpoch uint64,
		interactionID []byte,
		cropItemID, quantity uint32,
		ownerResultPayload []byte,
	) (response *wsv1.FriendActionResponse, alreadyCommitted bool, err error)

	ReleaseSteal(ctx context.Context, visitorID, ownerEpoch uint64, interactionID []byte) error

	ReserveActionChance(
		ctx context.Context, visitorID, ownerEpoch uint64, interactionID []byte,
		action datav1.FriendInteractionAction,
	) (bool, error)
	CommitActionChance(
		ctx context.Context, visitorID, ownerEpoch uint64, interactionID []byte,
		action datav1.FriendInteractionAction, ownerResultPayload []byte,
	) (*wsv1.FriendActionResponse, bool, error)
	ReleaseActionChance(
		ctx context.Context, visitorID, ownerEpoch uint64, interactionID []byte,
		action datav1.FriendInteractionAction,
	) error
}

// OwnerFarmClient is the visitor Zone's gRPC view of the owner's
// ApplyVisitorAction, narrowed to just this one method so this package
// never imports internal/visit either.
type OwnerFarmClient interface {
	ApplyVisitorAction(
		ctx context.Context, request *rpcv1.ApplyVisitorActionRequest,
	) (*rpcv1.ApplyVisitorActionResponse, error)
}

// StealRequest carries everything one STEAL_FRIEND_CROP interaction needs.
// CropItemID and Quantity are resolved by the caller before invoking the
// Saga (see cmd/zone's use of player.Runtime.SoleStealableCrop): they are
// not persisted on FriendInteraction, so Resume (used by the Reconciler)
// must re-resolve them identically on every call, exactly like
// VisitorOwnerEpoch and OwnerRoute.
//
// OwnerRoute is advisory: the production OwnerFarmClient
// (visit.ZoneOwnerFarmClient) re-resolves the owner's live route from
// OwnerPlayerID through the Coordinator on every call and overwrites it, so
// callers that always go through that client (cmd/zone, the Reconciler) may
// leave it nil or stale.
type StealRequest struct {
	InteractionID     []byte
	VisitorPlayerID   uint64
	VisitorOwnerEpoch uint64
	OwnerPlayerID     uint64
	OwnerRoute        *rpcv1.CommittedRoute
	VisitID           []byte
	PlotID            uint32
	CropItemID        uint32
	Quantity          uint32
}

func (r StealRequest) validate() error {
	if len(r.InteractionID) != 16 || r.VisitorPlayerID == 0 || r.VisitorOwnerEpoch == 0 ||
		r.OwnerPlayerID == 0 || len(r.VisitID) != 16 ||
		r.PlotID == 0 || r.CropItemID == 0 || r.Quantity == 0 {
		return errors.New("steal request is incomplete")
	}
	return nil
}

// StealSaga drives the STEAL_FRIEND_CROP interaction Saga described in
// docs/plans/friend_design_plan/03-好友互动Saga详细设计.md §4-§6. It is
// structured generically enough to grow a sibling method per friend action
// in Phase 6, but only steal is implemented.
type StealSaga struct {
	store   Store
	visitor VisitorSteps
	owner   OwnerFarmClient
}

func NewStealSaga(store Store, visitor VisitorSteps, owner OwnerFarmClient) (*StealSaga, error) {
	if store == nil || visitor == nil || owner == nil {
		return nil, errors.New("interaction store, visitor steps and owner client are required")
	}
	return &StealSaga{store: store, visitor: visitor, owner: owner}, nil
}

// Execute creates (or loads) the FriendInteraction for req.InteractionID and
// drives it to a terminal or currently-retryable state, returning the final
// FriendActionResponse on success (including on an idempotent replay of an
// already-COMPLETED interaction). See advance for the error contract.
func (s *StealSaga) Execute(ctx context.Context, req StealRequest, now time.Time) (*wsv1.FriendActionResponse, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	digest := RequestDigest(
		datav1.FriendInteractionAction_STEAL_FRIEND_CROP,
		req.VisitorPlayerID, req.OwnerPlayerID, req.VisitID, req.PlotID, 0,
	)
	record, version, err := s.createOrLoad(ctx, req, digest, now)
	if err != nil {
		return nil, err
	}
	return s.advance(ctx, record, version, req, now)
}

// Resume drives an already-durable, non-terminal FriendInteraction forward
// again without creating one: the Reconciler uses this for records whose
// retry_at_ms is due, and Execute's own callers may use it to retry after
// ErrOutcomeUnknown once they have re-resolved req's ephemeral fields.
func (s *StealSaga) Resume(ctx context.Context, req StealRequest, now time.Time) (*wsv1.FriendActionResponse, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	record, version, err := s.store.Get(ctx, req.InteractionID)
	if err != nil {
		return nil, err
	}
	return s.advance(ctx, record, version, req, now)
}

func (s *StealSaga) createOrLoad(
	ctx context.Context, req StealRequest, digest []byte, now time.Time,
) (*tcaplusv1.FriendInteraction, int32, error) {
	record, version, err := s.store.Get(ctx, req.InteractionID)
	if err == nil {
		if !bytes.Equal(record.RequestDigestSha256, digest) {
			return nil, 0, ErrDigestConflict
		}
		return record, version, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, 0, err
	}
	record = &tcaplusv1.FriendInteraction{
		InteractionId:       append([]byte(nil), req.InteractionID...),
		VisitorPlayerId:     req.VisitorPlayerID,
		OwnerPlayerId:       req.OwnerPlayerID,
		VisitId:             append([]byte(nil), req.VisitID...),
		Action:              uint32(datav1.FriendInteractionAction_STEAL_FRIEND_CROP),
		PlotId:              req.PlotID,
		RequestDigestSha256: digest,
		Status:              tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_INIT,
		CreatedAtMs:         now.UnixMilli(),
		UpdatedAtMs:         now.UnixMilli(),
	}
	version, err = s.store.Insert(ctx, record)
	if err == nil {
		return record, version, nil
	}
	if !errors.Is(err, ErrAlreadyExists) {
		return nil, 0, err
	}
	record, version, err = s.store.Get(ctx, req.InteractionID)
	if err != nil {
		return nil, 0, err
	}
	if !bytes.Equal(record.RequestDigestSha256, digest) {
		return nil, 0, ErrDigestConflict
	}
	return record, version, nil
}

// advance drives the Saga's state machine forward, persisting after every
// transition so a crash or CAS conflict can resume from the last committed
// step, exactly like friend.FriendLinker.advance.
func (s *StealSaga) advance(
	ctx context.Context,
	record *tcaplusv1.FriendInteraction,
	version int32,
	req StealRequest,
	now time.Time,
) (*wsv1.FriendActionResponse, error) {
	for step := 0; step < maxSagaSteps; step++ {
		switch record.Status {
		case tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_INIT:
			_, err := s.visitor.ReserveSteal(
				ctx, req.VisitorPlayerID, req.VisitorOwnerEpoch, record.InteractionId,
				req.CropItemID, req.Quantity,
			)
			if err != nil {
				if code, ok := visitorDomainErrorCode(err); ok {
					record.Status = tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_RELEASING
					record.ErrorCode = code.String()
					break
				}
				return s.deferRetry(ctx, record, version, err, now)
			}
			record.Status = tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_VISITOR_RESERVED

		case tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_VISITOR_RESERVED:
			response, err := s.owner.ApplyVisitorAction(ctx, &rpcv1.ApplyVisitorActionRequest{
				OwnerRoute: req.OwnerRoute, OwnerPlayerId: req.OwnerPlayerID,
				VisitorPlayerId: req.VisitorPlayerID, VisitId: req.VisitID,
				InteractionId: record.InteractionId,
				Action:        datav1.FriendInteractionAction_STEAL_FRIEND_CROP,
				PlotId:        req.PlotID, RequestDigestSha256: record.RequestDigestSha256,
			})
			if err != nil {
				return s.deferRetry(ctx, record, version, err, now)
			}
			if wsErr := response.GetError(); wsErr != nil {
				// The owner definitively rejected the action (e.g.
				// STEAL_NOT_AVAILABLE) without mutating: this is
				// deterministic, not ambiguous, so release and abort now.
				record.Status = tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_RELEASING
				record.ErrorCode = wsErr.Code.String()
				break
			}
			// The owner's own result_payload only proves what the owner's
			// receipt committed (its digest is kept verbatim below); the
			// visitor-facing intermediate payload additionally carries the
			// owner's farm_patch, which CommitSteal recovers so the final
			// FriendActionResponse the visitor commits can echo it back.
			intermediate := &wsv1.FriendActionResponse{
				InteractionId: record.InteractionId,
				FarmPatch:     response.GetFarmPatch(),
			}
			body, marshalErr := proto.MarshalOptions{Deterministic: true}.Marshal(intermediate)
			if marshalErr != nil {
				return nil, fmt.Errorf("marshal owner-applied intermediate result: %w", marshalErr)
			}
			record.ResultPayload = body
			record.ResultDigestSha256 = append([]byte(nil), response.GetResultDigestSha256()...)
			record.Status = tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_OWNER_APPLIED

		case tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_OWNER_APPLIED:
			response, _, err := s.visitor.CommitSteal(
				ctx, req.VisitorPlayerID, req.VisitorOwnerEpoch, record.InteractionId,
				req.CropItemID, req.Quantity, record.ResultPayload,
			)
			if err != nil {
				return s.deferRetry(ctx, record, version, err, now)
			}
			body, marshalErr := proto.MarshalOptions{Deterministic: true}.Marshal(response)
			if marshalErr != nil {
				return nil, fmt.Errorf("marshal final steal response: %w", marshalErr)
			}
			digest := sha256.Sum256(body)
			record.ResultPayload = body
			record.ResultDigestSha256 = digest[:]
			record.Status = tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_VISITOR_COMMITTED

		case tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_VISITOR_COMMITTED:
			record.Status = tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_COMPLETED

		case tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_COMPLETED:
			response := &wsv1.FriendActionResponse{}
			if err := proto.Unmarshal(record.ResultPayload, response); err != nil {
				return nil, fmt.Errorf("decode completed steal result: %w", err)
			}
			return response, nil

		case tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_RELEASING:
			// Idempotent no-op when nothing (or an already-released/consumed
			// reservation) exists for this interaction ID.
			_ = s.visitor.ReleaseSteal(ctx, req.VisitorPlayerID, req.VisitorOwnerEpoch, record.InteractionId)
			record.Status = tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_ABORTED

		case tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_ABORTED:
			return nil, &AbortedError{Code: parseErrorCode(record.ErrorCode)}

		default:
			return nil, fmt.Errorf("steal interaction Saga has an unknown status %d", record.Status)
		}

		record.RetryAtMs = 0
		record.UpdatedAtMs = now.UnixMilli()
		next, err := s.store.Update(ctx, record, version)
		if err != nil {
			return nil, err
		}
		version = next
	}
	return nil, errors.New("steal interaction Saga did not converge")
}

// deferRetry persists retry_at_ms/error_code without changing status: per
// §6 of the design doc, "Tcaplus 临时错误只推进重试时间，不能猜测成功或失败"
// (a transient/transport error only advances the retry clock; it must
// never guess success or failure). The caller (or the Reconciler once
// retry_at_ms elapses) resumes from the same status and safely converges
// because every step this can interrupt is itself idempotent on the owner
// or visitor side.
func (s *StealSaga) deferRetry(
	ctx context.Context,
	record *tcaplusv1.FriendInteraction,
	version int32,
	cause error,
	now time.Time,
) (*wsv1.FriendActionResponse, error) {
	record.RetryAtMs = now.Add(defaultRetryBackoff).UnixMilli()
	record.ErrorCode = transportErrorCode
	record.UpdatedAtMs = now.UnixMilli()
	if _, err := s.store.Update(ctx, record, version); err != nil {
		return nil, fmt.Errorf("persist retry state after %v: %w", cause, err)
	}
	return nil, fmt.Errorf("%w: %v", ErrOutcomeUnknown, cause)
}

func parseErrorCode(value string) wsv1.ErrorCode {
	if code, ok := wsv1.ErrorCode_value[value]; ok {
		return wsv1.ErrorCode(code)
	}
	return wsv1.ErrorCode_ERROR_UNSPECIFIED
}
