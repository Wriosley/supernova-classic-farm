package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	httpv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/http"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	chapterv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/chapter"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
	reasonv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/reason"
	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"
)

const (
	protobufMediaType = "application/x-protobuf"
	defaultLoginURL   = "http://127.0.0.1:8080"
	h5Origin          = "http://localhost:5173"
	e2ePassword       = "e2e-password-2026"
)

func TestAuthenticatedSnapshot(t *testing.T) {
	if os.Getenv("E2E_RUN") != "1" {
		t.Skip("set E2E_RUN=1 and start dependencies, or use tests/e2e/run-authenticated-snapshot.ps1")
	}
	loginURL := envOr("E2E_LOGIN_URL", defaultLoginURL)
	baseURL, err := url.Parse(loginURL)
	if err != nil {
		t.Fatalf("parse Login URL: %v", err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	client := &http.Client{
		Jar:     jar,
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	initialCSRF := getCSRF(t, client, loginURL)
	accountName := envOr("E2E_ACCOUNT_NAME", uniqueAccountName(t))
	authMode := envOr("E2E_AUTH_MODE", "register")
	var session *httpv1.SessionView
	switch authMode {
	case "register":
		register := &httpv1.RegisterResponse{}
		doProto(t, client, http.MethodPost, loginURL+"/v1/auth/register",
			&httpv1.RegisterRequest{AccountName: accountName, Password: e2ePassword},
			initialCSRF, http.StatusCreated, register)
		session = register.GetSession()
	case "login":
		login := &httpv1.LoginResponse{}
		doProto(t, client, http.MethodPost, loginURL+"/v1/auth/login",
			&httpv1.LoginRequest{AccountName: accountName, Password: e2ePassword},
			initialCSRF, http.StatusOK, login)
		session = login.GetSession()
	default:
		t.Fatalf("unsupported E2E_AUTH_MODE %q", authMode)
	}
	if session.GetPlayerId() == 0 || session.GetAccountName() != accountName {
		t.Fatalf("%s Session mismatch: %+v", authMode, session)
	}
	playerID := session.GetPlayerId()

	rotatedCSRF := cookieValue(jar.Cookies(baseURL), "cf_csrf_dev")
	if rotatedCSRF == "" || rotatedCSRF == initialCSRF {
		t.Fatalf("successful %s did not rotate the CSRF cookie", authMode)
	}
	authenticatedCSRF := getCSRF(t, client, loginURL)
	t.Logf("HTTP_AUTH mode=%s account=%s player_id=%d csrf_rotated=true", authMode, accountName, playerID)

	bootstrap := &httpv1.ClientBootstrapResponse{}
	doProto(t, client, http.MethodGet, loginURL+"/v1/bootstrap", nil, "", http.StatusOK, bootstrap)
	authBootstrap := bootstrap.GetAuthBootstrap()
	if authBootstrap == nil || authBootstrap.GetPlayerId() != playerID || len(bootstrap.GetGateways()) == 0 {
		t.Fatalf("invalid bootstrap: %+v", bootstrap)
	}
	gateway := bootstrap.GetGateways()[0]
	if gateway.GetGatewayId() == "" || gateway.GetWebsocketUrl() == "" {
		t.Fatalf("invalid bootstrap gateway: %+v", gateway)
	}
	t.Logf("BOOTSTRAP player_id=%d gateway_id=%s websocket_url=%s config_version=%d protocol=%d..%d",
		playerID, gateway.GetGatewayId(), gateway.GetWebsocketUrl(),
		authBootstrap.GetClientConfigVersion(), authBootstrap.GetProtocolMin(), authBootstrap.GetProtocolMax())

	configBody := downloadConfig(t, client, authBootstrap.GetClientConfigUrl())
	configDigest := sha256.Sum256(configBody)
	if !bytes.Equal(configDigest[:], authBootstrap.GetClientConfigSha256()) {
		t.Fatalf("client config SHA-256 mismatch: got %x want %x", configDigest, authBootstrap.GetClientConfigSha256())
	}
	configPackage := &httpv1.ClientConfigPackage{}
	if err := proto.Unmarshal(configBody, configPackage); err != nil {
		t.Fatalf("decode client config: %v", err)
	}
	if configPackage.GetSchemaVersion() != 1 ||
		configPackage.GetClientConfigVersion() != authBootstrap.GetClientConfigVersion() {
		t.Fatalf("client config version mismatch: schema=%d package=%d bootstrap=%d",
			configPackage.GetSchemaVersion(), configPackage.GetClientConfigVersion(),
			authBootstrap.GetClientConfigVersion())
	}
	t.Logf("CONFIG bytes=%d sha256=%x schema_version=%d client_config_version=%d",
		len(configBody), configDigest, configPackage.GetSchemaVersion(), configPackage.GetClientConfigVersion())

	ticketResponse := &httpv1.WsTicketResponse{}
	doProto(t, client, http.MethodPost, loginURL+"/v1/ws-tickets",
		&httpv1.WsTicketRequest{TicketRequestId: newUUID(t), GatewayId: gateway.GetGatewayId()},
		authenticatedCSRF, http.StatusCreated, ticketResponse)
	if ticketResponse.GetWsTicket() == "" || ticketResponse.GetGatewayId() != gateway.GetGatewayId() {
		t.Fatalf("ticket is not bound to bootstrap gateway: %+v", ticketResponse)
	}
	t.Logf("TICKET status=201 gateway_id=%s expires_at_ms=%d", ticketResponse.GetGatewayId(), ticketResponse.GetExpiresAtMs())

	conn := dialWebSocket(t, gateway.GetWebsocketUrl())
	defer conn.CloseNow()
	authRequestID := newUUID(t)
	writeEnvelope(t, conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1,
		MessageKind:     wsv1.MessageKind_REQUEST,
		Action:          wsv1.Action_AUTH,
		RequestId:       authRequestID,
		Payload: &wsv1.WsEnvelope_AuthRequest{
			AuthRequest: &wsv1.AuthRequest{WsTicket: ticketResponse.GetWsTicket()},
		},
	})
	authEnvelope := readEnvelope(t, conn)
	if authEnvelope.GetMessageKind() != wsv1.MessageKind_RESPONSE ||
		authEnvelope.GetAction() != wsv1.Action_AUTH ||
		authEnvelope.GetRequestId() != authRequestID ||
		authEnvelope.GetError() != nil {
		t.Fatalf("invalid AUTH envelope: %+v", authEnvelope)
	}
	authResponse := authEnvelope.GetAuthResponse()
	assertAuthMatchesBootstrap(t, authResponse, authBootstrap)
	t.Logf("AUTH request_id=%s seven_fields_match_bootstrap=true", authRequestID)

	pingRequestID := newUUID(t)
	const pingID uint64 = 17
	clientSentAt := time.Now().UnixMilli()
	writeEnvelope(t, conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1,
		MessageKind:     wsv1.MessageKind_REQUEST,
		Action:          wsv1.Action_PING,
		RequestId:       pingRequestID,
		Payload: &wsv1.WsEnvelope_PingRequest{
			PingRequest: &wsv1.PingRequest{PingId: pingID, ClientSentAtMs: clientSentAt},
		},
	})
	pingEnvelope := readEnvelope(t, conn)
	ping := pingEnvelope.GetPingResponse()
	if pingEnvelope.GetMessageKind() != wsv1.MessageKind_RESPONSE ||
		pingEnvelope.GetAction() != wsv1.Action_PING ||
		pingEnvelope.GetRequestId() != pingRequestID ||
		ping == nil || ping.GetPingId() != pingID || ping.GetClientSentAtMs() != clientSentAt {
		t.Fatalf("uncorrelated PING response: %+v", pingEnvelope)
	}
	t.Logf("PING request_id=%s ping_id=%d correlated=true", pingRequestID, pingID)

	shopRequestID := newUUID(t)
	writeEnvelope(t, conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1,
		MessageKind:     wsv1.MessageKind_REQUEST,
		Action:          wsv1.Action_GET_SHOP,
		RequestId:       shopRequestID,
		TargetPlayerId:  playerID,
		Payload: &wsv1.WsEnvelope_GetShopRequest{
			GetShopRequest: &wsv1.GetShopRequest{},
		},
	})
	shopEnvelope := readEnvelope(t, conn)
	shop := shopEnvelope.GetGetShopResponse()
	if shopEnvelope.GetMessageKind() != wsv1.MessageKind_RESPONSE ||
		shopEnvelope.GetAction() != wsv1.Action_GET_SHOP ||
		shopEnvelope.GetRequestId() != shopRequestID ||
		shopEnvelope.GetTargetPlayerId() != playerID ||
		shopEnvelope.GetError() != nil ||
		shop.GetServerConfigVersion() != 1 ||
		len(shop.GetEntries()) != 2 ||
		!shop.GetEntries()[0].GetEnabled() {
		t.Fatalf("invalid GET_SHOP response: %+v", shopEnvelope)
	}
	seedQuote := shop.GetEntries()[0]
	cropQuote := shop.GetEntries()[1]
	if seedQuote.GetItemId() != 1001 || cropQuote.GetItemId() != 1002 ||
		!cropQuote.GetEnabled() {
		t.Fatalf("invalid development buy/sell quotes: %+v", shop.GetEntries())
	}
	t.Logf("GET_SHOP request_id=%s config_version=%d seed_entry_id=%d seed_price=%d seed_price_version=%d crop_entry_id=%d crop_price=%d crop_price_version=%d",
		shopRequestID, shop.GetServerConfigVersion(), seedQuote.GetShopEntryId(),
		seedQuote.GetUnitPrice(), seedQuote.GetPriceVersion(), cropQuote.GetShopEntryId(),
		cropQuote.GetUnitPrice(), cropQuote.GetPriceVersion())

	snapshotRequestID := newUUID(t)
	writeEnvelope(t, conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1,
		MessageKind:     wsv1.MessageKind_REQUEST,
		Action:          wsv1.Action_GET_PLAYER_SNAPSHOT,
		RequestId:       snapshotRequestID,
		TargetPlayerId:  playerID,
		Payload: &wsv1.WsEnvelope_GetPlayerSnapshotRequest{
			GetPlayerSnapshotRequest: &wsv1.GetPlayerSnapshotRequest{},
		},
	})
	snapshotEnvelope := readEnvelope(t, conn)
	snapshot := snapshotEnvelope.GetGetPlayerSnapshotResponse().GetSnapshot()
	expectedPlayerSeq := envUint64(t, "E2E_EXPECT_PLAYER_SEQ", 0)
	expectedCoins := int64(10)
	expectedFertilizerQuantity := uint32(1)
	expectedSeedQuantity := uint32(0)
	expectedCropQuantity := uint32(0)
	expectedNextSeedQuantity := uint32(0)
	expectedPlotState := plotv1.PlotState_EMPTY
	expectedChapterID := uint32(1)
	expectedChapterStatus := chapterv1.ChapterStatus_IN_PROGRESS
	if expectedPlayerSeq >= 1 {
		expectedCoins = 4
		expectedSeedQuantity = 3
	}
	if expectedPlayerSeq >= 2 {
		expectedSeedQuantity = 2
		expectedPlotState = plotv1.PlotState_GROWING
	}
	if expectedPlayerSeq >= 3 {
		expectedFertilizerQuantity = 0
	}
	if expectedPlayerSeq >= 4 {
		expectedPlotState = plotv1.PlotState_MATURE
	}
	if expectedPlayerSeq >= 5 {
		expectedCropQuantity = 3
		expectedPlotState = plotv1.PlotState_NEED_CLEANUP
	}
	if expectedPlayerSeq >= 6 {
		expectedCoins = 19
		expectedCropQuantity = 0
		expectedChapterStatus = chapterv1.ChapterStatus_CLAIMABLE
	}
	if expectedPlayerSeq >= 7 {
		expectedCoins = 29
		expectedFertilizerQuantity = 1
		expectedNextSeedQuantity = 3
		expectedChapterID = 2
		expectedChapterStatus = chapterv1.ChapterStatus_IN_PROGRESS
	}
	if expectedPlayerSeq >= 8 {
		expectedPlotState = plotv1.PlotState_EMPTY
	}
	if snapshotEnvelope.GetMessageKind() != wsv1.MessageKind_RESPONSE ||
		snapshotEnvelope.GetAction() != wsv1.Action_GET_PLAYER_SNAPSHOT ||
		snapshotEnvelope.GetRequestId() != snapshotRequestID ||
		snapshotEnvelope.GetTargetPlayerId() != playerID ||
		snapshotEnvelope.GetError() != nil || snapshotEnvelope.GetStateVersion() == nil ||
		snapshotEnvelope.GetStateVersion().GetOwnerEpoch() != 1 ||
		snapshotEnvelope.GetStateVersion().GetPlayerSeq() != expectedPlayerSeq ||
		snapshot == nil || snapshot.GetPlayerId() != playerID {
		t.Fatalf("invalid snapshot envelope: %+v", snapshotEnvelope)
	}
	if snapshot.GetCoinBalance() != expectedCoins {
		t.Fatalf("coin balance = %d, want %d", snapshot.GetCoinBalance(), expectedCoins)
	}
	if inventoryQuantity(snapshot, 1) != expectedFertilizerQuantity ||
		inventoryQuantity(snapshot, 1001) != expectedSeedQuantity ||
		inventoryQuantity(snapshot, 1002) != expectedCropQuantity ||
		inventoryQuantity(snapshot, 1003) != expectedNextSeedQuantity {
		t.Fatalf("inventory mismatch: %+v", snapshot.GetInventory())
	}
	if len(snapshot.GetPlots()) != 4 ||
		snapshot.GetPlots()[0].GetPlotId() != 1 ||
		snapshot.GetPlots()[0].GetPlotState() != expectedPlotState {
		t.Fatalf("plot mismatch: %+v", snapshot.GetPlots())
	}
	for index, plot := range snapshot.GetPlots()[1:] {
		if plot.GetPlotId() != uint32(index+2) ||
			plot.GetPlotState() != plotv1.PlotState_EMPTY {
			t.Fatalf("secondary plot mismatch: %+v", snapshot.GetPlots())
		}
	}
	if snapshot.GetCurrentChapter().GetChapterId() != expectedChapterID ||
		snapshot.GetCurrentChapter().GetStatus() != expectedChapterStatus {
		t.Fatalf("chapter = %d/%s, want %d/%s",
			snapshot.GetCurrentChapter().GetChapterId(),
			snapshot.GetCurrentChapter().GetStatus(),
			expectedChapterID, expectedChapterStatus)
	}
	if expectedPlotState == plotv1.PlotState_GROWING &&
		(snapshot.GetPlots()[0].GetCropId() != 2001 ||
			snapshot.GetPlots()[0].GetEstimatedMatureAtMs() == 0) {
		t.Fatalf("growing plot fields mismatch: %+v", snapshot.GetPlots()[0])
	}
	if expectedPlayerSeq == 3 && snapshot.GetPlots()[0].GetFertilizerEffect() == nil {
		t.Fatalf("fertilizer effect was not recovered: %+v", snapshot.GetPlots()[0])
	}
	t.Logf("SNAPSHOT request_id=%s target_player_id=%d snapshot_player_id=%d owner_epoch=%d player_seq=%d coins=%d seed_quantity=%d plot_state=%s",
		snapshotRequestID, snapshotEnvelope.GetTargetPlayerId(), snapshot.GetPlayerId(),
		snapshotEnvelope.GetStateVersion().GetOwnerEpoch(), snapshotEnvelope.GetStateVersion().GetPlayerSeq(),
		snapshot.GetCoinBalance(), expectedSeedQuantity, expectedPlotState.String())

	wroteGameCommand := false
	if os.Getenv("E2E_BUY_SEEDS") == "1" {
		buyRequestID := newUUID(t)
		buyRequest := &wsv1.WsEnvelope{
			ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
			Action: wsv1.Action_BUY_SEEDS, RequestId: buyRequestID, TargetPlayerId: playerID,
			Payload: &wsv1.WsEnvelope_BuySeedsRequest{BuySeedsRequest: &wsv1.BuySeedsRequest{
				ShopEntryId: seedQuote.GetShopEntryId(), Quantity: 3,
				ExpectedPriceVersion: seedQuote.GetPriceVersion(),
			}},
		}
		writeEnvelope(t, conn, buyRequest)
		buyEnvelope := readEnvelope(t, conn)
		buy := buyEnvelope.GetBuySeedsResponse()
		if buyEnvelope.GetError() != nil || buyEnvelope.GetReplayed() ||
			buyEnvelope.GetStateVersion().GetPlayerSeq() != expectedPlayerSeq+1 ||
			buy.GetPatch().GetCoinBalance() != 4 ||
			buy.GetPatch().GetInventoryUpserts()[0].GetQuantity() != 3 {
			t.Fatalf("invalid BUY_SEEDS response: %+v", buyEnvelope)
		}
		writeEnvelope(t, conn, buyRequest)
		replayedBuy := readEnvelope(t, conn)
		if !replayedBuy.GetReplayed() ||
			replayedBuy.GetStateVersion().GetPlayerSeq() != expectedPlayerSeq+1 ||
			!proto.Equal(replayedBuy.GetBuySeedsResponse(), buy) {
			t.Fatalf("BUY_SEEDS replay mismatch: %+v", replayedBuy)
		}
		t.Logf("BUY_SEEDS request_id=%s player_seq=%d coins=4 seed_item_1001=3 replayed=true",
			buyRequestID, buyEnvelope.GetStateVersion().GetPlayerSeq())
		wroteGameCommand = true
	}

	if os.Getenv("E2E_PLANT") == "1" {
		if os.Getenv("E2E_BUY_SEEDS") != "1" {
			t.Fatal("E2E_PLANT requires E2E_BUY_SEEDS in the same phase")
		}
		plantRequestID := newUUID(t)
		plantRequest := &wsv1.WsEnvelope{
			ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
			Action: wsv1.Action_PLANT, RequestId: plantRequestID, TargetPlayerId: playerID,
			Payload: &wsv1.WsEnvelope_PlantRequest{PlantRequest: &wsv1.PlantRequest{
				PlotId: 1, SeedItemId: seedQuote.GetItemId(),
			}},
		}
		writeEnvelope(t, conn, plantRequest)
		plantEnvelope := readEnvelope(t, conn)
		plant := plantEnvelope.GetPlantResponse()
		plantPlot := plant.GetPatch().GetPlotUpserts()[0]
		if plantEnvelope.GetError() != nil || plantEnvelope.GetReplayed() ||
			plantEnvelope.GetStateVersion().GetPlayerSeq() != expectedPlayerSeq+2 ||
			plant.GetPatch().GetInventoryUpserts()[0].GetQuantity() != 2 ||
			plantPlot.GetPlotState() != plotv1.PlotState_GROWING ||
			plantPlot.GetCropId() != 2001 ||
			plantPlot.GetEstimatedMatureAtMs() == 0 {
			t.Fatalf("invalid PLANT response: %+v", plantEnvelope)
		}
		writeEnvelope(t, conn, plantRequest)
		replayedPlant := readEnvelope(t, conn)
		if !replayedPlant.GetReplayed() ||
			replayedPlant.GetStateVersion().GetPlayerSeq() != expectedPlayerSeq+2 ||
			!proto.Equal(replayedPlant.GetPlantResponse(), plant) {
			t.Fatalf("PLANT replay mismatch: %+v", replayedPlant)
		}
		t.Logf("PLANT request_id=%s player_seq=%d seed_item_1001=2 plot_id=1 plot_state=GROWING crop_id=2001 replayed=true",
			plantRequestID, plantEnvelope.GetStateVersion().GetPlayerSeq())
		wroteGameCommand = true
	}
	if os.Getenv("E2E_APPLY_FERTILIZER") == "1" {
		if os.Getenv("E2E_PLANT") != "1" {
			t.Fatal("E2E_APPLY_FERTILIZER requires E2E_PLANT in the same phase")
		}
		fertilizerRequestID := newUUID(t)
		fertilizerRequest := &wsv1.WsEnvelope{
			ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
			Action:    wsv1.Action_APPLY_FERTILIZER,
			RequestId: fertilizerRequestID, TargetPlayerId: playerID,
			Payload: &wsv1.WsEnvelope_ApplyFertilizerRequest{
				ApplyFertilizerRequest: &wsv1.ApplyFertilizerRequest{
					PlotId: 1, FertilizerItemId: 1,
				},
			},
		}
		writeEnvelope(t, conn, fertilizerRequest)
		fertilizerEnvelope := readEnvelope(t, conn)
		applied := fertilizerEnvelope.GetApplyFertilizerResponse()
		fertilizedPlot := applied.GetPatch().GetPlotUpserts()[0]
		if fertilizerEnvelope.GetError() != nil || fertilizerEnvelope.GetReplayed() ||
			fertilizerEnvelope.GetStateVersion().GetPlayerSeq() != expectedPlayerSeq+3 ||
			applied.GetEffectInstanceId() == "" ||
			len(applied.GetPatch().GetInventoryRemovedItemIds()) != 1 ||
			fertilizedPlot.GetFertilizerEffect() == nil ||
			fertilizedPlot.GetEstimatedMatureAtMs() == 0 {
			t.Fatalf("invalid APPLY_FERTILIZER response: %+v", fertilizerEnvelope)
		}
		writeEnvelope(t, conn, fertilizerRequest)
		replayedFertilizer := readEnvelope(t, conn)
		if !replayedFertilizer.GetReplayed() ||
			replayedFertilizer.GetStateVersion().GetPlayerSeq() != expectedPlayerSeq+3 ||
			!proto.Equal(replayedFertilizer.GetApplyFertilizerResponse(), applied) {
			t.Fatalf("APPLY_FERTILIZER replay mismatch: %+v", replayedFertilizer)
		}
		t.Logf("APPLY_FERTILIZER request_id=%s player_seq=%d fertilizer_item_1=0 effect_instance_id=%s replayed=true",
			fertilizerRequestID, fertilizerEnvelope.GetStateVersion().GetPlayerSeq(),
			applied.GetEffectInstanceId())
		wroteGameCommand = true
	}
	if os.Getenv("E2E_WAIT_MATURITY_PUSH") == "1" {
		if os.Getenv("E2E_APPLY_FERTILIZER") != "1" {
			t.Fatal("E2E_WAIT_MATURITY_PUSH requires E2E_APPLY_FERTILIZER")
		}
		pushEnvelope := readEnvelopeWithTimeout(t, conn, 90*time.Second)
		push := pushEnvelope.GetPlayerStateChangedPush()
		if pushEnvelope.GetMessageKind() != wsv1.MessageKind_PUSH ||
			pushEnvelope.GetAction() != wsv1.Action_PLAYER_STATE_CHANGED ||
			pushEnvelope.GetRequestId() != "" ||
			pushEnvelope.GetTargetPlayerId() != playerID ||
			pushEnvelope.GetStateVersion().GetOwnerEpoch() != 1 ||
			pushEnvelope.GetStateVersion().GetPlayerSeq() != expectedPlayerSeq+4 ||
			push.GetReason() != reasonv1.StateChangeReason_MATURED ||
			len(push.GetPatch().GetPlotUpserts()) != 1 ||
			push.GetPatch().GetPlotUpserts()[0].GetPlotState() != plotv1.PlotState_MATURE {
			t.Fatalf("invalid maturity push: %+v", pushEnvelope)
		}
		t.Logf("PLAYER_STATE_CHANGED reason=MATURED player_seq=%d plot_id=1 plot_state=MATURE request_id_absent=true",
			pushEnvelope.GetStateVersion().GetPlayerSeq())
	}
	if os.Getenv("E2E_HARVEST") == "1" {
		if os.Getenv("E2E_WAIT_MATURITY_PUSH") != "1" {
			t.Fatal("E2E_HARVEST requires E2E_WAIT_MATURITY_PUSH")
		}
		harvestRequestID := newUUID(t)
		harvestRequest := &wsv1.WsEnvelope{
			ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
			Action: wsv1.Action_HARVEST, RequestId: harvestRequestID, TargetPlayerId: playerID,
			Payload: &wsv1.WsEnvelope_HarvestRequest{
				HarvestRequest: &wsv1.HarvestRequest{PlotId: 1},
			},
		}
		writeEnvelope(t, conn, harvestRequest)
		harvestEnvelope := readEnvelope(t, conn)
		harvest := harvestEnvelope.GetHarvestResponse()
		if harvestEnvelope.GetError() != nil || harvestEnvelope.GetReplayed() ||
			harvestEnvelope.GetStateVersion().GetPlayerSeq() != expectedPlayerSeq+5 ||
			harvest.GetCropItemId() != 1002 ||
			harvest.GetHarvestedQuantity() != 3 ||
			harvest.GetPatch().GetInventoryUpserts()[0].GetQuantity() != 3 ||
			harvest.GetPatch().GetPlotUpserts()[0].GetPlotState() != plotv1.PlotState_NEED_CLEANUP ||
			harvest.GetPatch().GetCurrentChapter().GetTasks()[3].GetCurrentValue() != 1 {
			t.Fatalf("invalid HARVEST response: %+v", harvestEnvelope)
		}
		writeEnvelope(t, conn, harvestRequest)
		replayedHarvest := readEnvelope(t, conn)
		if !replayedHarvest.GetReplayed() ||
			replayedHarvest.GetStateVersion().GetPlayerSeq() != expectedPlayerSeq+5 ||
			!proto.Equal(replayedHarvest.GetHarvestResponse(), harvest) {
			t.Fatalf("HARVEST replay mismatch: %+v", replayedHarvest)
		}
		t.Logf("HARVEST request_id=%s player_seq=%d crop_item_1002=3 plot_state=NEED_CLEANUP replayed=true",
			harvestRequestID, harvestEnvelope.GetStateVersion().GetPlayerSeq())
		wroteGameCommand = true
	}
	if os.Getenv("E2E_SELL_CROP") == "1" {
		if os.Getenv("E2E_HARVEST") != "1" {
			t.Fatal("E2E_SELL_CROP requires E2E_HARVEST")
		}
		sellRequestID := newUUID(t)
		sellRequest := &wsv1.WsEnvelope{
			ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
			Action: wsv1.Action_SELL_CROP, RequestId: sellRequestID, TargetPlayerId: playerID,
			Payload: &wsv1.WsEnvelope_SellCropRequest{
				SellCropRequest: &wsv1.SellCropRequest{
					CropItemId: 1002, ExpectedPriceVersion: cropQuote.GetPriceVersion(),
					Amount: &wsv1.SellCropRequest_SellAll{SellAll: true},
				},
			},
		}
		writeEnvelope(t, conn, sellRequest)
		sellEnvelope := readEnvelope(t, conn)
		sold := sellEnvelope.GetSellCropResponse()
		if sellEnvelope.GetError() != nil || sellEnvelope.GetReplayed() ||
			sellEnvelope.GetStateVersion().GetPlayerSeq() != expectedPlayerSeq+6 ||
			sold.GetSoldQuantity() != 3 ||
			sold.GetUnitPrice() != cropQuote.GetUnitPrice() ||
			sold.GetTotalPrice() != cropQuote.GetUnitPrice()*3 ||
			sold.GetPatch().GetCoinBalance() != 19 ||
			len(sold.GetPatch().GetInventoryRemovedItemIds()) != 1 ||
			sold.GetPatch().GetCurrentChapter().GetStatus() != chapterv1.ChapterStatus_CLAIMABLE {
			t.Fatalf("invalid SELL_CROP response: %+v", sellEnvelope)
		}
		writeEnvelope(t, conn, sellRequest)
		replayedSell := readEnvelope(t, conn)
		if !replayedSell.GetReplayed() ||
			replayedSell.GetStateVersion().GetPlayerSeq() != expectedPlayerSeq+6 ||
			!proto.Equal(replayedSell.GetSellCropResponse(), sold) {
			t.Fatalf("SELL_CROP replay mismatch: %+v", replayedSell)
		}
		t.Logf("SELL_CROP request_id=%s player_seq=%d sold_quantity=3 coins=19 chapter_status=CLAIMABLE replayed=true",
			sellRequestID, sellEnvelope.GetStateVersion().GetPlayerSeq())
		wroteGameCommand = true
	}
	if os.Getenv("E2E_CLAIM_CHAPTER_REWARD") == "1" {
		if os.Getenv("E2E_SELL_CROP") != "1" {
			t.Fatal("E2E_CLAIM_CHAPTER_REWARD requires E2E_SELL_CROP")
		}
		claimRequestID := newUUID(t)
		claimRequest := &wsv1.WsEnvelope{
			ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
			Action:    wsv1.Action_CLAIM_CHAPTER_REWARD,
			RequestId: claimRequestID, TargetPlayerId: playerID,
			Payload: &wsv1.WsEnvelope_ClaimChapterRewardRequest{
				ClaimChapterRewardRequest: &wsv1.ClaimChapterRewardRequest{ChapterId: 1},
			},
		}
		writeEnvelope(t, conn, claimRequest)
		claimEnvelope := readEnvelope(t, conn)
		claimed := claimEnvelope.GetClaimChapterRewardResponse()
		if claimEnvelope.GetError() != nil || claimEnvelope.GetReplayed() ||
			claimEnvelope.GetStateVersion().GetPlayerSeq() != expectedPlayerSeq+7 ||
			claimed.GetChapterId() != 1 || claimed.GetCoinGranted() != 10 ||
			len(claimed.GetItemsAddedToInventory()) != 2 ||
			len(claimed.GetItemsPendingMail()) != 0 ||
			claimed.GetPatch().GetCoinBalance() != 29 ||
			claimed.GetPatch().GetCurrentChapter().GetChapterId() != 2 ||
			claimed.GetPatch().GetCurrentChapter().GetStatus() != chapterv1.ChapterStatus_IN_PROGRESS {
			t.Fatalf("invalid CLAIM_CHAPTER_REWARD response: %+v", claimEnvelope)
		}
		writeEnvelope(t, conn, claimRequest)
		replayedClaim := readEnvelope(t, conn)
		if !replayedClaim.GetReplayed() ||
			replayedClaim.GetStateVersion().GetPlayerSeq() != expectedPlayerSeq+7 ||
			!proto.Equal(replayedClaim.GetClaimChapterRewardResponse(), claimed) {
			t.Fatalf("CLAIM_CHAPTER_REWARD replay mismatch: %+v", replayedClaim)
		}
		t.Logf("CLAIM_CHAPTER_REWARD request_id=%s player_seq=%d coins=29 fertilizer_item_1=1 next_seed_item_1003=3 chapter_id=2 replayed=true",
			claimRequestID, claimEnvelope.GetStateVersion().GetPlayerSeq())
		wroteGameCommand = true
	}
	if os.Getenv("E2E_CLEAN_PLOT") == "1" {
		if os.Getenv("E2E_CLAIM_CHAPTER_REWARD") != "1" {
			t.Fatal("E2E_CLEAN_PLOT requires E2E_CLAIM_CHAPTER_REWARD")
		}
		cleanRequestID := newUUID(t)
		cleanRequest := &wsv1.WsEnvelope{
			ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
			Action:    wsv1.Action_CLEAN_PLOT,
			RequestId: cleanRequestID, TargetPlayerId: playerID,
			Payload: &wsv1.WsEnvelope_CleanPlotRequest{
				CleanPlotRequest: &wsv1.CleanPlotRequest{PlotId: 1},
			},
		}
		writeEnvelope(t, conn, cleanRequest)
		cleanEnvelope := readEnvelope(t, conn)
		cleaned := cleanEnvelope.GetCleanPlotResponse()
		if cleanEnvelope.GetError() != nil || cleanEnvelope.GetReplayed() ||
			cleanEnvelope.GetStateVersion().GetPlayerSeq() != expectedPlayerSeq+8 ||
			len(cleaned.GetPatch().GetPlotUpserts()) != 1 ||
			cleaned.GetPatch().GetPlotUpserts()[0].GetPlotId() != 1 ||
			cleaned.GetPatch().GetPlotUpserts()[0].GetPlotState() != plotv1.PlotState_EMPTY {
			t.Fatalf("invalid CLEAN_PLOT response: %+v", cleanEnvelope)
		}
		writeEnvelope(t, conn, cleanRequest)
		replayedClean := readEnvelope(t, conn)
		if !replayedClean.GetReplayed() ||
			replayedClean.GetStateVersion().GetPlayerSeq() != expectedPlayerSeq+8 ||
			!proto.Equal(replayedClean.GetCleanPlotResponse(), cleaned) {
			t.Fatalf("CLEAN_PLOT replay mismatch: %+v", replayedClean)
		}
		t.Logf("CLEAN_PLOT request_id=%s player_seq=%d plot_id=1 plot_state=EMPTY replayed=true",
			cleanRequestID, cleanEnvelope.GetStateVersion().GetPlayerSeq())
		wroteGameCommand = true
	}
	if wroteGameCommand {
		time.Sleep(1500 * time.Millisecond)
	}

	replayConn := dialWebSocket(t, gateway.GetWebsocketUrl())
	defer replayConn.CloseNow()
	writeEnvelope(t, replayConn, &wsv1.WsEnvelope{
		ProtocolVersion: 1,
		MessageKind:     wsv1.MessageKind_REQUEST,
		Action:          wsv1.Action_AUTH,
		RequestId:       newUUID(t),
		Payload: &wsv1.WsEnvelope_AuthRequest{
			AuthRequest: &wsv1.AuthRequest{WsTicket: ticketResponse.GetWsTicket()},
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, replayErr := replayConn.Read(ctx)
	if code := websocket.CloseStatus(replayErr); code != websocket.StatusCode(4401) {
		t.Fatalf("ticket replay close status = %d (%v), want 4401", code, replayErr)
	}
	t.Log("TICKET_REPLAY second_authentication_succeeded=false close_code=4401")
}

func getCSRF(t *testing.T, client *http.Client, loginURL string) string {
	t.Helper()
	response := &httpv1.CsrfResponse{}
	doProto(t, client, http.MethodGet, loginURL+"/v1/auth/csrf", nil, "", http.StatusOK, response)
	if response.GetCsrfToken() == "" {
		t.Fatal("empty CSRF token")
	}
	return response.GetCsrfToken()
}

func doProto(t *testing.T, client *http.Client, method, endpoint string, request proto.Message, csrf string, wantStatus int, response proto.Message) {
	t.Helper()
	var body io.Reader
	if request != nil {
		encoded, err := proto.Marshal(request)
		if err != nil {
			t.Fatalf("marshal %s: %v", endpoint, err)
		}
		body = bytes.NewReader(encoded)
	}
	httpRequest, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		t.Fatalf("create %s request: %v", endpoint, err)
	}
	httpRequest.Header.Set("Accept", protobufMediaType)
	httpRequest.Header.Set("Origin", h5Origin)
	if request != nil {
		httpRequest.Header.Set("Content-Type", protobufMediaType)
	}
	if csrf != "" {
		httpRequest.Header.Set("X-CSRF-Token", csrf)
		httpRequest.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	httpResponse, err := client.Do(httpRequest)
	if err != nil {
		t.Fatalf("%s %s: %v", method, endpoint, err)
	}
	defer httpResponse.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(httpResponse.Body, (2<<20)+1))
	if err != nil {
		t.Fatalf("read %s response: %v", endpoint, err)
	}
	if httpResponse.StatusCode != wantStatus {
		httpError := &httpv1.HttpError{}
		_ = proto.Unmarshal(responseBody, httpError)
		t.Fatalf("%s %s status=%d want=%d error=%s", method, endpoint, httpResponse.StatusCode, wantStatus, httpError.GetCode())
	}
	if response != nil {
		if err := proto.Unmarshal(responseBody, response); err != nil {
			t.Fatalf("decode %s response: %v", endpoint, err)
		}
	}
}

func downloadConfig(t *testing.T, client *http.Client, endpoint string) []byte {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("create config request: %v", err)
	}
	request.Header.Set("Accept", protobufMediaType)
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("download client config: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("download client config status=%d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil {
		t.Fatalf("read client config: %v", err)
	}
	if len(body) > 2<<20 {
		t.Fatal("client config exceeds 2 MiB")
	}
	return body
}

func dialWebSocket(t *testing.T, endpoint string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{h5Origin}},
	})
	if err != nil {
		t.Fatalf("dial %s: %v", endpoint, err)
	}
	return conn
}

func writeEnvelope(t *testing.T, conn *websocket.Conn, envelope *wsv1.WsEnvelope) {
	t.Helper()
	body, err := proto.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal WS envelope: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageBinary, body); err != nil {
		t.Fatalf("write WS envelope: %v", err)
	}
}

func readEnvelope(t *testing.T, conn *websocket.Conn) *wsv1.WsEnvelope {
	t.Helper()
	return readEnvelopeWithTimeout(t, conn, 3*time.Second)
}

func readEnvelopeWithTimeout(t *testing.T, conn *websocket.Conn, timeout time.Duration) *wsv1.WsEnvelope {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	messageType, body, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read WS envelope: %v", err)
	}
	if messageType != websocket.MessageBinary {
		t.Fatalf("WS message type = %v, want binary", messageType)
	}
	envelope := &wsv1.WsEnvelope{}
	if err := proto.Unmarshal(body, envelope); err != nil {
		t.Fatalf("decode WS envelope: %v", err)
	}
	return envelope
}

func assertAuthMatchesBootstrap(t *testing.T, auth *wsv1.AuthResponse, bootstrap *httpv1.AuthBootstrap) {
	t.Helper()
	if auth == nil ||
		auth.GetPlayerId() != bootstrap.GetPlayerId() ||
		auth.GetHeartbeatIntervalMs() != bootstrap.GetHeartbeatIntervalMs() ||
		auth.GetClientConfigVersion() != bootstrap.GetClientConfigVersion() ||
		auth.GetClientConfigUrl() != bootstrap.GetClientConfigUrl() ||
		!bytes.Equal(auth.GetClientConfigSha256(), bootstrap.GetClientConfigSha256()) ||
		auth.GetProtocolMin() != bootstrap.GetProtocolMin() ||
		auth.GetProtocolMax() != bootstrap.GetProtocolMax() {
		t.Fatalf("AUTH seven fields mismatch: auth=%+v bootstrap=%+v", auth, bootstrap)
	}
}

func cookieValue(cookies []*http.Cookie, name string) string {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func uniqueAccountName(t *testing.T) string {
	t.Helper()
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("generate account suffix: %v", err)
	}
	return "e2e_" + hex.EncodeToString(suffix[:])
}

func newUUID(t *testing.T) string {
	t.Helper()
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatalf("generate UUID: %v", err)
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envUint64(t *testing.T, key string, fallback uint64) uint64 {
	t.Helper()
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		t.Fatalf("parse %s: %v", key, err)
	}
	return parsed
}

func inventoryQuantity(snapshot *wsv1.PlayerSnapshot, itemID uint32) uint32 {
	for _, item := range snapshot.GetInventory() {
		if item.GetItemId() == itemID {
			return item.GetQuantity()
		}
	}
	return 0
}
