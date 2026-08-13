package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	coordinatorv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/coordinator"
	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	"github.com/Wriosley/supernova-classic-farm/server/internal/connection"
	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinatorclient"
	"github.com/Wriosley/supernova-classic-farm/server/internal/farmview"
	"github.com/Wriosley/supernova-classic-farm/server/internal/interaction"
	"github.com/Wriosley/supernova-classic-farm/server/internal/outbox"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/database"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/health"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/internalnet"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/logging"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/shutdown"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/tcaplusdb"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"github.com/Wriosley/supernova-classic-farm/server/internal/push"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"github.com/Wriosley/supernova-classic-farm/server/internal/visit"
	"github.com/Wriosley/supernova-classic-farm/server/internal/zoneidentity"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

const interactionReconcileInterval = 5 * time.Second

const (
	defaultListenAddress = "127.0.0.1:8082"
	dualRoutingMode      = "static-dual-zone"
)

func main() {
	readiness := newReadinessState()
	routingMode := environmentOr("ROUTING_MODE", "local")
	if routingMode != "local" && routingMode != dualRoutingMode {
		log.Fatalf("unsupported ROUTING_MODE %q", routingMode)
	}
	ownerZoneID := routing.DefaultZoneID
	if routingMode == dualRoutingMode {
		ownerZoneID = environmentOr("OWNER_ZONE_ID", "zone-a")
	}
	listenAddress := environmentOr("ZONE_HTTP_ADDRESS", defaultListenAddress)
	if err := internalnet.RequireListenAddress(listenAddress, "ZoneSvr"); err != nil {
		log.Fatal(err)
	}
	identity, err := zoneidentity.New(zoneIdentityConfig(routingMode, ownerZoneID, listenAddress))
	if err != nil {
		log.Fatalf("create Zone identity: %v", err)
	}
	ownerZoneID = identity.LogicalZoneID
	dsn := strings.TrimSpace(os.Getenv("MYSQL_DSN"))
	logger, err := logging.New("zone-"+ownerZoneID, "development", "info")
	if err != nil {
		log.Fatal(err)
	}
	rpcKey, err := rpcauth.LoadKeyFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	gatewayID := environmentOr("GATEWAY_ID", "local-gateway")

	var runtime *player.Runtime
	// interactionStore backs the Phase 5 friend-interaction Saga
	// (STEAL_FRIEND_CROP): a durable Tcaplus-backed store when this Zone
	// already opened Tcaplus for its own checkpoint storage, or an
	// in-memory store for development/MySQL modes so the Zone still starts
	// without requiring Tcaplus solely for friend interactions.
	var interactionStore interaction.Store
	// interactionScanner is interactionStore's underlying concrete type,
	// kept separately because Traverse is not part of the interaction.Store
	// interface: both *interaction.TcaplusStore's client and
	// *interaction.MemoryStore support it, letting the Reconciler's ticker
	// run in every storage mode.
	var interactionScanner interface {
		Traverse(proto.Message) ([]proto.Message, error)
	}
	var tcaplusClient *tcaplusdb.Client
	var tcaplusZoneID uint32
	storageMode := strings.TrimSpace(os.Getenv("STORAGE_MODE"))
	if storageMode == "tcaplus" {
		if dsn != "" {
			log.Fatal("pure Tcaplus mode forbids MYSQL_DSN")
		}
		config, configErr := tcaplusdb.LoadConfigFromEnv()
		if configErr != nil {
			log.Fatal(configErr)
		}
		checkpointTable, configErr := tcaplusdb.TableName(
			"TCAPLUS_CHECKPOINT_TABLE", "PlayerCheckpoint",
		)
		if configErr != nil {
			log.Fatal(configErr)
		}
		fenceTable, configErr := tcaplusdb.TableName(
			"TCAPLUS_FENCE_TABLE", "ShardFence",
		)
		if configErr != nil {
			log.Fatal(configErr)
		}
		outboxTable, configErr := tcaplusdb.TableName(
			"TCAPLUS_OUTBOX_TABLE", "PlayerOutbox",
		)
		if configErr != nil {
			log.Fatal(configErr)
		}
		friendInteractionTable, configErr := tcaplusdb.TableName(
			"TCAPLUS_FRIEND_INTERACTION_TABLE", "FriendInteraction",
		)
		if configErr != nil {
			log.Fatal(configErr)
		}
		accountTable, configErr := tcaplusdb.TableName(
			"TCAPLUS_ACCOUNT_BY_PLAYER_TABLE", "AccountByPlayer",
		)
		if configErr != nil {
			log.Fatal(configErr)
		}
		client, openErr := tcaplusdb.Open(
			config, checkpointTable, fenceTable, outboxTable,
			friendInteractionTable, accountTable,
		)
		if openErr != nil {
			log.Fatal(openErr)
		}
		defer client.Close()
		tcaplusClient = client
		tcaplusZoneID = config.ZoneID
		checkpoints, storeErr := player.NewTcaplusCheckpointStoreWithClient(
			client, config.ZoneID,
		)
		if storeErr != nil {
			log.Fatal(storeErr)
		}
		durable, storeErr := player.NewTcaplusDurableCheckpointStore(
			checkpoints, client, config.ZoneID, ownerZoneID,
		)
		if storeErr != nil {
			log.Fatal(storeErr)
		}
		runtime, err = player.NewRuntimeWithStore(durable)
		if err != nil {
			log.Fatal(err)
		}
		interactionStore, err = interaction.NewTcaplusStore(client, config.ZoneID)
		if err != nil {
			log.Fatal(err)
		}
		interactionScanner = client
		runtime.SetAccountNamer(accountNameLookup{client: client, zoneID: config.ZoneID})
		logger.Info("using pure Tcaplus Player checkpoint store")
	} else if dsn == "" {
		runtime = player.NewRuntime()
		memoryInteractions := interaction.NewMemoryStore()
		interactionStore, interactionScanner = memoryInteractions, memoryInteractions
		logger.Warn("using development-only lazy in-memory player state")
	} else {
		db, openErr := database.OpenMySQL(context.Background(), dsn)
		if openErr != nil {
			log.Fatal(openErr)
		}
		defer db.Close()
		runtime, err = player.NewRuntimeWithStore(&player.MySQLCheckpointStore{
			DB: db, OwnerZoneID: ownerZoneID,
		})
		if err != nil {
			log.Fatal(err)
		}
		memoryInteractions := interaction.NewMemoryStore()
		interactionStore, interactionScanner = memoryInteractions, memoryInteractions
		logger.Info("using MySQL Player checkpoint store")
	}
	var pushForwarder *player.GRPCPushForwarder
	defer func() {
		runtime.Close()
		if pushForwarder != nil {
			_ = pushForwarder.Close()
		}
	}()

	ctx, cancel := shutdown.SignalContext(context.Background())
	defer cancel()
	go func() {
		<-ctx.Done()
		readiness.SetNotReady("shutdown")
	}()
	gates := &shardExecutionGates{}
	var authorization ownerAuthorization = localAuthorization{}
	var lifecycle *lifecycleHandler
	coordinatorURL := environmentOr("COORDINATOR_URL", "http://127.0.0.1:8083")
	if routingMode == dualRoutingMode {
		table, tableErr := routing.NewAuthorizationTable(ownerZoneID)
		if tableErr != nil {
			log.Fatal(tableErr)
		}
		authorization = table
		client := &http.Client{Timeout: 5 * time.Second}
		switch routeSource := environmentOr("ZONE_ROUTE_SOURCE", "http-poll"); routeSource {
		case "http-poll":
			if err := refreshAuthorization(ctx, table, client, coordinatorURL); err != nil {
				log.Fatalf("load initial ownership snapshot: %v", err)
			}
			lifecycle = &lifecycleHandler{runtime: runtime, authorization: table, gates: gates, now: time.Now, refresh: func() error {
				refreshCtx, refreshCancel := context.WithTimeout(ctx, 3*time.Second)
				defer refreshCancel()
				return refreshAuthorization(refreshCtx, table, client, coordinatorURL)
			}}
			go refreshAuthorizationLoop(ctx, table, client, coordinatorURL, logger)
		case "coordinator-sdk":
			sdk, sdkErr := coordinatorclient.New(coordinatorclient.Config{Endpoint: environmentOr("COORDINATOR_RPC_URL", coordinatorURL), SubscriberID: identity.IncarnationID, Kind: coordinatorv1.SubscriberKind_SUBSCRIBER_KIND_ZONE, HMACKey: rpcKey, OnSnapshot: table.Replace})
			if sdkErr != nil {
				log.Fatal(sdkErr)
			}
			if sdkErr = sdk.Start(ctx); sdkErr != nil {
				log.Fatalf("start Coordinator SDK: %v", sdkErr)
			}
			defer sdk.Close()
			lifecycle = &lifecycleHandler{runtime: runtime, authorization: table, gates: gates, now: time.Now, refresh: func() error { sdk.ForceResync(); return nil }}
			interval, intervalErr := time.ParseDuration(environmentOr("ZONE_ROUTE_VERIFY_INTERVAL", "5m"))
			if intervalErr != nil || interval <= 0 {
				log.Fatal("invalid ZONE_ROUTE_VERIFY_INTERVAL")
			}
			go verifySDKRoutesLoop(ctx, sdk, client, coordinatorURL, interval, logger)
		default:
			log.Fatalf("unsupported ZONE_ROUTE_SOURCE %q", routeSource)
		}
	}

	pushEndpoint := os.Getenv("GATE_RPC_URL")
	if pushEndpoint == "" {
		pushEndpoint = "http://127.0.0.1:8081"
	}
	pushForwarder, err = player.NewGRPCPushForwarder(
		rpcKey, rpcauth.ZoneService, pushEndpoint, gatewayID,
	)
	if err != nil {
		log.Fatal(err)
	}
	if err := runtime.SetPushForwarder(pushForwarder); err != nil {
		log.Fatal(err)
	}

	friendURL := environmentOr("FRIEND_RPC_URL", "http://127.0.0.1:8085")
	friendClient, err := visit.NewFriendRPCClient(rpcKey, rpcauth.ZoneService, friendURL)
	if err != nil {
		log.Fatal(err)
	}
	ownerFarmClient, err := visit.NewZoneOwnerFarmClient(rpcKey, rpcauth.ZoneService, coordinatorURL, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		_ = friendClient.Close()
		_ = ownerFarmClient.Close()
	}()
	visitorService, err := visit.NewService(friendClient, ownerFarmClient, time.Now)
	if err != nil {
		log.Fatal(err)
	}
	ownerFarmService, err := visit.NewOwnerService(runtime, pushForwarder, time.Now)
	if err != nil {
		log.Fatal(err)
	}
	go ownerFarmService.RunEvictionLoop(ctx)

	connectionRegistry := connection.NewRegistry()
	go runConnectionEvictionLoop(ctx, connectionRegistry, logger)

	farmViewBroadcaster, err := farmview.NewBroadcaster(pushForwarder, ownerFarmService, connectionRegistry)
	if err != nil {
		log.Fatal(err)
	}
	farmViewDispatcher, err := farmview.NewDispatcher(farmViewBroadcaster, logger)
	if err != nil {
		log.Fatal(err)
	}
	defer farmViewDispatcher.Close()
	if err := runtime.SetFarmViewDispatcher(farmViewDispatcher); err != nil {
		log.Fatal(err)
	}
	pushDispatcher, err := push.NewDispatcher(
		push.StaticGateResolver{GateID: gatewayID, Client: pushForwarder},
		connectionRegistry,
		logger,
		push.Config{},
	)
	if err != nil {
		log.Fatal(err)
	}
	defer pushDispatcher.Close()

	infoNotifier, infoErr := newInfoStealableNotifier(rpcKey, environmentOr("INFO_RPC_URL", "http://127.0.0.1:8086"), logger)
	if infoErr != nil {
		log.Fatal(infoErr)
	}
	defer infoNotifier.Close()
	runtime.SetStealableNotifier(infoNotifier)

	// Phase 5: the STEAL_FRIEND_CROP interaction Saga. ownerFarmClient
	// already implements interaction.OwnerFarmClient (its
	// ApplyVisitorAction resolves the owner's route itself), and *Runtime
	// already implements interaction.VisitorSteps, so both wire in
	// directly with no adapter.
	stealSaga, err := interaction.NewStealSaga(interactionStore, runtime, ownerFarmClient)
	if err != nil {
		log.Fatal(err)
	}
	actionSaga, err := interaction.NewActionSaga(interactionStore, runtime, ownerFarmClient)
	if err != nil {
		log.Fatal(err)
	}
	stealResolver := newZoneStealResolver(runtime, authorization)
	interactionReconciler, err := interaction.NewReconciler(interactionStore, stealSaga, stealResolver, interactionScanner)
	if err != nil {
		log.Fatal(err)
	}
	interactionReconciler.WithActionSaga(actionSaga)
	go runInteractionReconcileLoop(ctx, interactionReconciler, logger)

	if tcaplusClient != nil {
		mailURL := strings.TrimSpace(os.Getenv("MAIL_RPC_URL"))
		if mailURL == "" {
			logger.Warn("MAIL_RPC_URL unset; gift Outbox relay disabled")
		} else {
			mailClient, mailErr := outbox.NewMailClient(rpcKey, rpcauth.ZoneService, mailURL)
			if mailErr != nil {
				log.Fatal(mailErr)
			}
			defer func() { _ = mailClient.Close() }()
			outboxStore, storeErr := outbox.NewTcaplusStore(tcaplusClient, tcaplusZoneID)
			if storeErr != nil {
				log.Fatal(storeErr)
			}
			relay, relayErr := outbox.NewRelay(
				outboxStore,
				mailClient,
				authorizationShardOwner{auth: authorization, zoneID: ownerZoneID},
				time.Now,
				logger,
			)
			if relayErr != nil {
				log.Fatal(relayErr)
			}
			go relay.Run(ctx)
			logger.Info("gift Outbox relay started", "mail_rpc_url", mailURL)
		}
	}

	mux := http.NewServeMux()
	if lifecycle != nil {
		lifecycle.connections = connectionRegistry
		mux.HandleFunc("POST /internal/v1/shards/{shard_id}/drain", lifecycle.drain)
		mux.HandleFunc("POST /internal/v1/shards/{shard_id}/drain-complete", lifecycle.completeDrain)
		mux.HandleFunc("POST /internal/v1/shards/{shard_id}/prepare", lifecycle.prepareMigration)
		mux.HandleFunc("POST /internal/v1/shards/{shard_id}/resume", lifecycle.resume)
		mux.HandleFunc("POST /internal/v1/ownership/refresh", lifecycle.refreshOwnership)
	}
	healthHandler := health.NewHandler(health.Check{Name: "startup", Run: readiness.Check})
	mux.Handle("GET /livez", healthHandler)
	mux.Handle("GET /readyz", healthHandler)
	mux.Handle("GET /internal/v1/zone-identity", newZoneIdentityHandler(identity))
	rpcInterceptor, err := rpcauth.NewServerUnaryInterceptor(rpcauth.ServerConfig{
		Key: rpcKey,
		AllowedCallers: map[string][]string{
			rpcv1.GameCommandService_ExecutePlayerCommand_FullMethodName:            {"gate"},
			rpcv1.PlayerSocialService_ApplyFriendTaskCredit_FullMethodName:          {"friend"},
			rpcv1.PlayerSocialService_ApplyMailReward_FullMethodName:                {"mail"},
			rpcv1.VisitorZoneService_EnterFriendFarm_FullMethodName:                 {"gate"},
			rpcv1.VisitorZoneService_HeartbeatFriendFarm_FullMethodName:             {"gate"},
			rpcv1.VisitorZoneService_ExitFriendFarm_FullMethodName:                  {"gate"},
			rpcv1.OwnerFarmService_EnterVisitor_FullMethodName:                      rpcauth.ZoneAllowedCallers(true),
			rpcv1.OwnerFarmService_RefreshVisitorHeartbeat_FullMethodName:           rpcauth.ZoneAllowedCallers(true),
			rpcv1.OwnerFarmService_ExitVisitor_FullMethodName:                       rpcauth.ZoneAllowedCallers(true),
			rpcv1.OwnerFarmService_GetPublicFarmSnapshot_FullMethodName:             rpcauth.ZoneAllowedCallers(true),
			rpcv1.VisitorZoneService_ExecuteFriendAction_FullMethodName:             {"gate"},
			rpcv1.OwnerFarmService_ApplyVisitorAction_FullMethodName:                rpcauth.ZoneAllowedCallers(true),
			rpcv1.PlayerConnectionService_RegisterPlayerConnection_FullMethodName:   {"gate"},
			rpcv1.PlayerConnectionService_RefreshPlayerConnection_FullMethodName:    {"gate"},
			rpcv1.PlayerConnectionService_UnregisterPlayerConnection_FullMethodName: {"gate"},
			rpcv1.ZoneNotificationService_DispatchRedDot_FullMethodName:             {"info"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(rpcInterceptor),
		grpc.MaxRecvMsgSize(128<<10),
		grpc.MaxSendMsgSize(128<<10),
	)
	defer grpcServer.Stop()
	rpcv1.RegisterGameCommandServiceServer(
		grpcServer,
		newGameCommandRPCServer(
			runtime, authorization, gates, time.Now, gatewayID, logger,
		),
	)
	rpcv1.RegisterPlayerSocialServiceServer(
		grpcServer,
		newPlayerSocialRPCServer(runtime, authorization, gates, time.Now),
	)
	visitorZoneServer := newVisitorZoneRPCServer(visitorService, authorization, ownerZoneID)
	visitorZoneServer.withFriendSagas(runtime, stealSaga, actionSaga)
	rpcv1.RegisterVisitorZoneServiceServer(grpcServer, visitorZoneServer)
	ownerFarmServer := newOwnerFarmRPCServer(ownerFarmService, authorization, gates, time.Now)
	ownerFarmServer.withRuntime(runtime)
	ownerFarmServer.enableFriendActions()
	rpcv1.RegisterOwnerFarmServiceServer(grpcServer, ownerFarmServer)
	rpcv1.RegisterPlayerConnectionServiceServer(
		grpcServer,
		newPlayerConnectionRPCServer(connectionRegistry, authorization, gates, time.Now, gatewayID),
	)
	rpcv1.RegisterZoneNotificationServiceServer(
		grpcServer,
		newZoneNotificationRPCServer(pushDispatcher, authorization, gates, time.Now),
	)

	server := &http.Server{
		Addr:              listenAddress,
		Handler:           rpcnet.H2CHandler(grpcServer, mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	logger.Info("zone listening",
		"address", listenAddress,
		"owner_zone_id", ownerZoneID,
		"incarnation_id", identity.IncarnationID,
		"advertised_endpoint", identity.Endpoint,
		"owner_epoch", player.LocalOwnerEpoch,
		"routing_mode", routingMode,
		"gate_rpc_url", pushEndpoint,
		"state_adapter", func() string {
			if os.Getenv("MYSQL_DSN") == "" {
				return "lazy-in-memory-development-only"
			}
			return "mysql-checkpoint"
		}(),
	)
	readiness.SetReady()
	if err := shutdown.Serve(ctx, server, 5*time.Second, logger); err != nil {
		logger.Error("zone stopped", "error", err)
	}
}

func zoneIdentityConfig(routingMode, ownerZoneID, listenAddress string) zoneidentity.Config {
	statefulSetName := strings.TrimSpace(os.Getenv("ZONE_STATEFULSET_NAME"))
	endpoint := strings.TrimSpace(os.Getenv("ZONE_ADVERTISED_ENDPOINT"))
	if statefulSetName != "" {
		return zoneidentity.Config{
			ClusterID:       strings.TrimSpace(os.Getenv("CLUSTER_ID")),
			Namespace:       strings.TrimSpace(os.Getenv("POD_NAMESPACE")),
			StatefulSetName: statefulSetName,
			PodName:         strings.TrimSpace(os.Getenv("POD_NAME")),
			Endpoint:        endpoint,
		}
	}
	if endpoint == "" {
		if routingMode == dualRoutingMode {
			endpoint = "http://" + ownerZoneID + ":8082"
		} else {
			endpoint = "http://" + listenAddress
		}
	}
	return zoneidentity.Config{LogicalOverride: ownerZoneID, Endpoint: endpoint}
}

func refreshAuthorization(
	ctx context.Context,
	table *routing.AuthorizationTable,
	client *http.Client,
	coordinatorURL string,
) error {
	snapshot, err := routing.FetchSnapshot(ctx, client, coordinatorURL)
	if err != nil {
		return err
	}
	return table.Replace(snapshot)
}

func refreshAuthorizationLoop(
	ctx context.Context,
	table *routing.AuthorizationTable,
	client *http.Client,
	coordinatorURL string,
	logger interface {
		Error(string, ...any)
		Debug(string, ...any)
	},
) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := refreshAuthorization(refreshCtx, table, client, coordinatorURL)
			cancel()
			if err != nil {
				logger.Error("ownership snapshot refresh failed", "error", err)
				continue
			}
			logger.Debug("ownership snapshot refreshed")
		}
	}
}

type routeSnapshotClient interface {
	Snapshot() routing.Snapshot
	ForceResync()
}

func verifySDKRoutesLoop(ctx context.Context, sdk routeSnapshotClient, client *http.Client, coordinatorURL string, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			verifyCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			remote, err := routing.FetchSnapshot(verifyCtx, client, coordinatorURL)
			cancel()
			if err != nil {
				logger.Error("route SDK verification failed", "error", err)
				continue
			}
			if !sameDurableRoutes(sdk.Snapshot(), remote) {
				logger.Error("route SDK verification mismatch; resync requested", "sdk_map_version", sdk.Snapshot().MapVersion, "http_map_version", remote.MapVersion)
				sdk.ForceResync()
			}
		}
	}
}
func sameDurableRoutes(a, b routing.Snapshot) bool {
	if a.MapVersion != b.MapVersion || len(a.Entries) != len(b.Entries) {
		return false
	}
	for i := range a.Entries {
		x, y := a.Entries[i], b.Entries[i]
		x.LeaseExpiresAt = time.Time{}
		y.LeaseExpiresAt = time.Time{}
		if x != y {
			return false
		}
	}
	return true
}

// runInteractionReconcileLoop drives the Phase 5 friend-interaction
// Reconciler every interactionReconcileInterval, exactly like
// cmd/friend's runReconcileLoop drives the friend-link Saga's Reconciler.
func runInteractionReconcileLoop(ctx context.Context, reconciler *interaction.Reconciler, logger *slog.Logger) {
	ticker := time.NewTicker(interactionReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := reconciler.ReconcileDue(ctx, time.Now()); err != nil {
				logger.Error("friend interaction Saga reconcile failed", "error", err)
			}
		}
	}
}

func runConnectionEvictionLoop(ctx context.Context, registry *connection.Registry, logger *slog.Logger) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			removed := registry.EvictExpired(time.Now())
			if len(removed) > 0 {
				logger.Debug("evicted expired player connections", "count", len(removed))
			}
		}
	}
}

func environmentOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
