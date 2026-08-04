package shutdown

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"
)

type fakeServer struct {
	started  chan struct{}
	stopped  chan struct{}
	shutdown chan struct{}
}

func (server *fakeServer) ListenAndServe() error {
	close(server.started)
	<-server.stopped
	return http.ErrServerClosed
}

func (server *fakeServer) Shutdown(context.Context) error {
	close(server.shutdown)
	close(server.stopped)
	return nil
}

func TestServeShutsDownAfterCancellation(t *testing.T) {
	server := &fakeServer{
		started:  make(chan struct{}),
		stopped:  make(chan struct{}),
		shutdown: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	go func() {
		result <- Serve(ctx, server, time.Second, logger)
	}()
	<-server.started
	cancel()

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve() did not stop")
	}
}
