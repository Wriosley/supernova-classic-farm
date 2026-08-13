package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	httpv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/http"
	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"github.com/coder/websocket"
	_ "github.com/go-sql-driver/mysql"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type dualPlayer struct {
	id       uint64
	zoneID   string
	route    routing.RouteEntry
	client   *http.Client
	csrf     string
	wsTicket string
}

func TestDualZoneRoutingAndCache(t *testing.T) {
	if os.Getenv("E2E_RUN") != "1" ||
		os.Getenv("E2E_DUAL_ZONE") != "1" ||
		os.Getenv("E2E_SUITE") != "dual-zone" {
		t.Skip("use tests/e2e/run-dual-zone-routing.ps1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	routeSnapshot, err := routing.FetchSnapshot(ctx, &http.Client{Timeout: 5 * time.Second},
		"http://127.0.0.1:8083")
	if err != nil {
		t.Fatal(err)
	}

	players := map[string]*dualPlayer{}
	for attempt := 0; attempt < 20 && len(players) < 2; attempt++ {
		player := registerDualPlayer(t, routeSnapshot)
		if _, exists := players[player.zoneID]; !exists {
			players[player.zoneID] = player
		}
	}
	playerA, okA := players["zone-a"]
	playerB, okB := players["zone-b"]
	if !okA || !okB {
		t.Fatalf("failed to register players in both Zones: %+v", players)
	}
	var migrationPlayer *dualPlayer
	for attempt := 0; attempt < 20 && migrationPlayer == nil; attempt++ {
		candidate := registerDualPlayer(t, routeSnapshot)
		if candidate.zoneID == "zone-a" &&
			candidate.route.ShardID != playerA.route.ShardID {
			migrationPlayer = candidate
		}
	}
	if migrationPlayer == nil {
		t.Fatal("failed to register an inactive Zone-A migration player")
	}

	before := routeLookupStats(t)
	assertRouteSubscribers(t, 4)
	connA := authenticateDualPlayer(t, playerA)
	defer connA.CloseNow()
	connB := authenticateDualPlayer(t, playerB)
	defer connB.CloseNow()
	assertInitialSnapshot(t, connA, playerA.id, "zone-a-snapshot")
	assertInitialSnapshot(t, connB, playerB.id, "zone-b-snapshot")

	writeEnvelope(t, connA, &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_BUY_SEEDS, RequestId: newUUID(t),
		TargetPlayerId: playerA.id,
		Payload: &wsv1.WsEnvelope_BuySeedsRequest{
			BuySeedsRequest: &wsv1.BuySeedsRequest{
				ShopEntryId: 5001, Quantity: 1, ExpectedPriceVersion: 8,
			},
		},
	})
	bought := readEnvelope(t, connA)
	if bought.GetError() != nil ||
		bought.GetStateVersion().GetPlayerSeq() != 1 ||
		bought.GetBuySeedsResponse().GetPatch().GetCoinBalance() != 8 {
		t.Fatalf("player A purchase failed: %+v", bought)
	}
	assertInitialSnapshot(t, connB, playerB.id, "zone-b-isolated")
	afterNormal := routeLookupStats(t)
	if afterNormal.Shard != before.Shard {
		t.Fatalf("normal SDK route hit unexpectedly used Coordinator single-Shard HTTP lookup: before=%+v after=%+v", before, afterNormal)
	}

	migrated := moveShard(t, migrationPlayer.route.ShardID, "zone-b", http.StatusOK)
	if migrated.OwnerZoneID != "zone-b" || migrated.OwnerEpoch != "2" ||
		migrated.State != "ACTIVE" {
		t.Fatalf("unexpected migrated route: %+v", migrated)
	}
	migrationConn := authenticateDualPlayer(t, migrationPlayer)
	defer migrationConn.CloseNow()
	assertSnapshotEpoch(
		t, migrationConn, migrationPlayer.id, "migrated-stale-cache", 2,
	)
	assertWrongZoneRejected(t, migrationPlayer)
	activeMoved := moveShard(t, playerA.route.ShardID, "zone-b", http.StatusOK)
	if activeMoved.OwnerZoneID != "zone-b" || activeMoved.OwnerEpoch != "2" ||
		activeMoved.State != "ACTIVE" {
		t.Fatalf("unexpected active-player migrated route: %+v", activeMoved)
	}
	assertSnapshotStateEventually(
		t, connA, playerA.id, "active-player-migrated", 2, 1, 8,
	)

	after := routeLookupStats(t)
	t.Logf("DUAL_ZONE zone_a_player=%d shard=%d zone_b_player=%d shard=%d migrated_player=%d migrated_shard=%d migrated_epoch=2 snapshot_lookups=%d shard_lookups=%d",
		playerA.id, playerA.route.ShardID, playerB.id, playerB.route.ShardID,
		migrationPlayer.id, migrationPlayer.route.ShardID,
		after.Snapshot, after.Shard)
}

func TestDualZoneMySQLRoutingAndPersistence(t *testing.T) {
	if os.Getenv("E2E_RUN") != "1" ||
		os.Getenv("E2E_DUAL_ZONE") != "1" ||
		os.Getenv("E2E_SUITE") != "dual-zone-mysql" {
		t.Skip("use tests/e2e/run-dual-zone-mysql.ps1")
	}
	dsn := strings.TrimSpace(os.Getenv("MYSQL_DSN"))
	if dsn == "" {
		t.Fatal("MYSQL_DSN is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	routeSnapshot, err := routing.FetchSnapshot(
		ctx, &http.Client{Timeout: 5 * time.Second},
		"http://127.0.0.1:8083",
	)
	if err != nil {
		t.Fatal(err)
	}
	players := map[string]*dualPlayer{}
	for attempt := 0; attempt < 30 && len(players) < 2; attempt++ {
		player := registerDualPlayer(t, routeSnapshot)
		if _, exists := players[player.zoneID]; !exists {
			players[player.zoneID] = player
		}
	}
	playerA, okA := players["zone-a"]
	playerB, okB := players["zone-b"]
	if !okA || !okB {
		t.Fatalf("failed to register MySQL players in both Zones: %+v", players)
	}

	connA := authenticateDualPlayer(t, playerA)
	defer connA.CloseNow()
	connB := authenticateDualPlayer(t, playerB)
	defer connB.CloseNow()
	assertInitialSnapshot(t, connA, playerA.id, "mysql-zone-a-snapshot")
	assertInitialSnapshot(t, connB, playerB.id, "mysql-zone-b-snapshot")
	buyOneSeed(t, connA, playerA.id)
	buyOneSeed(t, connB, playerB.id)
	assertWrongZoneRejected(t, playerA)
	assertWrongZoneRejected(t, playerB)
	moved := moveShard(t, playerA.route.ShardID, "zone-b", http.StatusOK)
	if moved.OwnerZoneID != "zone-b" || moved.OwnerEpoch != "2" {
		t.Fatalf("unexpected MySQL migrated route: %+v", moved)
	}
	assertDirectZoneRejected(t, playerA, "http://127.0.0.1:8082")
	assertSnapshotState(
		t, connA, playerA.id, "mysql-migrated-snapshot", 2, 1, 8,
	)
	buyOneSeedAtVersion(t, connA, playerA.id, 2, 2, 6)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	waitForDualMySQLCheckpoint(t, db, playerA, "zone-b", 2, 2)
	waitForDualMySQLCheckpoint(t, db, playerB, "zone-b", 1, 1)
	assertStaleZoneWriterFenced(t, db, playerA.id)
	assertMigrationInspectEmpty(t, playerA.route.ShardID)
	t.Logf(
		"DUAL_ZONE_MYSQL zone_a_player=%d shard=%d migrated_zone=zone-b migrated_epoch=2 persisted_seq=2 zone_b_player=%d shard=%d persisted_seq=1",
		playerA.id, playerA.route.ShardID, playerB.id, playerB.route.ShardID,
	)
}

func TestDualZoneMySQLCoordinatorHydrateAfterMigration(t *testing.T) {
	if os.Getenv("E2E_RUN") != "1" ||
		os.Getenv("E2E_DUAL_ZONE") != "1" ||
		os.Getenv("E2E_SUITE") != "dual-zone-mysql-hydrate" {
		t.Skip("use tests/e2e/run-dual-zone-mysql.ps1 after Coordinator restart")
	}
	dsn := strings.TrimSpace(os.Getenv("MYSQL_DSN"))
	if dsn == "" {
		t.Fatal("MYSQL_DSN is required")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fences, err := routing.LoadMySQLFences(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	routeSnapshot, err := routing.FetchSnapshot(
		ctx, &http.Client{Timeout: 5 * time.Second},
		"http://127.0.0.1:8083",
	)
	if err != nil {
		t.Fatal(err)
	}
	advanced := 0
	for _, fence := range fences {
		if fence.OwnerEpoch < 2 {
			continue
		}
		advanced++
		entry := routeSnapshot.Entries[fence.ShardID]
		if entry.State != routing.RouteStateActive ||
			entry.OwnerZoneID != fence.OwnerZoneID ||
			entry.OwnerEpoch != fence.OwnerEpoch ||
			entry.RouteVersion != fence.RouteVersion {
			t.Fatalf(
				"hydrated route mismatch for shard %d: fence=%+v route=%+v",
				fence.ShardID, fence, entry,
			)
		}
	}
	if advanced == 0 {
		t.Fatal("expected at least one advanced fence after prior migration")
	}
	assertOpenMigrationsEmpty(t)
	t.Logf(
		"DUAL_ZONE_MYSQL_HYDRATE advanced_fences=%d map_version=%d",
		advanced, routeSnapshot.MapVersion,
	)
}

func assertStaleZoneWriterFenced(t *testing.T, db *sql.DB, playerID uint64) {
	t.Helper()
	store := &player.MySQLCheckpointStore{DB: db, OwnerZoneID: "zone-a"}
	loaded, err := store.Load(context.Background(), playerID)
	if err != nil {
		t.Fatal(err)
	}
	state := loaded.State
	expectedRevision := state.CheckpointRevision
	state.OwnerEpoch = 1
	state.CheckpointRevision++
	state.UpdatedAtMS++
	checkpoint, err := state.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(
		context.Background(), checkpoint, expectedRevision,
	); !errors.Is(err, player.ErrCheckpointFenced) {
		t.Fatalf("stale Zone-A writer error = %v, want ErrCheckpointFenced", err)
	}
}

func buyOneSeed(t *testing.T, conn *websocket.Conn, playerID uint64) {
	t.Helper()
	buyOneSeedAtVersion(t, conn, playerID, 1, 1, 8)
}

func buyOneSeedAtVersion(
	t *testing.T,
	conn *websocket.Conn,
	playerID uint64,
	ownerEpoch uint64,
	playerSeq uint64,
	coinBalance int64,
) {
	t.Helper()
	writeEnvelope(t, conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_BUY_SEEDS, RequestId: newUUID(t),
		TargetPlayerId: playerID,
		Payload: &wsv1.WsEnvelope_BuySeedsRequest{
			BuySeedsRequest: &wsv1.BuySeedsRequest{
				ShopEntryId: 5001, Quantity: 1, ExpectedPriceVersion: 8,
			},
		},
	})
	response := readEnvelope(t, conn)
	if response.GetError() != nil ||
		response.GetStateVersion().GetOwnerEpoch() != ownerEpoch ||
		response.GetStateVersion().GetPlayerSeq() != playerSeq ||
		response.GetBuySeedsResponse().GetPatch().GetCoinBalance() != coinBalance {
		t.Fatalf("player %d purchase failed: %+v", playerID, response)
	}
}

func waitForDualMySQLCheckpoint(
	t *testing.T,
	db *sql.DB,
	player *dualPlayer,
	expectedFenceOwner string,
	expectedOwnerEpoch uint64,
	expectedPlayerSeq uint64,
) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	var lastError error
	for time.Now().Before(deadline) {
		var checkpointEpoch uint64
		var storedPlayerSeq uint64
		var storedFenceOwner string
		var fenceEpoch uint64
		err := db.QueryRow(`
			SELECT p.owner_epoch, p.player_seq, f.owner_zone_id, f.owner_epoch
			FROM player_checkpoints p
			JOIN shard_fences f ON f.logical_shard_id = p.logical_shard_id
			WHERE p.player_id = ?`,
			player.id,
		).Scan(
			&checkpointEpoch, &storedPlayerSeq, &storedFenceOwner, &fenceEpoch,
		)
		if err == nil &&
			checkpointEpoch == expectedOwnerEpoch &&
			storedPlayerSeq == expectedPlayerSeq &&
			storedFenceOwner == expectedFenceOwner &&
			fenceEpoch == expectedOwnerEpoch {
			return
		}
		if err != nil {
			lastError = err
		} else {
			lastError = fmt.Errorf(
				"checkpoint_epoch=%d player_seq=%d fence_owner=%s fence_epoch=%d",
				checkpointEpoch, storedPlayerSeq, storedFenceOwner, fenceEpoch,
			)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("player %d checkpoint was not persisted: %v", player.id, lastError)
}

func registerDualPlayer(t *testing.T, snapshot routing.Snapshot) *dualPlayer {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	loginURL := envOr("E2E_LOGIN_URL", "http://127.0.0.1:8080")
	csrf := getCSRF(t, client, loginURL)
	register := &httpv1.RegisterResponse{}
	doProto(t, client, http.MethodPost, loginURL+"/v1/auth/register",
		&httpv1.RegisterRequest{
			AccountName: uniqueAccountName(t), Password: e2ePassword,
		}, csrf, http.StatusCreated, register)
	playerID := register.GetSession().GetPlayerId()
	authenticatedCSRF := getCSRF(t, client, loginURL)
	ticket := &httpv1.WsTicketResponse{}
	doProto(t, client, http.MethodPost, loginURL+"/v1/ws-tickets",
		&httpv1.WsTicketRequest{
			TicketRequestId: newUUID(t), GatewayId: "local-gateway",
		}, authenticatedCSRF, http.StatusCreated, ticket)
	shardID := routing.ShardForPlayer(playerID)
	route := snapshot.Entries[shardID]
	return &dualPlayer{
		id: playerID, zoneID: route.OwnerZoneID, route: route,
		client: client, csrf: authenticatedCSRF, wsTicket: ticket.GetWsTicket(),
	}
}

func authenticateDualPlayer(t *testing.T, player *dualPlayer) *websocket.Conn {
	t.Helper()
	conn := dialWebSocket(t, "ws://127.0.0.1:8081/ws")
	writeEnvelope(t, conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_AUTH, RequestId: newUUID(t),
		Payload: &wsv1.WsEnvelope_AuthRequest{
			AuthRequest: &wsv1.AuthRequest{WsTicket: player.wsTicket},
		},
	})
	response := readEnvelope(t, conn)
	if response.GetError() != nil ||
		response.GetAuthResponse().GetPlayerId() != player.id {
		t.Fatalf("AUTH failed for player %d: %+v", player.id, response)
	}
	return conn
}

func assertInitialSnapshot(
	t *testing.T,
	conn *websocket.Conn,
	playerID uint64,
	requestID string,
) {
	t.Helper()
	assertSnapshotEpoch(t, conn, playerID, requestID, 1)
}

func assertSnapshotEpoch(
	t *testing.T,
	conn *websocket.Conn,
	playerID uint64,
	requestID string,
	ownerEpoch uint64,
) {
	t.Helper()
	assertSnapshotState(t, conn, playerID, requestID, ownerEpoch, 0, 10)
}

func assertSnapshotState(
	t *testing.T,
	conn *websocket.Conn,
	playerID uint64,
	requestID string,
	ownerEpoch uint64,
	playerSeq uint64,
	coinBalance int64,
) {
	t.Helper()
	writeEnvelope(t, conn, &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_GET_PLAYER_SNAPSHOT, RequestId: requestID,
		TargetPlayerId: playerID,
		Payload: &wsv1.WsEnvelope_GetPlayerSnapshotRequest{
			GetPlayerSnapshotRequest: &wsv1.GetPlayerSnapshotRequest{},
		},
	})
	response := readEnvelope(t, conn)
	snapshot := response.GetGetPlayerSnapshotResponse().GetSnapshot()
	if response.GetError() != nil ||
		response.GetStateVersion().GetOwnerEpoch() != ownerEpoch ||
		response.GetStateVersion().GetPlayerSeq() != playerSeq ||
		snapshot.GetPlayerId() != playerID ||
		snapshot.GetCoinBalance() != coinBalance {
		t.Fatalf("initial snapshot failed for player %d: %+v", playerID, response)
	}
}

func assertSnapshotStateEventually(
	t *testing.T,
	conn *websocket.Conn,
	playerID uint64,
	requestID string,
	ownerEpoch uint64,
	playerSeq uint64,
	coinBalance int64,
) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for attempt := 1; ; attempt++ {
		writeEnvelope(t, conn, &wsv1.WsEnvelope{
			ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
			Action:         wsv1.Action_GET_PLAYER_SNAPSHOT,
			RequestId:      requestID + "-" + strconv.Itoa(attempt),
			TargetPlayerId: playerID,
			Payload: &wsv1.WsEnvelope_GetPlayerSnapshotRequest{
				GetPlayerSnapshotRequest: &wsv1.GetPlayerSnapshotRequest{},
			},
		})
		response := readEnvelope(t, conn)
		snapshot := response.GetGetPlayerSnapshotResponse().GetSnapshot()
		if response.GetError() == nil &&
			response.GetStateVersion().GetOwnerEpoch() == ownerEpoch &&
			response.GetStateVersion().GetPlayerSeq() == playerSeq &&
			snapshot.GetPlayerId() == playerID &&
			snapshot.GetCoinBalance() == coinBalance {
			t.Logf("active route delivered after %d attempt(s)", attempt)
			return
		}
		if response.GetError().GetCode() != wsv1.ErrorCode_SERVICE_UNAVAILABLE || time.Now().After(deadline) {
			t.Fatalf("active route did not arrive for player %d: %+v", playerID, response)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type movedRoute struct {
	OwnerZoneID string `json:"owner_zone_id"`
	OwnerEpoch  string `json:"owner_epoch"`
	State       string `json:"state"`
}

func assertMigrationInspectEmpty(t *testing.T, shardID uint32) {
	t.Helper()
	response, err := http.Get(
		"http://127.0.0.1:8083/internal/v1/shards/" +
			strconv.FormatUint(uint64(shardID), 10) + "/migration",
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("migration inspect status=%d body=%s", response.StatusCode, body)
	}
	var payload struct {
		Status string `json:"status"`
		Step   string `json:"step"`
		Route  struct {
			State string `json:"state"`
		} `json:"route"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "" || payload.Step != "" ||
		payload.Route.State != "ACTIVE" {
		t.Fatalf("expected empty completed migration inspect, got %s", body)
	}
}

func assertOpenMigrationsEmpty(t *testing.T) {
	t.Helper()
	response, err := http.Get("http://127.0.0.1:8083/internal/v1/migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list migrations status=%d body=%s", response.StatusCode, body)
	}
	var payload struct {
		Migrations []json.RawMessage `json:"migrations"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Migrations) != 0 {
		t.Fatalf("expected no open migrations, got %s", body)
	}
}

func moveShard(
	t *testing.T,
	shardID uint32,
	targetZoneID string,
	wantStatus int,
) movedRoute {
	t.Helper()
	body := strings.NewReader(`{"target_zone_id":"` + targetZoneID + `"}`)
	request, err := http.NewRequest(http.MethodPost,
		"http://127.0.0.1:8083/internal/v1/shards/"+
			strconv.FormatUint(uint64(shardID), 10)+"/move",
		body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("move shard %d status=%d want=%d body=%s",
			shardID, response.StatusCode, wantStatus, responseBody)
	}
	var moved movedRoute
	if wantStatus == http.StatusOK {
		if err := json.Unmarshal(responseBody, &moved); err != nil {
			t.Fatal(err)
		}
	}
	return moved
}

func assertWrongZoneRejected(t *testing.T, player *dualPlayer) {
	t.Helper()
	wrongEndpoint := "http://127.0.0.1:8084"
	if player.zoneID == "zone-b" {
		wrongEndpoint = "http://127.0.0.1:8082"
	}
	assertDirectZoneRejected(t, player, wrongEndpoint)
}

func assertDirectZoneRejected(
	t *testing.T,
	player *dualPlayer,
	endpoint string,
) {
	t.Helper()
	envelope := &wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_GET_PLAYER_SNAPSHOT, RequestId: newUUID(t),
		TargetPlayerId: player.id,
		Payload: &wsv1.WsEnvelope_GetPlayerSnapshotRequest{
			GetPlayerSnapshotRequest: &wsv1.GetPlayerSnapshotRequest{},
		},
	}
	key, err := rpcauth.LoadKeyFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{
		Service: "gate", Key: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := grpc.NewClient(
		strings.TrimPrefix(endpoint, "http://"),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(interceptor),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = rpcv1.NewGameCommandServiceClient(conn).ExecutePlayerCommand(
		ctx,
		&rpcv1.ExecutePlayerCommandRequest{
			CallerPlayerId: player.id,
			GateId:         "local-gateway",
			Route: &rpcv1.CommittedRoute{
				LogicalShardId: player.route.ShardID,
				OwnerZoneId:    player.route.OwnerZoneID,
				OwnerEpoch:     player.route.OwnerEpoch,
				RouteVersion:   player.route.RouteVersion,
			},
			Envelope: envelope,
		},
	)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("wrong Zone gRPC status=%v error=%v", status.Code(err), err)
	}
}

type lookupStats struct {
	Snapshot uint64 `json:"snapshot"`
	Shard    uint64 `json:"shard"`
}

type subscriberStats struct {
	ActiveSubscribers       int    `json:"active_subscribers"`
	LastPublishedMapVersion uint64 `json:"last_published_map_version"`
}

func assertRouteSubscribers(t *testing.T, want int) {
	t.Helper()
	response, err := http.Get("http://127.0.0.1:8083/internal/v1/debug/route-subscribers")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("route subscriber diagnostics status=%d", response.StatusCode)
	}
	var stats subscriberStats
	if err := json.NewDecoder(response.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	if stats.ActiveSubscribers != want {
		t.Fatalf("route subscriber count=%d want=%d diagnostics=%+v", stats.ActiveSubscribers, want, stats)
	}
}

func routeLookupStats(t *testing.T) lookupStats {
	t.Helper()
	response, err := http.Get("http://127.0.0.1:8083/internal/v1/debug/route-lookups")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var stats lookupStats
	if err := json.NewDecoder(response.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	return stats
}
