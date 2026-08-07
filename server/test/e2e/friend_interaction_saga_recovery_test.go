package e2e

import (
	"os"
	"testing"
	"time"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/coder/websocket"
)

// TestFriendRestartRecovery verifies acceptance checklist §6 restart item:
// after a full FriendSvr/Gate/Zone stack stop+start, previously linked
// accounts can log in, list each other, and re-enter the owner's farm.
//
// The companion shell script runs TestFriendInteraction first (register +
// mutate), stops the stack, restarts it, then runs this test with
// E2E_AUTH_MODE=login against the same account names.
func TestFriendRestartRecovery(t *testing.T) {
	if os.Getenv("E2E_RUN") != "1" || os.Getenv("E2E_SUITE") != "friend-saga-recovery" {
		t.Skip("set E2E_RUN=1 E2E_SUITE=friend-saga-recovery, or use tests/e2e/run-friend-restart-recovery.sh")
	}
	ownerName := os.Getenv("E2E_OWNER_ACCOUNT")
	visitorAName := os.Getenv("E2E_VISITOR_A_ACCOUNT")
	visitorBName := os.Getenv("E2E_VISITOR_B_ACCOUNT")
	if ownerName == "" || visitorAName == "" || visitorBName == "" {
		t.Fatal("E2E_OWNER_ACCOUNT / E2E_VISITOR_A_ACCOUNT / E2E_VISITOR_B_ACCOUNT are required")
	}

	owner := loginFriendPlayer(t, ownerName)
	visitorA := loginFriendPlayer(t, visitorAName)
	visitorB := loginFriendPlayer(t, visitorBName)
	owner.conn = authenticateFriendPlayer(t, owner)
	visitorA.conn = authenticateFriendPlayer(t, visitorA)
	visitorB.conn = authenticateFriendPlayer(t, visitorB)
	t.Cleanup(func() {
		_ = owner.conn.Close(websocket.StatusNormalClosure, "done")
		_ = visitorA.conn.Close(websocket.StatusNormalClosure, "done")
		_ = visitorB.conn.Close(websocket.StatusNormalClosure, "done")
	})
	loadOwnSnapshot(t, owner)
	loadOwnSnapshot(t, visitorA)
	loadOwnSnapshot(t, visitorB)

	assertFriendListed(t, owner, visitorA.id)
	assertFriendListed(t, owner, visitorB.id)
	assertFriendListed(t, visitorA, owner.id)
	assertFriendListed(t, visitorB, owner.id)

	// Snapshot after restart must load (checkpoint + actor activation).
	requestID := newUUID(t)
	writeEnvelope(t, owner.conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_GET_PLAYER_SNAPSHOT, RequestId: requestID,
		TargetPlayerId: owner.id,
		Payload: &wsv1.WsEnvelope_GetPlayerSnapshotRequest{
			GetPlayerSnapshotRequest: &wsv1.GetPlayerSnapshotRequest{},
		},
	})
	snapshot := readMatchingResponse(t, owner.conn, requestID, friendInteractionTimeout)
	if snapshot.GetError() != nil ||
		snapshot.GetGetPlayerSnapshotResponse().GetSnapshot() == nil {
		t.Fatalf("owner snapshot after restart failed: %+v", snapshot)
	}

	enter := enterFriendFarm(t, visitorA, owner.id)
	if enter.GetSnapshot() == nil || len(enter.GetVisitId()) != 16 {
		t.Fatalf("re-enter after restart returned empty visit: %+v", enter)
	}
	presence := readMatchingPush(t, owner.conn, wsv1.Action_FARM_PRESENCE_CHANGED, 5*time.Second)
	if presence.GetFarmPresenceChangedPush().GetKind() != wsv1.FarmPresenceKind_FARM_VISITOR_ENTERED {
		t.Fatalf("presence after restart re-enter: %+v", presence.GetFarmPresenceChangedPush())
	}
	exitFriendFarm(t, visitorA, owner.id, enter.GetVisitId())

	t.Log("friend restart recovery E2E passed")
}
