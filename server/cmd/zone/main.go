package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/database"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/health"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/internalnet"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/logging"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/shutdown"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/tcaplusdb"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/grpc"
)

const (
	defaultListenAddress = "127.0.0.1:8082"
	dualRoutingMode      = "static-dual-zone"
)

func main() {
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
		client, openErr := tcaplusdb.Open(
			config, checkpointTable, fenceTable, outboxTable,
		)
		if openErr != nil {
			log.Fatal(openErr)
		}
		defer client.Close()
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
		logger.Info("using pure Tcaplus Player checkpoint store")
	} else if dsn == "" {
		runtime = player.NewRuntime()
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
	gates := &shardExecutionGates{}
	var authorization ownerAuthorization = localAuthorization{}
	var lifecycle *lifecycleHandler
	if routingMode == dualRoutingMode {
		table, tableErr := routing.NewAuthorizationTable(ownerZoneID)
		if tableErr != nil {
			log.Fatal(tableErr)
		}
		coordinatorURL := environmentOr("COORDINATOR_URL", "http://127.0.0.1:8083")
		client := &http.Client{Timeout: 5 * time.Second}
		if err := refreshAuthorization(ctx, table, client, coordinatorURL); err != nil {
			log.Fatalf("load initial ownership snapshot: %v", err)
		}
		authorization = table
		lifecycle = &lifecycleHandler{
			runtime: runtime, authorization: table, gates: gates, now: time.Now,
			refresh: func() error {
				refreshCtx, refreshCancel := context.WithTimeout(ctx, 3*time.Second)
				defer refreshCancel()
				return refreshAuthorization(refreshCtx, table, client, coordinatorURL)
			},
		}
		go refreshAuthorizationLoop(ctx, table, client, coordinatorURL, logger)
	}

	pushEndpoint := os.Getenv("GATE_RPC_URL")
	if pushEndpoint == "" {
		pushEndpoint = "http://127.0.0.1:8081"
	}
	pushForwarder, err = player.NewGRPCPushForwarder(
		rpcKey, ownerZoneID, pushEndpoint, gatewayID,
	)
	if err != nil {
		log.Fatal(err)
	}
	if err := runtime.SetPushForwarder(pushForwarder); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	if lifecycle != nil {
		mux.HandleFunc("POST /internal/v1/shards/{shard_id}/drain", lifecycle.drain)
		mux.HandleFunc("POST /internal/v1/shards/{shard_id}/drain-complete", lifecycle.completeDrain)
		mux.HandleFunc("POST /internal/v1/shards/{shard_id}/prepare", lifecycle.prepareMigration)
		mux.HandleFunc("POST /internal/v1/shards/{shard_id}/resume", lifecycle.resume)
		mux.HandleFunc("POST /internal/v1/ownership/refresh", lifecycle.refreshOwnership)
	}
	healthHandler := health.NewHandler()
	mux.Handle("GET /livez", healthHandler)
	mux.Handle("GET /readyz", healthHandler)
	rpcInterceptor, err := rpcauth.NewServerUnaryInterceptor(rpcauth.ServerConfig{
		Key: rpcKey,
		AllowedCallers: map[string][]string{
			rpcv1.GameCommandService_ExecutePlayerCommand_FullMethodName:   {"gate"},
			rpcv1.PlayerSocialService_ApplyFriendTaskCredit_FullMethodName: {"friend"},
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
			runtime, authorization, gates, time.Now, gatewayID,
		),
	)
	rpcv1.RegisterPlayerSocialServiceServer(
		grpcServer,
		newPlayerSocialRPCServer(runtime, authorization, gates, time.Now),
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
	if err := shutdown.Serve(ctx, server, 5*time.Second, logger); err != nil {
		logger.Error("zone stopped", "error", err)
	}
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

func environmentOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
