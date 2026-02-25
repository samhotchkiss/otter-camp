package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

type Options struct {
	Addr            string
	Version         string
	Logger          *slog.Logger
	Handler         http.Handler
	TLSMode         string
	TLSCertFile     string
	TLSKeyFile      string
	ACMEDomain      string
	ACMEEmail       string
	ACMECacheDir    string
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
	serveFn := func() error { return srv.Serve(ln) }
	tlsMode := strings.ToLower(strings.TrimSpace(opts.TLSMode))
	switch tlsMode {
	case "", "none":
		// Default plain HTTP.
	case "manual":
		if strings.TrimSpace(opts.TLSCertFile) == "" || strings.TrimSpace(opts.TLSKeyFile) == "" {
			return fmt.Errorf("manual TLS mode requires certificate and key files")
		}
		serveFn = func() error {
			return srv.ServeTLS(ln, strings.TrimSpace(opts.TLSCertFile), strings.TrimSpace(opts.TLSKeyFile))
		}
	case "acme":
		if strings.TrimSpace(opts.ACMEDomain) == "" {
			return fmt.Errorf("acme TLS mode requires a domain")
		}
		cacheDir := strings.TrimSpace(opts.ACMECacheDir)
		if cacheDir == "" {
			home, _ := os.UserHomeDir()
			if strings.TrimSpace(home) == "" {
				cacheDir = ".ottercamp/acme"
			} else {
				cacheDir = filepath.Join(home, ".ottercamp", "acme")
			}
		}
		_ = os.MkdirAll(cacheDir, 0o700)
		manager := &autocert.Manager{
			Cache:      autocert.DirCache(cacheDir),
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(strings.TrimSpace(opts.ACMEDomain)),
			Email:      strings.TrimSpace(opts.ACMEEmail),
		}
		srv.TLSConfig = &tls.Config{
			MinVersion:     tls.VersionTLS12,
			GetCertificate: manager.GetCertificate,
		}
		serveFn = func() error {
			return srv.ServeTLS(ln, "", "")
		}
	default:
		return fmt.Errorf("invalid tls mode %q", opts.TLSMode)
	}
	serveErrCh := make(chan error, 1)
	go func() {
		if err := serveFn(); err != nil && err != http.ErrServerClosed {
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
	return NewHandlerWithOptions(HandlerOptions{Version: version})
}

func shutdown(parent context.Context, srv *http.Server, timeout time.Duration) error {
	shutdownCtx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
