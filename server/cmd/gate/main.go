package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	"github.com/Wriosley/supernova-classic-farm/server/internal/gateway"
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
		slog.Error("gate service stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load("gate", "127.0.0.1:8081")
	if err != nil {
		return err
	}
	if err := internalnet.RequireListenAddress(cfg.HTTPAddress, "GateSvr"); err != nil {
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
	gatewayID := envOr("GATEWAY_ID", gateway.DefaultGatewayID)
	zoneCommander, err := gateway.NewGRPCZoneCommander(rpcKey, gatewayID)
	if err != nil {
		return err
	}
	defer zoneCommander.Close()
	visitorCommander, err := gateway.NewGRPCVisitorZoneCommander(rpcKey, gatewayID)
	if err != nil {
		return err
	}
	defer visitorCommander.Close()
	friendURL := envOr("FRIEND_RPC_URL", "http://127.0.0.1:8085")
	friendCommander, err := gateway.NewGRPCFriendCommander(rpcKey, friendURL)
	if err != nil {
		return err
	}
	defer friendCommander.Close()
	client := newInternalHTTPClient()
	clientConfigURL := envOr("CLIENT_CONFIG_URL", gateway.DefaultConfigURL)
	configSHA, err := configuredSHA(client, clientConfigURL)
	if err != nil {
		return err
	}
	// CLIENT_CONFIG_URL is resolved server-side and may point at an in-cluster
	// address. Clients must be handed the same browser-reachable URL Login
	// advertises, otherwise AuthResponse looks like a config change and the H5
	// re-downloads the package from a host it cannot resolve.
	publicConfigURL := envOr("CLIENT_CONFIG_PUBLIC_URL", clientConfigURL)
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
		return fmt.Errorf("warm route cache: %w", err)
	}
	wsHandler, err := gateway.NewHandler(gateway.Config{
		Tickets: &gateway.HTTPTicketConsumer{
			Client: client, Endpoint: envOr("LOGIN_TICKET_CONSUME_URL", "http://127.0.0.1:8080/internal/v1/ws-tickets/consume"),
			GatewayID: gatewayID,
		},
		Routes:          routeCache,
		Zone:            zoneCommander,
		Visitor:         visitorCommander,
		Friends:         friendCommander,
		ClientConfigURL: publicConfigURL,
		ClientConfigSHA: configSHA,
	})
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.Handle("GET /ws", wsHandler)
	mux.Handle("GET /internal/v1/debug/command-failures", wsHandler.DebugCommandFailuresHandler())
	healthHandler := health.NewHandler()
	mux.Handle("GET /livez", healthHandler)
	mux.Handle("GET /readyz", healthHandler)
	pushServer, err := gateway.NewGRPCPushServer(wsHandler, gatewayID)
	if err != nil {
		return err
	}
	rpcInterceptor, err := rpcauth.NewServerUnaryInterceptor(rpcauth.ServerConfig{
		Key: rpcKey,
		AllowedCallers: map[string][]string{
			rpcv1.GatePushService_PublishPlayerStateChanged_FullMethodName: {
				"zone-local", "zone-a", "zone-b",
			},
			rpcv1.GatePushService_PublishFarmPresence_FullMethodName: {
				"zone-local", "zone-a", "zone-b",
			},
			rpcv1.GatePushService_PublishFarmViewPatch_FullMethodName: {
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
	rpcv1.RegisterGatePushServiceServer(grpcServer, pushServer)
	server := &http.Server{
		Addr: cfg.HTTPAddress, Handler: rpcnet.H2CHandler(grpcServer, mux),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	logger.Info("gate listening",
		"address", cfg.HTTPAddress,
		"gateway_id", gatewayID,
		"max_message_bytes", gateway.MaxMessageBytes,
		"production_backpressure", false,
		"distributed_connection_revocation", false,
	)
	ctx, cancel := shutdown.SignalContext(context.Background())
	defer cancel()
	return shutdown.Serve(ctx, server, cfg.ShutdownTimeout, logger)
}

func newInternalHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 128
	transport.MaxIdleConnsPerHost = 64
	// Zone closes idle connections after 30 seconds. Retire Gate-side pooled
	// connections first so a non-idempotent command never selects a stale one.
	transport.IdleConnTimeout = 20 * time.Second
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}

func configuredSHA(client *http.Client, clientConfigURL string) ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv("CLIENT_CONFIG_SHA256_HEX"))
	if raw != "" {
		digest, err := hex.DecodeString(raw)
		if err != nil || len(digest) != sha256.Size {
			return nil, fmt.Errorf("CLIENT_CONFIG_SHA256_HEX must encode exactly %d bytes", sha256.Size)
		}
		return digest, nil
	}

	response, err := client.Get(clientConfigURL)
	if err != nil {
		return nil, fmt.Errorf("download client config for digest: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download client config for digest: %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("read client config for digest: %w", err)
	}
	if len(body) > 2<<20 {
		return nil, errors.New("client config exceeds 2 MiB")
	}
	digest := sha256.Sum256(body)
	return digest[:], nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
