package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestRunGracefulShutdownDrainsInflightRequest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	handlerEntered := make(chan struct{}, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerEntered <- struct{}{}
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	})

	signalCh := make(chan os.Signal, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(context.Background(), Options{
			Listener:        listener,
			Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
			Handler:         handler,
			SignalCh:        signalCh,
			ShutdownTimeout: 2 * time.Second,
		})
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	respCh := make(chan *http.Response, 1)
	respErrCh := make(chan error, 1)
	go func() {
		resp, reqErr := client.Get("http://" + listener.Addr().String() + "/version")
		if reqErr != nil {
			respErrCh <- reqErr
			return
		}
		respCh <- resp
	}()

	select {
	case <-handlerEntered:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("handler was never entered")
	}

	signalCh <- syscall.SIGINT

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after signal")
	}

	select {
	case reqErr := <-respErrCh:
		t.Fatalf("request failed: %v", reqErr)
	case resp := <-respCh:
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	}
}
