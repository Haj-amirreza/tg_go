package platform

import (
	"context"
	"errors"
	"github.com/Haj-amirreza/tg_go/backend/internal/postgres"
	"net"
	"net/http"
	"time"
)

func Serve(ctx context.Context, address string, h http.Handler) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return errors.New("HTTP listener unavailable")
	}
	return ServeListener(ctx, listener, h)
}
func ServeListener(ctx context.Context, l net.Listener, h http.Handler) error {
	server := &http.Server{Handler: postgres.Track(h), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	done := make(chan error, 1)
	go func() { done <- server.Serve(l) }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return errors.New("HTTP shutdown deadline exceeded")
		}
		return <-normalize(done)
	}
}
func normalize(ch <-chan error) <-chan error {
	out := make(chan error, 1)
	go func() {
		err := <-ch
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		out <- err
	}()
	return out
}

// Foundation worker/scheduler lifecycle only: no jobs or Telegram calls until domain migration.
func Wait(ctx context.Context) { <-ctx.Done() }
