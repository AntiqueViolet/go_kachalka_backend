package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/app"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/config"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store"
)

const (
	shutdownTimeout = 20 * time.Second
	cleanupEvery    = time.Hour
	dbWaitAttempts  = 30
)

// run migrates the database, then binds the port via listen and serves until ctx is
// cancelled, then drains in-flight requests for up to shutdownTimeout. The port is bound
// only after the database is ready, so clients and the Docker healthcheck get
// "connection refused" instead of hanging while the service is not usable yet.
// A signal during startup is a clean stop, not an error.
func run(ctx context.Context, cfg config.Config, log *slog.Logger, listen func() (net.Listener, error)) error {
	if cfg.UsingDevSecret() {
		log.Warn("APP_SECRET is empty: using the insecure dev secret")
	}
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer pool.Close()
	if err := waitForDB(ctx, pool, log); err != nil {
		return startupErr(ctx, log, err)
	}
	if err := store.Migrate(ctx, pool); err != nil {
		return startupErr(ctx, log, err)
	}
	a, err := app.New(app.Options{Config: cfg, Pool: pool, Log: log})
	if err != nil {
		return err
	}
	ln, err := listen()
	if err != nil {
		return err
	}

	srv := &http.Server{
		Handler:           a.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	cleanupCtx, stopCleanup := context.WithCancel(ctx)
	defer stopCleanup()
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		a.RunCleanup(cleanupCtx, cleanupEvery)
	}()

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	log.Info("listening", "addr", ln.Addr().String(), "env", cfg.Env)

	select {
	case err := <-errc:
		stopCleanup()
		<-cleanupDone
		a.Wait()
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		_ = srv.Close() // drain timed out: drop the remaining connections
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// The server is stopped: let the cleanup pass and background emails finish
	// before the deferred pool.Close pulls the database away.
	<-cleanupDone
	a.Wait()
	return nil
}

// startupErr turns the error caused by ctx being cancelled during startup into nil.
func startupErr(ctx context.Context, log *slog.Logger, err error) error {
	if ctx.Err() != nil {
		log.Info("startup interrupted")
		return nil
	}
	return err
}

// waitForDB retries the first connection for up to dbWaitAttempts seconds: under docker
// compose the database may still be starting.
func waitForDB(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	var err error
	for attempt := 1; attempt <= dbWaitAttempts; attempt++ {
		if err = pool.Ping(ctx); err == nil {
			return nil
		}
		log.Warn("database not ready, retrying", "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("database: %w", err)
}

// healthcheck asks the server listening on addr (HTTP_ADDR) for /healthz.
func healthcheck(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// gosec G704 (SSRF) does not apply: the URL is built from our own HTTP_ADDR.
	url := "http://" + net.JoinHostPort(host, port) + "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) //nolint:gosec
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz: status %d", resp.StatusCode)
	}
	return nil
}
