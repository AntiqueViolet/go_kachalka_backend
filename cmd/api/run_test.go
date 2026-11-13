package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/config"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store/storetest"
)

func TestRunServesAndShutsDown(t *testing.T) {
	cfg, err := config.LoadFrom(map[string]string{"DATABASE_URL": storetest.DSN(t)})
	if err != nil {
		t.Fatal(err)
	}
	// run binds the port itself, after the database is ready; learn the address from it.
	addrc := make(chan string, 1)
	listen := func() (net.Listener, error) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err == nil {
			addrc <- ln.Addr().String()
		}
		return ln, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), listen) }()

	var addr string
	select {
	case addr = <-addrc:
	case err := <-done:
		t.Fatalf("run returned before listening: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("run never listened")
	}
	base := "http://" + addr
	deadline := time.Now().Add(15 * time.Second)
	for {
		resp, err := http.Get(base + "/readyz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("server not ready: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := healthcheck(addr); err != nil {
		t.Fatalf("healthcheck: %v", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned %v", err)
		}
	case <-time.After(shutdownTimeout + 5*time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestHealthcheckFailsWithoutServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	if err := healthcheck(addr); err == nil {
		t.Fatal("healthcheck succeeded with nothing listening")
	}
}
