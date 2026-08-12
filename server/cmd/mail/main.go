package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	mailv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/mail"
	"github.com/Wriosley/supernova-classic-farm/server/internal/mail"
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

func main() {
	if err := run(); err != nil {
		slog.Error("mail service stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load("mail", "127.0.0.1:8087")
	if err != nil {
		return err
	}
	if err := internalnet.RequireListenAddress(cfg.HTTPAddress, "MailSvr"); err != nil {
		return err
	}
	logger, err := logging.New(cfg.ServiceName, cfg.Environment, cfg.LogLevel)
	if err != nil {
		return err
	}
	if strings.TrimSpace(os.Getenv("STORAGE_MODE")) != "tcaplus" {
		return errors.New("MailSvr requires STORAGE_MODE=tcaplus")
	}
	rpcKey, err := rpcauth.LoadKeyFromEnv()
	if err != nil {
		return err
	}
	adminToken, err := mail.LoadAdminTokenFromEnv()
	if err != nil {
		return err
	}

	tcaplusConfig, err := tcaplusdb.LoadConfigFromEnv()
	if err != nil {
		return err
	}
	tables, err := mailTableNames()
	if err != nil {
		return err
	}
	client, err := tcaplusdb.Open(tcaplusConfig, tables...)
	if err != nil {
		return err
	}
	defer client.Close()

	store, err := mail.NewTcaplusStore(client, tcaplusConfig.ZoneID)
	if err != nil {
		return err
	}

	var notifier mail.RedDotNotifier
	var infoCloser interface{ Close() error }
	if infoURL := strings.TrimSpace(os.Getenv("INFO_RPC_URL")); infoURL != "" {
		infoClient, infoErr := mail.NewInfoClient(rpcKey, infoURL)
		if infoErr != nil {
			return infoErr
		}
		infoCloser = infoClient
		notifier = infoClient
	} else {
		logger.Warn("INFO_RPC_URL unset; private mail red dots disabled")
	}
	if infoCloser != nil {
		defer func() { _ = infoCloser.Close() }()
	}

	coordinatorURL := envOr("COORDINATOR_URL", "http://127.0.0.1:8083")
	zoneClient, err := mail.NewZoneClient(rpcKey, coordinatorURL, nil)
	if err != nil {
		return err
	}
	defer func() { _ = zoneClient.Close() }()

	orchestrator, err := mail.NewClaimOrchestrator(store, zoneClient, time.Now)
	if err != nil {
		return err
	}
	service, err := mail.NewServiceWithOrchestrator(store, notifier, orchestrator, time.Now, logger)
	if err != nil {
		return err
	}
	admin, err := mail.NewAdminHandler(service, adminToken)
	if err != nil {
		return err
	}

	rpcInterceptor, err := rpcauth.NewServerUnaryInterceptor(rpcauth.ServerConfig{
		Key: rpcKey,
		AllowedCallers: map[string][]string{
			mailv1.MailService_OpenMailbox_FullMethodName:           {"gate"},
			mailv1.MailService_MarkMailRead_FullMethodName:          {"gate"},
			mailv1.MailService_CheckMailboxIndicator_FullMethodName: {"gate"},
			mailv1.MailService_ClaimMail_FullMethodName:             {"gate"},
			mailv1.MailService_CreateGiftMail_FullMethodName: {
				"zone-local", "zone-a", "zone-b",
			},
			mailv1.MailService_CreateSystemRewardMail_FullMethodName: {"friend"},
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
	mailv1.RegisterMailServiceServer(grpcServer, service)

	mux := http.NewServeMux()
	healthHandler := health.NewHandler()
	mux.Handle("GET /livez", healthHandler)
	mux.Handle("GET /readyz", healthHandler)
	admin.Register(mux)

	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           rpcnet.H2CHandler(grpcServer, mux),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	logger.Info("mail listening", "address", cfg.HTTPAddress)
	ctx, cancel := shutdown.SignalContext(context.Background())
	defer cancel()
	go mail.NewClaimReconciler(orchestrator, time.Now, logger).Run(ctx)
	return shutdown.Serve(ctx, server, cfg.ShutdownTimeout, logger)
}

func mailTableNames() ([]string, error) {
	specs := [][2]string{
		{"TCAPLUS_PUBLIC_MAIL_TABLE", "PublicMail"},
		{"TCAPLUS_PRIVATE_MAIL_TABLE", "PrivateMail"},
		{"TCAPLUS_PLAYER_MAILBOX_CURSOR_TABLE", "PlayerMailboxCursor"},
		{"TCAPLUS_PLAYER_MAIL_STATE_TABLE", "PlayerMailState"},
		{"TCAPLUS_MAIL_SOURCE_DEDUP_TABLE", "MailSourceDedup"},
		{"TCAPLUS_MAIL_CLAIM_SAGA_TABLE", "MailClaimSaga"},
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
