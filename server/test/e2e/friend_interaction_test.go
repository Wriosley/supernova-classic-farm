package e2e

import (
	"bytes"
	"context"
	"net/http"
	"net/http/cookiejar"
	"os"
	"testing"
	"time"

	httpv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/http"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"
)

const (
	friendSeedShopEntryID   uint32 = 5001
	friendSeedPriceVersion  uint64 = 8
	friendSeedItemID        uint32 = 1001
	friendFertilizerItemID  uint32 = 1
	friendPestID            uint32 = 1
	friendPlotID            uint32 = 1
	friendInteractionTimeout       = 30 * time.Second
	friendMaturityTimeout          = 120 * time.Second
)

type friendPlayer struct {
	id       uint64
	account  string
	client   *http.Client
	csrf     string
	wsTicket string
	conn     *websocket.Conn
}

// TestFriendGiftRedDotLatency measures the two user-visible boundaries of the
// durable gift path: sender acknowledgement and recipient red-dot push.
func TestFriendGiftRedDotLatency(t *testing.T) {
	if os.Getenv("E2E_RUN") != "1" || os.Getenv("E2E_SUITE") != "friend-gift-latency" {
		t.Skip("set E2E_RUN=1 E2E_SUITE=friend-gift-latency")
	}
	sender := loginFriendPlayer(t, os.Getenv("E2E_GIFT_SENDER_ACCOUNT"))
	recipient := loginFriendPlayer(t, os.Getenv("E2E_GIFT_RECIPIENT_ACCOUNT"))
	sender.conn = authenticateFriendPlayer(t, sender)
	recipient.conn = authenticateFriendPlayer(t, recipient)
	t.Cleanup(func() {
		_ = sender.conn.Close(websocket.StatusNormalClosure, "done")
		_ = recipient.conn.Close(websocket.StatusNormalClosure, "done")
	})
	loadOwnSnapshot(t, sender)
	loadOwnSnapshot(t, recipient)

	requestID := newUUID(t)
	started := time.Now()
	writeEnvelope(t, sender.conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_SEND_FRIEND_GIFT, RequestId: requestID, TargetPlayerId: sender.id,
		Payload: &wsv1.WsEnvelope_SendFriendGiftRequest{SendFriendGiftRequest: &wsv1.SendFriendGiftRequest{
			RecipientPlayerId: recipient.id, CropItemId: 1002, Quantity: 1,
		}},
	})
	response := readMatchingResponse(t, sender.conn, requestID, friendInteractionTimeout)
	if response.GetError() != nil {
		t.Fatalf("SEND_FRIEND_GIFT failed: %+v", response)
	}
	ackAt := time.Now()
	push := readMatchingPush(t, recipient.conn, wsv1.Action_RED_DOT_CHANGED, 10*time.Second)
	if push.GetRedDotChangedPush().GetCategory() != wsv1.RedDotCategory_RED_DOT_CATEGORY_MAIL {
		t.Fatalf("unexpected red dot: %+v", push)
	}
	t.Logf("SEND_FRIEND_GIFT response latency=%s red-dot latency=%s post-response=%s",
		ackAt.Sub(started), time.Since(started), time.Since(ackAt))
}

// TestFriendInteraction covers acceptance checklist §6 through a three-client
// WebSocket session against a live dual-Zone + FriendSvr stack:
// friend link, dual visit + presence, pest/catch/steal/help-clean, view sync,
// and patch cutoff after exit.
func TestFriendInteraction(t *testing.T) {
	if os.Getenv("E2E_RUN") != "1" || os.Getenv("E2E_SUITE") != "friend-interaction" {
		t.Skip("set E2E_RUN=1 E2E_SUITE=friend-interaction, or use tests/e2e/run-friend-interaction.sh")
	}

	ownerName := envOr("E2E_OWNER_ACCOUNT", uniqueAccountName(t))
	visitorAName := envOr("E2E_VISITOR_A_ACCOUNT", uniqueAccountName(t))
	visitorBName := envOr("E2E_VISITOR_B_ACCOUNT", uniqueAccountName(t))
	t.Logf("accounts owner=%s visitor_a=%s visitor_b=%s", ownerName, visitorAName, visitorBName)

	owner := registerFriendPlayer(t, ownerName)
	visitorA := registerFriendPlayer(t, visitorAName)
	visitorB := registerFriendPlayer(t, visitorBName)
	owner.conn = authenticateFriendPlayer(t, owner)
	visitorA.conn = authenticateFriendPlayer(t, visitorA)
	visitorB.conn = authenticateFriendPlayer(t, visitorB)
	t.Cleanup(func() {
		_ = owner.conn.Close(websocket.StatusNormalClosure, "done")
		_ = visitorA.conn.Close(websocket.StatusNormalClosure, "done")
		_ = visitorB.conn.Close(websocket.StatusNormalClosure, "done")
	})
	// Gate buffers pushes until GET_PLAYER_SNAPSHOT finishes (subscription.ready).
	loadOwnSnapshot(t, owner)
	loadOwnSnapshot(t, visitorA)
	loadOwnSnapshot(t, visitorB)

	code := createFriendCode(t, owner)
	redeemFriendCode(t, visitorA, code)
	redeemFriendCode(t, visitorB, code)
	assertFriendListed(t, owner, visitorA.id)
	assertFriendListed(t, owner, visitorB.id)

	buySeeds(t, owner, 1)
	plantSeed(t, owner, friendPlotID, friendSeedItemID)
	applyFertilizer(t, owner, friendPlotID)

	visitA := enterFriendFarm(t, visitorA, owner.id)
	presence := readMatchingPush(t, owner.conn, wsv1.Action_FARM_PRESENCE_CHANGED, 5*time.Second)
	if presence.GetFarmPresenceChangedPush().GetKind() != wsv1.FarmPresenceKind_FARM_VISITOR_ENTERED {
		t.Fatalf("owner expected ENTERED presence, got %+v", presence.GetFarmPresenceChangedPush())
	}
	visitB := enterFriendFarm(t, visitorB, owner.id)
	presence = readMatchingPush(t, owner.conn, wsv1.Action_FARM_PRESENCE_CHANGED, 5*time.Second)
	if presence.GetFarmPresenceChangedPush().GetKind() != wsv1.FarmPresenceKind_FARM_VISITOR_ENTERED {
		t.Fatalf("owner expected second ENTERED presence, got %+v", presence.GetFarmPresenceChangedPush())
	}
	if !bytes.Equal(
		publicPlotFingerprint(visitA.GetSnapshot()),
		publicPlotFingerprint(visitB.GetSnapshot()),
	) {
		t.Fatalf("two visitors entered with divergent public snapshots")
	}

	pestResponse := applyPestToFriend(t, visitorA, owner.id, visitA.GetVisitId(), friendPlotID)
	if !pestResponse.GetFarmPatch().GetPlotUpserts()[0].GetPestActive() {
		t.Fatalf("apply pest response missing pest_active: %+v", pestResponse)
	}
	ownerPatch := readMatchingPush(t, owner.conn, wsv1.Action_FARM_VIEW_CHANGED, 5*time.Second)
	visitorBPatch := readMatchingPush(t, visitorB.conn, wsv1.Action_FARM_VIEW_CHANGED, 5*time.Second)
	assertPublicPest(t, ownerPatch.GetFarmViewChangedPush(), true)
	assertPublicPest(t, visitorBPatch.GetFarmViewChangedPush(), true)
	assertPublicPest(t, pestResponse.GetFarmPatch(), true)

	catchResponse := catchPestForFriend(t, visitorB, owner.id, visitB.GetVisitId(), friendPlotID)
	if catchResponse.GetFarmPatch().GetPlotUpserts()[0].GetPestActive() {
		t.Fatalf("catch pest left pest_active set: %+v", catchResponse)
	}
	_ = readMatchingPush(t, owner.conn, wsv1.Action_FARM_VIEW_CHANGED, 5*time.Second)
	_ = readMatchingPush(t, visitorA.conn, wsv1.Action_FARM_VIEW_CHANGED, 5*time.Second)

	waitPlotMature(t, owner, friendPlotID, visitA, visitB, visitorA, visitorB, owner.id)
	visitSnap := visitA.GetSnapshot()
	cropItemID := uint32(0)
	for _, plot := range visitSnap.GetPlots() {
		if plot.GetPlotId() == friendPlotID {
			cropItemID = plot.GetCropItemId()
			break
		}
	}
	if cropItemID == 0 {
		t.Fatal("visit snapshot missing crop_item_id for steal plot")
	}
	stealStarted := time.Now()
	stealResponse := stealFriendCrop(
		t, visitorA, owner.id, visitA.GetVisitId(), friendPlotID,
		cropItemID, visitSnap.GetVersion().GetFarmViewEpoch(), visitSnap.GetVersion().GetFarmViewSeq(),
	)
	t.Logf("STEAL_FRIEND_CROP end-to-end latency=%s", time.Since(stealStarted))
	if stealResponse.GetVisitorPatch() == nil {
		t.Fatalf("steal missing visitor patch: %+v", stealResponse)
	}
	_ = readMatchingPush(t, owner.conn, wsv1.Action_FARM_VIEW_CHANGED, 5*time.Second)
	_ = readMatchingPush(t, visitorB.conn, wsv1.Action_FARM_VIEW_CHANGED, 5*time.Second)

	harvestPlot(t, owner, friendPlotID)
	_ = readMatchingPush(t, visitorA.conn, wsv1.Action_FARM_VIEW_CHANGED, 5*time.Second)
	_ = readMatchingPush(t, visitorB.conn, wsv1.Action_FARM_VIEW_CHANGED, 5*time.Second)

	helpResponse := helpCleanFriendPlot(t, visitorA, owner.id, visitA.GetVisitId(), friendPlotID)
	if helpResponse.GetFarmPatch().GetPlotUpserts()[0].GetPlotState() != plotv1.PlotState_EMPTY {
		t.Fatalf("help clean did not empty plot: %+v", helpResponse)
	}
	_ = readMatchingPush(t, owner.conn, wsv1.Action_FARM_VIEW_CHANGED, 5*time.Second)
	_ = readMatchingPush(t, visitorB.conn, wsv1.Action_FARM_VIEW_CHANGED, 5*time.Second)

	exitFriendFarm(t, visitorA, owner.id, visitA.GetVisitId())
	left := readMatchingPush(t, owner.conn, wsv1.Action_FARM_PRESENCE_CHANGED, 5*time.Second)
	if left.GetFarmPresenceChangedPush().GetKind() != wsv1.FarmPresenceKind_FARM_VISITOR_LEFT {
		t.Fatalf("owner expected LEFT presence after exit, got %+v", left.GetFarmPresenceChangedPush())
	}

	buySeeds(t, owner, 1)
	plantSeed(t, owner, friendPlotID, friendSeedItemID)
	stillVisiting := readMatchingPush(t, visitorB.conn, wsv1.Action_FARM_VIEW_CHANGED, 5*time.Second)
	if stillVisiting.GetFarmViewChangedPush() == nil {
		t.Fatalf("remaining visitor missed plant FarmViewPatch")
	}
	assertNoEnvelope(t, visitorA.conn, 1500*time.Millisecond)

	t.Log("friend interaction E2E passed")
}

func registerFriendPlayer(t *testing.T, accountName string) *friendPlayer {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 8 * time.Second}
	loginURL := envOr("E2E_LOGIN_URL", defaultLoginURL)
	csrf := getCSRF(t, client, loginURL)
	register := &httpv1.RegisterResponse{}
	doProto(t, client, http.MethodPost, loginURL+"/v1/auth/register",
		&httpv1.RegisterRequest{AccountName: accountName, Password: e2ePassword},
		csrf, http.StatusCreated, register)
	authenticatedCSRF := getCSRF(t, client, loginURL)
	ticket := &httpv1.WsTicketResponse{}
	doProto(t, client, http.MethodPost, loginURL+"/v1/ws-tickets",
		&httpv1.WsTicketRequest{TicketRequestId: newUUID(t), GatewayId: "local-gateway"},
		authenticatedCSRF, http.StatusCreated, ticket)
	return &friendPlayer{
		id: register.GetSession().GetPlayerId(), account: accountName,
		client: client, csrf: authenticatedCSRF, wsTicket: ticket.GetWsTicket(),
	}
}

func loginFriendPlayer(t *testing.T, accountName string) *friendPlayer {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 8 * time.Second}
	loginURL := envOr("E2E_LOGIN_URL", defaultLoginURL)
	csrf := getCSRF(t, client, loginURL)
	session := &httpv1.LoginResponse{}
	doProto(t, client, http.MethodPost, loginURL+"/v1/auth/login",
		&httpv1.LoginRequest{AccountName: accountName, Password: e2ePassword},
		csrf, http.StatusOK, session)
	authenticatedCSRF := getCSRF(t, client, loginURL)
	ticket := &httpv1.WsTicketResponse{}
	doProto(t, client, http.MethodPost, loginURL+"/v1/ws-tickets",
		&httpv1.WsTicketRequest{TicketRequestId: newUUID(t), GatewayId: "local-gateway"},
		authenticatedCSRF, http.StatusCreated, ticket)
	return &friendPlayer{
		id: session.GetSession().GetPlayerId(), account: accountName,
		client: client, csrf: authenticatedCSRF, wsTicket: ticket.GetWsTicket(),
	}
}

func authenticateFriendPlayer(t *testing.T, player *friendPlayer) *websocket.Conn {
	t.Helper()
	conn := dialWebSocket(t, envOr("E2E_GATE_WS_URL", "ws://127.0.0.1:8081/ws"))
	writeEnvelope(t, conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_AUTH, RequestId: newUUID(t),
		Payload: &wsv1.WsEnvelope_AuthRequest{
			AuthRequest: &wsv1.AuthRequest{WsTicket: player.wsTicket},
		},
	})
	response := readEnvelopeWithTimeout(t, conn, friendInteractionTimeout)
	if response.GetError() != nil ||
		response.GetAuthResponse().GetPlayerId() != player.id {
		t.Fatalf("AUTH failed for %s (%d): %+v", player.account, player.id, response)
	}
	return conn
}

func loadOwnSnapshot(t *testing.T, player *friendPlayer) {
	t.Helper()
	requestID := newUUID(t)
	writeEnvelope(t, player.conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_GET_PLAYER_SNAPSHOT, RequestId: requestID,
		TargetPlayerId: player.id,
		Payload: &wsv1.WsEnvelope_GetPlayerSnapshotRequest{
			GetPlayerSnapshotRequest: &wsv1.GetPlayerSnapshotRequest{},
		},
	})
	response := readMatchingResponse(t, player.conn, requestID, friendInteractionTimeout)
	if response.GetError() != nil ||
		response.GetGetPlayerSnapshotResponse().GetSnapshot() == nil {
		t.Fatalf("GET_PLAYER_SNAPSHOT failed for %s: %+v", player.account, response)
	}
}

func createFriendCode(t *testing.T, player *friendPlayer) string {
	t.Helper()
	requestID := newUUID(t)
	writeEnvelope(t, player.conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_CREATE_FRIEND_CODE, RequestId: requestID,
		TargetPlayerId: player.id,
		Payload: &wsv1.WsEnvelope_CreateFriendCodeRequest{
			CreateFriendCodeRequest: &wsv1.CreateFriendCodeRequest{},
		},
	})
	response := readMatchingResponse(t, player.conn, requestID, friendInteractionTimeout)
	code := response.GetCreateFriendCodeResponse().GetCode()
	if response.GetError() != nil || code == "" {
		t.Fatalf("CREATE_FRIEND_CODE failed: %+v", response)
	}
	return code
}

func redeemFriendCode(t *testing.T, player *friendPlayer, code string) {
	t.Helper()
	requestID := newUUID(t)
	writeEnvelope(t, player.conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_REDEEM_FRIEND_CODE, RequestId: requestID,
		TargetPlayerId: player.id,
		Payload: &wsv1.WsEnvelope_RedeemFriendCodeRequest{
			RedeemFriendCodeRequest: &wsv1.RedeemFriendCodeRequest{Code: code},
		},
	})
	response := readMatchingResponse(t, player.conn, requestID, friendInteractionTimeout)
	if response.GetError() != nil ||
		response.GetRedeemFriendCodeResponse().GetFriend() == nil {
		t.Fatalf("REDEEM_FRIEND_CODE failed: %+v", response)
	}
}

func assertFriendListed(t *testing.T, player *friendPlayer, friendID uint64) {
	t.Helper()
	requestID := newUUID(t)
	writeEnvelope(t, player.conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_LIST_FRIENDS, RequestId: requestID,
		TargetPlayerId: player.id,
		Payload: &wsv1.WsEnvelope_ListFriendsRequest{
			ListFriendsRequest: &wsv1.ListFriendsRequest{},
		},
	})
	response := readMatchingResponse(t, player.conn, requestID, friendInteractionTimeout)
	if response.GetError() != nil {
		t.Fatalf("LIST_FRIENDS failed: %+v", response)
	}
	for _, friend := range response.GetListFriendsResponse().GetFriends() {
		if friend.GetPlayerId() == friendID {
			return
		}
	}
	t.Fatalf("player %d missing friend %d in list: %+v",
		player.id, friendID, response.GetListFriendsResponse())
}

func buySeeds(t *testing.T, player *friendPlayer, quantity uint32) {
	t.Helper()
	requestID := newUUID(t)
	writeEnvelope(t, player.conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_BUY_SEEDS, RequestId: requestID,
		TargetPlayerId: player.id,
		Payload: &wsv1.WsEnvelope_BuySeedsRequest{BuySeedsRequest: &wsv1.BuySeedsRequest{
			ShopEntryId: friendSeedShopEntryID, Quantity: quantity,
			ExpectedPriceVersion: friendSeedPriceVersion,
		}},
	})
	response := readMatchingResponse(t, player.conn, requestID, friendInteractionTimeout)
	if response.GetError() != nil {
		t.Fatalf("BUY_SEEDS failed: %+v", response)
	}
}

func plantSeed(t *testing.T, player *friendPlayer, plotID, seedItemID uint32) {
	t.Helper()
	requestID := newUUID(t)
	writeEnvelope(t, player.conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_PLANT, RequestId: requestID,
		TargetPlayerId: player.id,
		Payload: &wsv1.WsEnvelope_PlantRequest{PlantRequest: &wsv1.PlantRequest{
			PlotId: plotID, SeedItemId: seedItemID,
		}},
	})
	response := readMatchingResponse(t, player.conn, requestID, friendInteractionTimeout)
	if response.GetError() != nil {
		t.Fatalf("PLANT failed: %+v", response)
	}
}

func applyFertilizer(t *testing.T, player *friendPlayer, plotID uint32) {
	t.Helper()
	requestID := newUUID(t)
	writeEnvelope(t, player.conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_APPLY_FERTILIZER, RequestId: requestID,
		TargetPlayerId: player.id,
		Payload: &wsv1.WsEnvelope_ApplyFertilizerRequest{
			ApplyFertilizerRequest: &wsv1.ApplyFertilizerRequest{
				PlotId: plotID, FertilizerItemId: friendFertilizerItemID,
			},
		},
	})
	response := readMatchingResponse(t, player.conn, requestID, friendInteractionTimeout)
	if response.GetError() != nil {
		t.Fatalf("APPLY_FERTILIZER failed: %+v", response)
	}
}

func harvestPlot(t *testing.T, player *friendPlayer, plotID uint32) {
	t.Helper()
	requestID := newUUID(t)
	writeEnvelope(t, player.conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_HARVEST, RequestId: requestID,
		TargetPlayerId: player.id,
		Payload: &wsv1.WsEnvelope_HarvestRequest{
			HarvestRequest: &wsv1.HarvestRequest{PlotId: plotID},
		},
	})
	response := readMatchingResponse(t, player.conn, requestID, friendInteractionTimeout)
	if response.GetError() != nil {
		t.Fatalf("HARVEST failed: %+v", response)
	}
}

func enterFriendFarm(
	t *testing.T, visitor *friendPlayer, ownerID uint64,
) *wsv1.EnterFriendFarmResponse {
	t.Helper()
	requestID := newUUID(t)
	writeEnvelope(t, visitor.conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_ENTER_FRIEND_FARM, RequestId: requestID,
		TargetPlayerId: visitor.id,
		Payload: &wsv1.WsEnvelope_EnterFriendFarmRequest{
			EnterFriendFarmRequest: &wsv1.EnterFriendFarmRequest{OwnerPlayerId: ownerID},
		},
	})
	response := readMatchingResponse(t, visitor.conn, requestID, friendInteractionTimeout)
	enter := response.GetEnterFriendFarmResponse()
	if response.GetError() != nil || enter == nil || len(enter.GetVisitId()) != 16 {
		t.Fatalf("ENTER_FRIEND_FARM failed: %+v", response)
	}
	return enter
}

func exitFriendFarm(t *testing.T, visitor *friendPlayer, ownerID uint64, visitID []byte) {
	t.Helper()
	requestID := newUUID(t)
	writeEnvelope(t, visitor.conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_EXIT_FRIEND_FARM, RequestId: requestID,
		TargetPlayerId: visitor.id,
		Payload: &wsv1.WsEnvelope_ExitFriendFarmRequest{
			ExitFriendFarmRequest: &wsv1.ExitFriendFarmRequest{
				OwnerPlayerId: ownerID, VisitId: visitID,
			},
		},
	})
	response := readMatchingResponse(t, visitor.conn, requestID, friendInteractionTimeout)
	if response.GetError() != nil {
		t.Fatalf("EXIT_FRIEND_FARM failed: %+v", response)
	}
}

func applyPestToFriend(
	t *testing.T, visitor *friendPlayer, ownerID uint64, visitID []byte, plotID uint32,
) *wsv1.FriendActionResponse {
	t.Helper()
	return friendAction(t, visitor, wsv1.Action_APPLY_PEST_TO_FRIEND,
		&wsv1.WsEnvelope_ApplyPestToFriendRequest{
			ApplyPestToFriendRequest: &wsv1.ApplyPestToFriendRequest{
				OwnerPlayerId: ownerID, VisitId: visitID, PlotId: plotID, PestId: friendPestID,
			},
		})
}

func catchPestForFriend(
	t *testing.T, visitor *friendPlayer, ownerID uint64, visitID []byte, plotID uint32,
) *wsv1.FriendActionResponse {
	t.Helper()
	return friendAction(t, visitor, wsv1.Action_CATCH_PEST_FOR_FRIEND,
		&wsv1.WsEnvelope_CatchPestForFriendRequest{
			CatchPestForFriendRequest: &wsv1.CatchPestForFriendRequest{
				OwnerPlayerId: ownerID, VisitId: visitID, PlotId: plotID,
			},
		})
}

func stealFriendCrop(
	t *testing.T, visitor *friendPlayer, ownerID uint64, visitID []byte, plotID uint32,
	cropItemID uint32, farmViewEpoch []byte, farmViewSeq uint64,
) *wsv1.FriendActionResponse {
	t.Helper()
	return friendAction(t, visitor, wsv1.Action_STEAL_FRIEND_CROP,
		&wsv1.WsEnvelope_StealFriendCropRequest{
			StealFriendCropRequest: &wsv1.StealFriendCropRequest{
				OwnerPlayerId: ownerID, VisitId: visitID, PlotId: plotID,
				ExpectedCropItemId: cropItemID,
				FarmViewEpoch:      farmViewEpoch,
				FarmViewSeq:        farmViewSeq,
			},
		})
}

func helpCleanFriendPlot(
	t *testing.T, visitor *friendPlayer, ownerID uint64, visitID []byte, plotID uint32,
) *wsv1.FriendActionResponse {
	t.Helper()
	return friendAction(t, visitor, wsv1.Action_HELP_CLEAN_FRIEND_PLOT,
		&wsv1.WsEnvelope_HelpCleanFriendPlotRequest{
			HelpCleanFriendPlotRequest: &wsv1.HelpCleanFriendPlotRequest{
				OwnerPlayerId: ownerID, VisitId: visitID, PlotId: plotID,
			},
		})
}

func friendAction(
	t *testing.T,
	visitor *friendPlayer,
	action wsv1.Action,
	payload any,
) *wsv1.FriendActionResponse {
	t.Helper()
	requestID := newUUID(t)
	envelope := &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
		Action: action, RequestId: requestID, TargetPlayerId: visitor.id,
	}
	switch typed := payload.(type) {
	case *wsv1.WsEnvelope_ApplyPestToFriendRequest:
		envelope.Payload = typed
	case *wsv1.WsEnvelope_CatchPestForFriendRequest:
		envelope.Payload = typed
	case *wsv1.WsEnvelope_StealFriendCropRequest:
		envelope.Payload = typed
	case *wsv1.WsEnvelope_HelpCleanFriendPlotRequest:
		envelope.Payload = typed
	default:
		t.Fatalf("unsupported friend action payload %T", payload)
	}
	writeEnvelope(t, visitor.conn, envelope)
	var response *wsv1.WsEnvelope
	var result *wsv1.FriendActionResponse
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			writeEnvelope(t, visitor.conn, envelope)
		}
		response = readMatchingResponse(t, visitor.conn, requestID, friendInteractionTimeout)
		switch action {
		case wsv1.Action_APPLY_PEST_TO_FRIEND:
			result = response.GetApplyPestToFriendResponse()
		case wsv1.Action_CATCH_PEST_FOR_FRIEND:
			result = response.GetCatchPestForFriendResponse()
		case wsv1.Action_STEAL_FRIEND_CROP:
			result = response.GetStealFriendCropResponse()
		case wsv1.Action_HELP_CLEAN_FRIEND_PLOT:
			result = response.GetHelpCleanFriendPlotResponse()
		}
		if response.GetError() == nil && result != nil {
			return result
		}
		if response.GetError() == nil ||
			response.GetError().GetCode() != wsv1.ErrorCode_INTERACTION_OUTCOME_UNKNOWN ||
			!response.GetError().GetRetryable() {
			break
		}
		t.Logf("%s attempt %d outcome unknown; retrying", action.String(), attempt+1)
	}
	t.Fatalf("%s failed: %+v", action.String(), response)
	return nil
}

func farmHeartbeat(t *testing.T, visitor *friendPlayer, ownerID uint64, visitID []byte) {
	t.Helper()
	requestID := newUUID(t)
	writeEnvelope(t, visitor.conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_FARM_HEARTBEAT, RequestId: requestID,
		TargetPlayerId: visitor.id,
		Payload: &wsv1.WsEnvelope_FarmHeartbeatRequest{
			FarmHeartbeatRequest: &wsv1.FarmHeartbeatRequest{
				OwnerPlayerId: ownerID, VisitId: visitID,
			},
		},
	})
	response := readMatchingResponse(t, visitor.conn, requestID, friendInteractionTimeout)
	if response.GetError() != nil {
		t.Fatalf("FARM_HEARTBEAT failed: %+v", response)
	}
}

func waitPlotMature(
	t *testing.T,
	owner *friendPlayer,
	plotID uint32,
	visitA, visitB *wsv1.EnterFriendFarmResponse,
	visitorA, visitorB *friendPlayer,
	ownerID uint64,
) {
	t.Helper()
	deadline := time.Now().Add(friendMaturityTimeout)
	for time.Now().Before(deadline) {
		farmHeartbeat(t, visitorA, ownerID, visitA.GetVisitId())
		farmHeartbeat(t, visitorB, ownerID, visitB.GetVisitId())
		requestID := newUUID(t)
		writeEnvelope(t, owner.conn, &wsv1.WsEnvelope{
			ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
			Action: wsv1.Action_GET_PLAYER_SNAPSHOT, RequestId: requestID,
			TargetPlayerId: owner.id,
			Payload: &wsv1.WsEnvelope_GetPlayerSnapshotRequest{
				GetPlayerSnapshotRequest: &wsv1.GetPlayerSnapshotRequest{},
			},
		})
		response := readMatchingResponse(t, owner.conn, requestID, friendInteractionTimeout)
		snapshot := response.GetGetPlayerSnapshotResponse().GetSnapshot()
		if response.GetError() != nil || snapshot == nil {
			t.Fatalf("snapshot while waiting maturity failed: %+v", response)
		}
		for _, plot := range snapshot.GetPlots() {
			if plot.GetPlotId() == plotID && plot.GetPlotState() == plotv1.PlotState_MATURE {
				t.Logf("plot %d matured", plotID)
				return
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("plot %d did not mature within %s", plotID, friendMaturityTimeout)
}

func readMatchingResponse(
	t *testing.T, conn *websocket.Conn, requestID string, timeout time.Duration,
) *wsv1.WsEnvelope {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining < time.Second {
			remaining = time.Second
		}
		envelope := readEnvelopeWithTimeout(t, conn, remaining)
		if envelope.GetMessageKind() == wsv1.MessageKind_RESPONSE &&
			envelope.GetRequestId() == requestID {
			return envelope
		}
	}
	t.Fatalf("timed out waiting for response request_id=%s", requestID)
	return nil
}

func readMatchingPush(
	t *testing.T, conn *websocket.Conn, action wsv1.Action, timeout time.Duration,
) *wsv1.WsEnvelope {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining < time.Second {
			remaining = time.Second
		}
		envelope := readEnvelopeWithTimeout(t, conn, remaining)
		if envelope.GetMessageKind() == wsv1.MessageKind_PUSH &&
			envelope.GetAction() == action {
			return envelope
		}
	}
	t.Fatalf("timed out waiting for push action=%s", action.String())
	return nil
}

func assertNoEnvelope(t *testing.T, conn *websocket.Conn, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	messageType, body, err := conn.Read(ctx)
	if err != nil {
		return
	}
	envelope := &wsv1.WsEnvelope{}
	_ = proto.Unmarshal(body, envelope)
	t.Fatalf("expected no WS traffic after exit, got type=%v action=%s kind=%s",
		messageType, envelope.GetAction().String(), envelope.GetMessageKind().String())
}

func assertPublicPest(t *testing.T, patch *wsv1.FarmViewPatch, want bool) {
	t.Helper()
	if patch == nil || len(patch.GetPlotUpserts()) == 0 {
		t.Fatalf("missing farm view patch for pest assertion")
	}
	if patch.GetPlotUpserts()[0].GetPestActive() != want {
		t.Fatalf("pest_active=%v want=%v patch=%+v",
			patch.GetPlotUpserts()[0].GetPestActive(), want, patch)
	}
}

func publicPlotFingerprint(snapshot *wsv1.FarmVisitSnapshot) []byte {
	if snapshot == nil {
		return nil
	}
	body, _ := proto.Marshal(snapshot)
	return body
}
