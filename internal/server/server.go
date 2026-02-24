package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"
)

type Options struct {
	Addr            string
	Version         string
	Logger          *slog.Logger
	Handler         http.Handler
	Listener        net.Listener
	SignalCh        <-chan os.Signal
	ShutdownTimeout time.Duration
}

func Run(ctx context.Context, opts Options) error {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Addr == "" {
		opts.Addr = ":4110"
	}
	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = 30 * time.Second
	}
	if opts.Handler == nil {
		opts.Handler = NewHandler(opts.Version)
	}

	ln := opts.Listener
	if ln == nil {
		var err error
		ln, err = net.Listen("tcp", opts.Addr)
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
	}
	defer ln.Close()

	srv := &http.Server{Handler: opts.Handler}
	serveErrCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			serveErrCh <- err
		}
		close(serveErrCh)
	}()

	opts.Logger.Info("server started", "addr", ln.Addr().String())

	select {
	case err := <-serveErrCh:
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	case <-ctx.Done():
		return shutdown(ctx, srv, opts.ShutdownTimeout)
	case <-opts.SignalCh:
		return shutdown(ctx, srv, opts.ShutdownTimeout)
	}
}

func NewHandler(version string) http.Handler {
	return NewHandlerWithOptions(HandlerOptions{
		Version: version,
		Mode:    os.Getenv("OTTERCAMP_MODE"),
	})
}

func shutdown(parent context.Context, srv *http.Server, timeout time.Duration) error {
	shutdownCtx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
