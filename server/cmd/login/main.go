package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/auth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/config"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/database"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/internalnet"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/logging"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/shutdown"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/tcaplusdb"
)

func main() {
	if err := run(); err != nil {
		slog.Error("login service stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load("login", "127.0.0.1:8080")
	if err != nil {
		return err
	}
	if err := internalnet.RequireListenAddress(cfg.HTTPAddress, "LoginSvr"); err != nil {
		return err
	}
	logger, err := logging.New(cfg.ServiceName, cfg.Environment, cfg.LogLevel)
	if err != nil {
		return err
	}
	var store *auth.Store
	if strings.TrimSpace(os.Getenv("STORAGE_MODE")) == "tcaplus" {
		if cfg.MySQLDSN != "" {
			return errors.New("pure Tcaplus mode forbids MYSQL_DSN")
		}
		tcaplusConfig, configErr := tcaplusdb.LoadConfigFromEnv()
		if configErr != nil {
			return configErr
		}
		tableSpecs := [][2]string{
			{"TCAPLUS_PLAYER_ID_COUNTER_TABLE", "PlayerIdCounter"},
			{"TCAPLUS_ACCOUNT_BY_NAME_TABLE", "AccountByName"},
			{"TCAPLUS_ACCOUNT_BY_PLAYER_TABLE", "AccountByPlayer"},
			{"TCAPLUS_SESSION_TABLE", "Session"},
		}
		tables := make([]string, 0, len(tableSpecs))
		for _, spec := range tableSpecs {
			table, tableErr := tcaplusdb.TableName(spec[0], spec[1])
			if tableErr != nil {
				return tableErr
			}
			tables = append(tables, table)
		}
		client, openErr := tcaplusdb.Open(tcaplusConfig, tables...)
		if openErr != nil {
			return openErr
		}
		defer client.Close()
		store, err = auth.NewTcaplusStore(client, tcaplusConfig.ZoneID)
		if err != nil {
			return err
		}
		logger.Info("using pure Tcaplus auth store; registration creates account identity only")
	} else if cfg.MySQLDSN == "" {
		store, err = auth.NewStore()
		if err != nil {
			return err
		}
		logger.Warn("using development-only in-memory auth store; registrations are not durable")
	} else {
		db, openErr := database.OpenMySQL(context.Background(), cfg.MySQLDSN)
		if openErr != nil {
			return openErr
		}
		defer db.Close()
		store, err = auth.NewMySQLStore(db)
		if err != nil {
			return err
		}
		logger.Info("using MySQL auth store; registration creates account and Session only")
	}
	if err := auth.ApplyTicketHMACKeyFromEnv(store); err != nil {
		return err
	}
	handler, err := auth.NewHandler(store, auth.HandlerConfig{
		Origin:          envOr("H5_ORIGIN", "http://localhost:5173"),
		GatewayID:       envOr("GATEWAY_ID", "local-gateway"),
		GatewayURL:      envOr("GATEWAY_URL", "ws://127.0.0.1:8081/ws"),
		ClientConfigURL: envOr("CLIENT_CONFIG_URL", "http://127.0.0.1:8080/v1/client-config/1"),
	})
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr: cfg.HTTPAddress, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second,
	}
	logger.Info("login listening",
		"address", cfg.HTTPAddress, "gateway_url", envOr("GATEWAY_URL", "ws://127.0.0.1:8081/ws"))
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
