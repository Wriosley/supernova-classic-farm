package visit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
)

type visitorCurrentVisit struct {
	ownerPlayerID uint64
	visitID       []byte
}

// Service is the visitor Zone's friend-farm-visit orchestration: it rejects
// self-visits, checks mutual friendship with FriendSvr, then forwards
// Enter/Heartbeat/Exit to whichever Zone owns the target farm. It also
// tracks each visitor's single current visit in memory so entering a
// different owner auto-exits the previous one, enforcing "one farm at a
// time" the same way H5 does client-side.
//
// Every method returns (result, domainError, transportError): a non-nil
// domainError belongs in the caller's WsEnvelope.Error (gRPC status OK,
// mirroring FriendSvr's Phase 2 pattern), while transportError means the
// call itself failed and should surface as an internal RPC failure.
type Service struct {
	friend FriendChecker
	owner  OwnerFarmClient
	now    func() time.Time

	mu      sync.Mutex
	current map[uint64]visitorCurrentVisit
}

func NewService(friendChecker FriendChecker, ownerClient OwnerFarmClient, now func() time.Time) (*Service, error) {
	if friendChecker == nil || ownerClient == nil {
		return nil, errors.New("friend checker and owner farm client are required")
	}
	if now == nil {
		now = time.Now
	}
	return &Service{
		friend: friendChecker, owner: ownerClient, now: now,
		current: make(map[uint64]visitorCurrentVisit),
	}, nil
}

func (s *Service) EnterFriendFarm(
	ctx context.Context,
	visitorPlayerID, ownerPlayerID uint64,
	gateID, requestID string,
) (*wsv1.EnterFriendFarmResponse, *wsv1.Error, error) {
	if visitorPlayerID == 0 || ownerPlayerID == 0 {
		return nil, &wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}, nil
	}
	if visitorPlayerID == ownerPlayerID {
		return nil, &wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}, nil
	}
	mutual, relationID, err := s.friend.CheckMutualFriend(ctx, visitorPlayerID, ownerPlayerID)
	if err != nil {
		return nil, nil, fmt.Errorf("check mutual friend: %w", err)
	}
	if !mutual {
		return nil, &wsv1.Error{Code: wsv1.ErrorCode_NOT_MUTUAL_FRIEND}, nil
	}

	s.mu.Lock()
	previous, hasPrevious := s.current[visitorPlayerID]
	s.mu.Unlock()
	if hasPrevious && previous.ownerPlayerID != ownerPlayerID {
		// Leaving the old farm is best-effort cleanup: it must never block
		// entering the new one, and "already gone" is not an error here.
		_, _ = s.owner.ExitVisitor(ctx, previous.ownerPlayerID, visitorPlayerID, previous.visitID)
	}

	visitID, expiresAtMs, snapshot, wsErr, err := s.owner.EnterVisitor(
		ctx, ownerPlayerID, visitorPlayerID, gateID, relationID, requestID,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("enter Owner farm: %w", err)
	}
	if wsErr != nil {
		return nil, wsErr, nil
	}
	s.mu.Lock()
	s.current[visitorPlayerID] = visitorCurrentVisit{
		ownerPlayerID: ownerPlayerID, visitID: append([]byte(nil), visitID...),
	}
	s.mu.Unlock()
	return &wsv1.EnterFriendFarmResponse{
		VisitId: visitID, ExpiresAtMs: expiresAtMs, Snapshot: snapshot,
	}, nil, nil
}

func (s *Service) HeartbeatFriendFarm(
	ctx context.Context,
	visitorPlayerID, ownerPlayerID uint64,
	visitID []byte,
	gateID string,
) (*wsv1.FarmHeartbeatResponse, *wsv1.Error, error) {
	if visitorPlayerID == 0 || ownerPlayerID == 0 || len(visitID) != 16 {
		return nil, &wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}, nil
	}
	expiresAtMs, wsErr, err := s.owner.RefreshVisitorHeartbeat(
		ctx, ownerPlayerID, visitorPlayerID, visitID, gateID,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("refresh visitor heartbeat: %w", err)
	}
	if wsErr != nil {
		s.clearIfMatches(visitorPlayerID, ownerPlayerID, visitID)
		return nil, wsErr, nil
	}
	return &wsv1.FarmHeartbeatResponse{ExpiresAtMs: expiresAtMs}, nil, nil
}

func (s *Service) ExitFriendFarm(
	ctx context.Context,
	visitorPlayerID, ownerPlayerID uint64,
	visitID []byte,
) (*wsv1.Error, error) {
	if visitorPlayerID == 0 || ownerPlayerID == 0 || len(visitID) != 16 {
		return &wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}, nil
	}
	wsErr, err := s.owner.ExitVisitor(ctx, ownerPlayerID, visitorPlayerID, visitID)
	if err != nil {
		return nil, fmt.Errorf("exit Owner farm: %w", err)
	}
	s.clearIfMatches(visitorPlayerID, ownerPlayerID, visitID)
	return wsErr, nil
}

func (s *Service) clearIfMatches(visitorPlayerID, ownerPlayerID uint64, visitID []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.current[visitorPlayerID]
	if ok && current.ownerPlayerID == ownerPlayerID && bytes.Equal(current.visitID, visitID) {
		delete(s.current, visitorPlayerID)
	}
}
