package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	infov1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/info"
	"github.com/Wriosley/supernova-classic-farm/server/internal/gateway"
	"github.com/Wriosley/supernova-classic-farm/server/internal/info"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/config"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/health"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/internalnet"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/logging"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/shutdown"
	"google.golang.org/grpc"
)

func main() {
	if err := run(); err != nil {
		slog.Error("info service stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load("info", "127.0.0.1:8086")
	if err != nil {
		return err
	}
	if err := internalnet.RequireListenAddress(cfg.HTTPAddress, "InfoSvr"); err != nil {
		return err
	}
	logger, err := logging.New(cfg.ServiceName, cfg.Environment, cfg.LogLevel)
	if err != nil {
		return err
	}
	rpcKey, err := rpcauth.LoadKeyFromEnv()
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 5 * time.Second}
	routeSource := &gateway.HTTPRouteResolver{
		Client: client, BaseURL: envOr("COORDINATOR_URL", "http://127.0.0.1:8083"),
	}
	routeCache, err := gateway.NewCachedRouteResolver(routeSource, time.Now)
	if err != nil {
		return err
	}
	warmCtx, warmCancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = routeCache.Warm(warmCtx)
	warmCancel()
	if err != nil {
		return err
	}

	zoneClient, err := info.NewZoneClient(rpcKey)
	if err != nil {
		return err
	}
	defer zoneClient.Close()
	var friendLister info.FriendLister
	if friendURL := strings.TrimSpace(os.Getenv("FRIEND_RPC_URL")); friendURL != "" {
		friendClient, friendErr := info.NewFriendClient(rpcKey, friendURL)
		if friendErr != nil {
			return friendErr
		}
		defer friendClient.Close()
		friendLister = friendClient
	} else {
		friendLister = nil
		logger.Warn("FRIEND_RPC_URL unset; friend-farm red dots disabled")
	}

	service, err := info.NewService(routeCache, zoneClient, friendLister, logger)
	if err != nil {
		return err
	}

	rpcInterceptor, err := rpcauth.NewServerUnaryInterceptor(rpcauth.ServerConfig{
		Key: rpcKey,
		AllowedCallers: map[string][]string{
			infov1.InfoService_SetMailRedDot_FullMethodName: {
				"mail", "zone-local", "zone-a", "zone-b",
			},
			infov1.InfoService_NotifyOwnerPlotStealable_FullMethodName: {
				"zone-local", "zone-a", "zone-b",
			},
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
	infov1.RegisterInfoServiceServer(grpcServer, service)

	mux := http.NewServeMux()
	healthHandler := health.NewHandler()
	mux.Handle("GET /livez", healthHandler)
	mux.Handle("GET /readyz", healthHandler)
	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           rpcnet.H2CHandler(grpcServer, mux),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	logger.Info("info listening", "address", cfg.HTTPAddress)
	ctx, cancel := shutdown.SignalContext(context.Background())
	defer cancel()
	return shutdown.Serve(ctx, server, cfg.ShutdownTimeout, logger)
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
