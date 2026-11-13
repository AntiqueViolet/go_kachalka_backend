// Command api is the go_kachalka backend server.
//
//	api              serve HTTP (configuration from environment variables, see README)
//	api healthcheck  exit 0 if the local server answers /healthz (for Docker HEALTHCHECK)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/config"
)

func main() {
	var err error
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		addr := os.Getenv("HTTP_ADDR")
		if addr == "" {
			addr = ":8080"
		}
		err = healthcheck(addr)
	} else {
		err = serve()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.Level}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// run binds the port only once the database is ready, see run.
	listen := func() (net.Listener, error) {
		return (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.HTTPAddr)
	}
	return run(ctx, cfg, log, listen)
}
