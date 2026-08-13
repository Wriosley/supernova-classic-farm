package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	friendv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/friend"
	"github.com/Wriosley/supernova-classic-farm/server/internal/friend"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/config"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/health"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/internalnet"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/logging"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/shutdown"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/tcaplusdb"
	"google.golang.org/grpc"
)

const reconcileInterval = 5 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("friend service stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load("friend", "127.0.0.1:8085")
	if err != nil {
		return err
	}
	if err := internalnet.RequireListenAddress(cfg.HTTPAddress, "FriendSvr"); err != nil {
		return err
	}
	logger, err := logging.New(cfg.ServiceName, cfg.Environment, cfg.LogLevel)
	if err != nil {
		return err
	}
	// FriendSvr owns the durable friend graph (relations, share codes, list
	// projections); unlike Zone/Login it has no development-only in-memory
	// fallback because there is nothing correct to fall back to.
	if strings.TrimSpace(os.Getenv("STORAGE_MODE")) != "tcaplus" {
		return errors.New("FriendSvr requires STORAGE_MODE=tcaplus")
	}
	rpcKey, err := rpcauth.LoadKeyFromEnv()
	if err != nil {
		return err
	}

	tcaplusConfig, err := tcaplusdb.LoadConfigFromEnv()
	if err != nil {
		return err
	}
	tables, err := friendTableNames()
	if err != nil {
		return err
	}
	client, err := tcaplusdb.Open(tcaplusConfig, tables...)
	if err != nil {
		return err
	}
	defer client.Close()

	store, err := friend.NewTcaplusStore(client, tcaplusConfig.ZoneID)
	if err != nil {
		return err
	}

	coordinatorURL := envOr("COORDINATOR_URL", "http://127.0.0.1:8083")
	taskCreditor, err := friend.NewZoneTaskCreditClient(rpcKey, coordinatorURL, nil)
	if err != nil {
		return err
	}
	defer func() { _ = taskCreditor.Close() }()

	mailURL := envOr("MAIL_RPC_URL", "http://127.0.0.1:8087")
	mailer, err := friend.NewMailClient(rpcKey, mailURL)
	if err != nil {
		return err
	}
	defer func() { _ = mailer.Close() }()

	linker, err := friend.NewFriendLinkerWithMailer(store, taskCreditor, mailer, time.Now)
	if err != nil {
		return err
	}
	service, err := friend.NewService(store, linker, time.Now)
	if err != nil {
		return err
	}
	reconciler, err := friend.NewReconciler(store, linker, client)
	if err != nil {
		return err
	}

	ctx, cancel := shutdown.SignalContext(context.Background())
	defer cancel()
	go runReconcileLoop(ctx, reconciler, logger)

	mux := http.NewServeMux()
	healthHandler := health.NewHandler()
	mux.Handle("GET /livez", healthHandler)
	mux.Handle("GET /readyz", healthHandler)

	rpcInterceptor, err := rpcauth.NewServerUnaryInterceptor(rpcauth.ServerConfig{
		Key: rpcKey,
		AllowedCallers: map[string][]string{
			friendv1.FriendService_CreateShareCode_FullMethodName:   {"gate"},
			friendv1.FriendService_RedeemShareCode_FullMethodName:   {"gate"},
			friendv1.FriendService_ListFriends_FullMethodName:       {"gate", "info"},
			friendv1.FriendService_CheckMutualFriend_FullMethodName: append(rpcauth.ZoneAllowedCallers(true), "gate"),
		},
	})
	if err != nil {
		return err
	}
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(rpcInterceptor),
		grpc.MaxRecvMsgSize(128<<10),
		grpc.MaxSendMsgSize(128<<10),
	)
	defer grpcServer.Stop()
	friendv1.RegisterFriendServiceServer(grpcServer, service)

	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           rpcnet.H2CHandler(grpcServer, mux),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	logger.Info("friend listening",
		"address", cfg.HTTPAddress,
		"coordinator_url", coordinatorURL,
	)
	return shutdown.Serve(ctx, server, cfg.ShutdownTimeout, logger)
}

func runReconcileLoop(ctx context.Context, reconciler *friend.Reconciler, logger *slog.Logger) {
	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := reconciler.ReconcileDue(ctx, time.Now()); err != nil {
				logger.Error("friend Saga reconcile failed", "error", err)
			}
		}
	}
}

func friendTableNames() ([]string, error) {
	specs := [][2]string{
		{"TCAPLUS_FRIEND_CODE_CURRENT_TABLE", "FriendCodeCurrent"},
		{"TCAPLUS_FRIEND_CODE_LOOKUP_TABLE", "FriendCodeLookup"},
		{"TCAPLUS_FRIEND_RELATION_TABLE", "FriendRelation"},
		{"TCAPLUS_FRIEND_LIST_TABLE", "FriendList"},
		{"TCAPLUS_FRIEND_LINK_SAGA_TABLE", "FriendLinkSaga"},
		{"TCAPLUS_FRIEND_INTERACTION_TABLE", "FriendInteraction"},
		{"TCAPLUS_FIRST_FRIEND_REWARD_TABLE", "FirstFriendReward"},
		{"TCAPLUS_ACCOUNT_BY_PLAYER_TABLE", "AccountByPlayer"},
	}
	tables := make([]string, 0, len(specs))
	for _, spec := range specs {
		table, err := tcaplusdb.TableName(spec[0], spec[1])
		if err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
