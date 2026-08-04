package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/database"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/health"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/logging"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/shutdown"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
)

const listenAddress = "127.0.0.1:8082"

func main() {
	logger, err := logging.New("zone", "development", "info")
	if err != nil {
		log.Fatal(err)
	}

	var runtime *player.Runtime
	if dsn := os.Getenv("MYSQL_DSN"); dsn == "" {
		runtime = player.NewRuntime()
		logger.Warn("using development-only lazy in-memory player state")
	} else {
		db, openErr := database.OpenMySQL(context.Background(), dsn)
		if openErr != nil {
			log.Fatal(openErr)
		}
		defer db.Close()
		runtime, err = player.NewRuntimeWithLoader(&player.MySQLCheckpointLoader{DB: db})
		if err != nil {
			log.Fatal(err)
		}
		logger.Info("using MySQL Player checkpoint activation")
	}
	defer runtime.Close()
	pushEndpoint := os.Getenv("GATE_PUSH_URL")
	if pushEndpoint == "" {
		pushEndpoint = "http://127.0.0.1:8081/internal/v1/player-state-changes"
	}
	pushForwarder, err := player.NewHTTPPushForwarder(
		&http.Client{Timeout: 2 * time.Second},
		pushEndpoint,
	)
	if err != nil {
		log.Fatal(err)
	}
	if err := runtime.SetPushForwarder(pushForwarder); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("POST /internal/v1/command", newCommandHandler(runtime))
	healthHandler := health.NewHandler()
	mux.Handle("GET /livez", healthHandler)
	mux.Handle("GET /readyz", healthHandler)

	server := &http.Server{
		Addr:              listenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	ctx, cancel := shutdown.SignalContext(context.Background())
	defer cancel()
	logger.Info("zone listening",
		"address", listenAddress,
		"owner_zone_id", player.DefaultZoneID,
		"owner_epoch", player.LocalOwnerEpoch,
		"gate_push_url", pushEndpoint,
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
