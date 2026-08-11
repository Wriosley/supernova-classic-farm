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

type ActionRequest struct {
	InteractionID     []byte
	VisitorPlayerID   uint64
	VisitorOwnerEpoch uint64
	OwnerPlayerID     uint64
	OwnerRoute        *rpcv1.CommittedRoute
	VisitID           []byte
	Action            datav1.FriendInteractionAction
	PlotID            uint32
	PestID            uint32
}

func (r ActionRequest) validate() error {
	if len(r.InteractionID) != 16 || r.VisitorPlayerID == 0 || r.VisitorOwnerEpoch == 0 ||
		r.OwnerPlayerID == 0 || len(r.VisitID) != 16 || r.PlotID == 0 {
		return errors.New("friend action request is incomplete")
	}
	switch r.Action {
	case datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND:
		if r.PestID == 0 {
			return errors.New("apply pest request requires pest ID")
		}
	case datav1.FriendInteractionAction_CATCH_PEST_FOR_FRIEND,
		datav1.FriendInteractionAction_HELP_CLEAN_FRIEND_PLOT:
		if r.PestID != 0 {
			return errors.New("friend action does not accept pest ID")
		}
	default:
		return errors.New("unsupported friend action")
	}
	return nil
}

type ActionSaga struct {
	store   Store
	visitor VisitorSteps
	owner   OwnerFarmClient
}

func NewActionSaga(store Store, visitor VisitorSteps, owner OwnerFarmClient) (*ActionSaga, error) {
	if store == nil || visitor == nil || owner == nil {
		return nil, errors.New("interaction store, visitor steps and owner client are required")
	}
	return &ActionSaga{store: store, visitor: visitor, owner: owner}, nil
}

func (s *ActionSaga) Execute(ctx context.Context, req ActionRequest, now time.Time) (*wsv1.FriendActionResponse, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	digest := RequestDigest(req.Action, req.VisitorPlayerID, req.OwnerPlayerID, req.VisitID, req.PlotID, req.PestID, 0, nil, 0)
	record, version, err := s.createOrLoad(ctx, req, digest, now)
	if err != nil {
		return nil, err
	}
	return s.advance(ctx, record, version, req, now)
}

func (s *ActionSaga) Resume(ctx context.Context, req ActionRequest, now time.Time) (*wsv1.FriendActionResponse, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	record, version, err := s.store.Get(ctx, req.InteractionID)
	if err != nil {
		return nil, err
	}
	return s.advance(ctx, record, version, req, now)
}

func (s *ActionSaga) createOrLoad(
	ctx context.Context, req ActionRequest, digest []byte, now time.Time,
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
		InteractionId:   append([]byte(nil), req.InteractionID...),
		VisitorPlayerId: req.VisitorPlayerID, OwnerPlayerId: req.OwnerPlayerID,
		VisitId: append([]byte(nil), req.VisitID...), Action: uint32(req.Action),
		PlotId: req.PlotID, PestId: req.PestID, RequestDigestSha256: digest,
		Status:      tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_INIT,
		CreatedAtMs: now.UnixMilli(), UpdatedAtMs: now.UnixMilli(),
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

func (s *ActionSaga) advance(
	ctx context.Context, record *tcaplusv1.FriendInteraction, version int32,
	req ActionRequest, now time.Time,
) (*wsv1.FriendActionResponse, error) {
	for step := 0; step < maxSagaSteps; step++ {
		switch record.Status {
		case tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_INIT:
			if _, err := s.visitor.ReserveActionChance(
				ctx, req.VisitorPlayerID, req.VisitorOwnerEpoch, record.InteractionId, req.Action,
			); err != nil {
				if code, ok := visitorDomainErrorCode(err); ok {
					record.Status = tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_RELEASING
					record.ErrorCode = code.String()
					break
				}
				return s.deferRetry(ctx, record, version, err, now)
			}
			record.Status = tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_VISITOR_RESERVED

		case tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_VISITOR_RESERVED:
			ownerRequest := &rpcv1.ApplyVisitorActionRequest{
				OwnerRoute: req.OwnerRoute, OwnerPlayerId: req.OwnerPlayerID,
				VisitorPlayerId: req.VisitorPlayerID, VisitId: req.VisitID,
				InteractionId: record.InteractionId, Action: req.Action, PlotId: req.PlotID,
				RequestDigestSha256: record.RequestDigestSha256,
			}
			if req.Action == datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND {
				ownerRequest.PestId = uint32Pointer(req.PestID)
			}
			response, err := s.owner.ApplyVisitorAction(ctx, ownerRequest)
			if err != nil {
				return s.deferRetry(ctx, record, version, err, now)
			}
			if wsErr := response.GetError(); wsErr != nil {
				record.Status = tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_RELEASING
				record.ErrorCode = wsErr.Code.String()
				break
			}
			intermediate := &wsv1.FriendActionResponse{
				InteractionId: record.InteractionId, FarmPatch: response.GetFarmPatch(),
			}
			body, err := proto.MarshalOptions{Deterministic: true}.Marshal(intermediate)
			if err != nil {
				return nil, fmt.Errorf("marshal owner-applied friend action result: %w", err)
			}
			record.ResultPayload = body
			record.ResultDigestSha256 = append([]byte(nil), response.GetResultDigestSha256()...)
			record.Status = tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_OWNER_APPLIED

		case tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_OWNER_APPLIED:
			response, _, err := s.visitor.CommitActionChance(
				ctx, req.VisitorPlayerID, req.VisitorOwnerEpoch, record.InteractionId,
				req.Action, record.ResultPayload,
			)
			if err != nil {
				return s.deferRetry(ctx, record, version, err, now)
			}
			body, err := proto.MarshalOptions{Deterministic: true}.Marshal(response)
			if err != nil {
				return nil, fmt.Errorf("marshal final friend action response: %w", err)
			}
			digest := sha256.Sum256(body)
			record.ResultPayload, record.ResultDigestSha256 = body, digest[:]
			record.Status = tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_VISITOR_COMMITTED

		case tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_VISITOR_COMMITTED:
			record.Status = tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_COMPLETED

		case tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_COMPLETED:
			response := &wsv1.FriendActionResponse{}
			if err := proto.Unmarshal(record.ResultPayload, response); err != nil {
				return nil, fmt.Errorf("decode completed friend action result: %w", err)
			}
			return response, nil

		case tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_RELEASING:
			_ = s.visitor.ReleaseActionChance(
				ctx, req.VisitorPlayerID, req.VisitorOwnerEpoch, record.InteractionId, req.Action,
			)
			record.Status = tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_ABORTED

		case tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_ABORTED:
			return nil, &AbortedError{Code: parseErrorCode(record.ErrorCode)}
		default:
			return nil, fmt.Errorf("friend action Saga has unknown status %d", record.Status)
		}
		record.RetryAtMs = 0
		record.UpdatedAtMs = now.UnixMilli()
		next, err := s.store.Update(ctx, record, version)
		if err != nil {
			return nil, err
		}
		version = next
	}
	return nil, errors.New("friend action Saga did not converge")
}

func (s *ActionSaga) deferRetry(
	ctx context.Context, record *tcaplusv1.FriendInteraction, version int32,
	cause error, now time.Time,
) (*wsv1.FriendActionResponse, error) {
	record.RetryAtMs = now.Add(defaultRetryBackoff).UnixMilli()
	record.ErrorCode = transportErrorCode
	record.UpdatedAtMs = now.UnixMilli()
	if _, err := s.store.Update(ctx, record, version); err != nil {
		return nil, fmt.Errorf("persist retry state after %v: %w", cause, err)
	}
	return nil, fmt.Errorf("%w: %v", ErrOutcomeUnknown, cause)
}

func uint32Pointer(value uint32) *uint32 { return &value }
