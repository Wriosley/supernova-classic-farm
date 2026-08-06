package visit

import (
	"bytes"
	"context"
	"errors"
	"testing"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
)

type fakeFriendChecker struct {
	mutual     bool
	relationID []byte
	err        error
	calls      int
}

func (f *fakeFriendChecker) CheckMutualFriend(_ context.Context, _, _ uint64) (bool, []byte, error) {
	f.calls++
	return f.mutual, f.relationID, f.err
}

type enterCall struct {
	ownerPlayerID, visitorPlayerID uint64
	gateID, requestID              string
	relationID                     []byte
}

type exitCall struct {
	ownerPlayerID, visitorPlayerID uint64
	visitID                        []byte
}

type fakeOwnerFarmClient struct {
	enterCalls []enterCall
	exitCalls  []exitCall

	nextVisitID     []byte
	nextExpiresAtMs int64
	nextSnapshot    *wsv1.FarmVisitSnapshot
	enterErr        *wsv1.Error
	enterTransport  error

	heartbeatExpiresAtMs int64
	heartbeatErr         *wsv1.Error
	heartbeatTransport   error

	exitErr       *wsv1.Error
	exitTransport error
}

func (f *fakeOwnerFarmClient) EnterVisitor(
	_ context.Context, ownerPlayerID, visitorPlayerID uint64, gateID string, relationID []byte, requestID string,
) ([]byte, int64, *wsv1.FarmVisitSnapshot, *wsv1.Error, error) {
	f.enterCalls = append(f.enterCalls, enterCall{
		ownerPlayerID: ownerPlayerID, visitorPlayerID: visitorPlayerID,
		gateID: gateID, requestID: requestID, relationID: relationID,
	})
	if f.enterTransport != nil {
		return nil, 0, nil, nil, f.enterTransport
	}
	if f.enterErr != nil {
		return nil, 0, nil, f.enterErr, nil
	}
	return f.nextVisitID, f.nextExpiresAtMs, f.nextSnapshot, nil, nil
}

func (f *fakeOwnerFarmClient) RefreshVisitorHeartbeat(
	_ context.Context, _, _ uint64, _ []byte, _ string,
) (int64, *wsv1.Error, error) {
	if f.heartbeatTransport != nil {
		return 0, nil, f.heartbeatTransport
	}
	if f.heartbeatErr != nil {
		return 0, f.heartbeatErr, nil
	}
	return f.heartbeatExpiresAtMs, nil, nil
}

func (f *fakeOwnerFarmClient) ExitVisitor(
	_ context.Context, ownerPlayerID, visitorPlayerID uint64, visitID []byte,
) (*wsv1.Error, error) {
	f.exitCalls = append(f.exitCalls, exitCall{
		ownerPlayerID: ownerPlayerID, visitorPlayerID: visitorPlayerID, visitID: visitID,
	})
	if f.exitTransport != nil {
		return nil, f.exitTransport
	}
	return f.exitErr, nil
}

func (f *fakeOwnerFarmClient) ApplyVisitorAction(
	context.Context, *rpcv1.ApplyVisitorActionRequest,
) (*rpcv1.ApplyVisitorActionResponse, error) {
	return &rpcv1.ApplyVisitorActionResponse{}, nil
}

func newTestService(t *testing.T, friend *fakeFriendChecker, owner *fakeOwnerFarmClient) *Service {
	t.Helper()
	svc, err := NewService(friend, owner, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func TestEnterFriendFarmRejectsSelfVisit(t *testing.T) {
	friend := &fakeFriendChecker{mutual: true}
	owner := &fakeOwnerFarmClient{}
	svc := newTestService(t, friend, owner)

	result, wsErr, err := svc.EnterFriendFarm(context.Background(), 1, 1, "gate-a", "req-1")
	if err != nil {
		t.Fatalf("EnterFriendFarm: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result for self-visit, got %+v", result)
	}
	if wsErr.GetCode() != wsv1.ErrorCode_INVALID_ARGUMENT {
		t.Fatalf("error code = %v, want INVALID_ARGUMENT", wsErr.GetCode())
	}
	if friend.calls != 0 {
		t.Fatalf("expected self-visit to short-circuit before checking friendship, got %d calls", friend.calls)
	}
}

func TestEnterFriendFarmRejectsNonMutualFriend(t *testing.T) {
	friend := &fakeFriendChecker{mutual: false}
	owner := &fakeOwnerFarmClient{}
	svc := newTestService(t, friend, owner)

	result, wsErr, err := svc.EnterFriendFarm(context.Background(), 1, 2, "gate-a", "req-1")
	if err != nil {
		t.Fatalf("EnterFriendFarm: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result for non-mutual friend, got %+v", result)
	}
	if wsErr.GetCode() != wsv1.ErrorCode_NOT_MUTUAL_FRIEND {
		t.Fatalf("error code = %v, want NOT_MUTUAL_FRIEND", wsErr.GetCode())
	}
	if len(owner.enterCalls) != 0 {
		t.Fatalf("expected Owner Zone to never be called for a non-mutual friend")
	}
}

func TestEnterFriendFarmSucceedsAndTracksCurrentVisit(t *testing.T) {
	relationID := bytes.Repeat([]byte{0xAB}, 16)
	visitID := bytes.Repeat([]byte{0x01}, 16)
	friend := &fakeFriendChecker{mutual: true, relationID: relationID}
	owner := &fakeOwnerFarmClient{
		nextVisitID: visitID, nextExpiresAtMs: 1000,
		nextSnapshot: &wsv1.FarmVisitSnapshot{OwnerPlayerId: 2},
	}
	svc := newTestService(t, friend, owner)

	result, wsErr, err := svc.EnterFriendFarm(context.Background(), 1, 2, "gate-a", "req-1")
	if err != nil || wsErr != nil {
		t.Fatalf("EnterFriendFarm: result=%+v wsErr=%+v err=%v", result, wsErr, err)
	}
	if !bytes.Equal(result.GetVisitId(), visitID) || result.GetExpiresAtMs() != 1000 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(owner.enterCalls) != 1 || !bytes.Equal(owner.enterCalls[0].relationID, relationID) {
		t.Fatalf("Owner Zone was not called with the relation ID: %+v", owner.enterCalls)
	}
}

func TestEnterFriendFarmSwitchingOwnersAutoExitsPreviousFarm(t *testing.T) {
	friend := &fakeFriendChecker{mutual: true}
	owner := &fakeOwnerFarmClient{nextVisitID: bytes.Repeat([]byte{0x01}, 16)}
	svc := newTestService(t, friend, owner)

	if _, wsErr, err := svc.EnterFriendFarm(context.Background(), 1, 2, "gate-a", "req-1"); err != nil || wsErr != nil {
		t.Fatalf("first EnterFriendFarm: wsErr=%+v err=%v", wsErr, err)
	}
	owner.nextVisitID = bytes.Repeat([]byte{0x02}, 16)
	if _, wsErr, err := svc.EnterFriendFarm(context.Background(), 1, 3, "gate-a", "req-2"); err != nil || wsErr != nil {
		t.Fatalf("second EnterFriendFarm: wsErr=%+v err=%v", wsErr, err)
	}
	if len(owner.exitCalls) != 1 {
		t.Fatalf("expected exactly one auto-exit of the previous farm, got %+v", owner.exitCalls)
	}
	if owner.exitCalls[0].ownerPlayerID != 2 || owner.exitCalls[0].visitorPlayerID != 1 {
		t.Fatalf("auto-exit targeted the wrong farm: %+v", owner.exitCalls[0])
	}
}

func TestEnterFriendFarmReenteringSameOwnerDoesNotAutoExit(t *testing.T) {
	friend := &fakeFriendChecker{mutual: true}
	owner := &fakeOwnerFarmClient{nextVisitID: bytes.Repeat([]byte{0x01}, 16)}
	svc := newTestService(t, friend, owner)

	if _, _, err := svc.EnterFriendFarm(context.Background(), 1, 2, "gate-a", "req-1"); err != nil {
		t.Fatalf("first EnterFriendFarm: %v", err)
	}
	if _, _, err := svc.EnterFriendFarm(context.Background(), 1, 2, "gate-a", "req-1"); err != nil {
		t.Fatalf("second EnterFriendFarm: %v", err)
	}
	if len(owner.exitCalls) != 0 {
		t.Fatalf("expected no auto-exit when re-entering the same farm, got %+v", owner.exitCalls)
	}
}

func TestHeartbeatFriendFarmForwardsResultAndClearsOnError(t *testing.T) {
	friend := &fakeFriendChecker{mutual: true}
	visitID := bytes.Repeat([]byte{0x01}, 16)
	owner := &fakeOwnerFarmClient{nextVisitID: visitID, heartbeatExpiresAtMs: 5000}
	svc := newTestService(t, friend, owner)
	if _, _, err := svc.EnterFriendFarm(context.Background(), 1, 2, "gate-a", "req-1"); err != nil {
		t.Fatalf("EnterFriendFarm: %v", err)
	}

	result, wsErr, err := svc.HeartbeatFriendFarm(context.Background(), 1, 2, visitID, "gate-a")
	if err != nil || wsErr != nil {
		t.Fatalf("HeartbeatFriendFarm: result=%+v wsErr=%+v err=%v", result, wsErr, err)
	}
	if result.GetExpiresAtMs() != 5000 {
		t.Fatalf("expires_at_ms = %d, want 5000", result.GetExpiresAtMs())
	}

	owner.heartbeatErr = &wsv1.Error{Code: wsv1.ErrorCode_VISIT_EXPIRED}
	result, wsErr, err = svc.HeartbeatFriendFarm(context.Background(), 1, 2, visitID, "gate-a")
	if err != nil {
		t.Fatalf("HeartbeatFriendFarm after expiry: %v", err)
	}
	if result != nil || wsErr.GetCode() != wsv1.ErrorCode_VISIT_EXPIRED {
		t.Fatalf("expected VISIT_EXPIRED, got result=%+v wsErr=%+v", result, wsErr)
	}

	// A subsequent Enter switching owners should not think it still needs to
	// auto-exit the now-expired visit (no unnecessary ExitVisitor call).
	owner.exitCalls = nil
	owner.nextVisitID = bytes.Repeat([]byte{0x02}, 16)
	if _, _, err := svc.EnterFriendFarm(context.Background(), 1, 3, "gate-a", "req-2"); err != nil {
		t.Fatalf("EnterFriendFarm after expiry: %v", err)
	}
	if len(owner.exitCalls) != 0 {
		t.Fatalf("expected no auto-exit for an already-cleared visit, got %+v", owner.exitCalls)
	}
}

func TestExitFriendFarmClearsCurrentVisit(t *testing.T) {
	friend := &fakeFriendChecker{mutual: true}
	visitID := bytes.Repeat([]byte{0x01}, 16)
	owner := &fakeOwnerFarmClient{nextVisitID: visitID}
	svc := newTestService(t, friend, owner)
	if _, _, err := svc.EnterFriendFarm(context.Background(), 1, 2, "gate-a", "req-1"); err != nil {
		t.Fatalf("EnterFriendFarm: %v", err)
	}

	wsErr, err := svc.ExitFriendFarm(context.Background(), 1, 2, visitID)
	if err != nil || wsErr != nil {
		t.Fatalf("ExitFriendFarm: wsErr=%+v err=%v", wsErr, err)
	}
	if len(owner.exitCalls) != 1 {
		t.Fatalf("expected ExitVisitor to be called once, got %+v", owner.exitCalls)
	}

	// A later switch to a new owner must not try to exit the already-exited visit.
	owner.exitCalls = nil
	owner.nextVisitID = bytes.Repeat([]byte{0x02}, 16)
	if _, _, err := svc.EnterFriendFarm(context.Background(), 1, 3, "gate-a", "req-2"); err != nil {
		t.Fatalf("EnterFriendFarm after exit: %v", err)
	}
	if len(owner.exitCalls) != 0 {
		t.Fatalf("expected no auto-exit after an explicit exit, got %+v", owner.exitCalls)
	}
}

func TestEnterFriendFarmPropagatesTransportErrors(t *testing.T) {
	friend := &fakeFriendChecker{err: errors.New("friend rpc unavailable")}
	owner := &fakeOwnerFarmClient{}
	svc := newTestService(t, friend, owner)

	if _, _, err := svc.EnterFriendFarm(context.Background(), 1, 2, "gate-a", "req-1"); err == nil {
		t.Fatal("expected a transport error when FriendSvr is unavailable")
	}

	friend = &fakeFriendChecker{mutual: true}
	owner = &fakeOwnerFarmClient{enterTransport: errors.New("owner zone unavailable")}
	svc = newTestService(t, friend, owner)
	if _, _, err := svc.EnterFriendFarm(context.Background(), 1, 2, "gate-a", "req-1"); err == nil {
		t.Fatal("expected a transport error when the Owner Zone is unavailable")
	}
}

func TestEnterExitHeartbeatRejectInvalidArguments(t *testing.T) {
	friend := &fakeFriendChecker{mutual: true}
	owner := &fakeOwnerFarmClient{}
	svc := newTestService(t, friend, owner)

	if _, wsErr, err := svc.EnterFriendFarm(context.Background(), 0, 2, "gate-a", "req-1"); err != nil || wsErr.GetCode() != wsv1.ErrorCode_INVALID_ARGUMENT {
		t.Fatalf("EnterFriendFarm(visitor=0) = wsErr=%+v err=%v", wsErr, err)
	}
	if _, wsErr, err := svc.HeartbeatFriendFarm(context.Background(), 1, 2, []byte{1, 2, 3}, "gate-a"); err != nil || wsErr.GetCode() != wsv1.ErrorCode_INVALID_ARGUMENT {
		t.Fatalf("HeartbeatFriendFarm(bad visit_id) = wsErr=%+v err=%v", wsErr, err)
	}
	if wsErr, err := svc.ExitFriendFarm(context.Background(), 1, 2, []byte{1, 2, 3}); err != nil || wsErr.GetCode() != wsv1.ErrorCode_INVALID_ARGUMENT {
		t.Fatalf("ExitFriendFarm(bad visit_id) = wsErr=%+v err=%v", wsErr, err)
	}
}
