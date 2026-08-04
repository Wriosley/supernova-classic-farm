// Package shutdown coordinates operating-system signals and graceful HTTP exit.
package shutdown

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// HTTPServer is the part of http.Server needed by Serve.
type HTTPServer interface {
	ListenAndServe() error
	Shutdown(context.Context) error
}

// SignalContext is canceled by an interrupt or termination signal.
func SignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

// Serve runs an HTTP server until it fails or the context is canceled. On
// cancellation it gives in-flight requests up to timeout to finish.
func Serve(ctx context.Context, server HTTPServer, timeout time.Duration, logger *slog.Logger) error {
	result := make(chan error, 1)
	go func() {
		result <- server.ListenAndServe()
	}()

	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		logger.Info("shutdown started", "timeout", timeout.String())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-result
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		logger.Info("shutdown completed")
		return err
	}
}
