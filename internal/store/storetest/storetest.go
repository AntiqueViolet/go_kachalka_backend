// Package storetest provides a throwaway, migrated Postgres for tests. It needs Docker
// and is skipped with `go test -short`.
package storetest

import (
	"context"
	"os"
	"runtime"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/store"
)

const image = "postgres:17-alpine"

var (
	once    sync.Once
	dsn     string
	pool    *pgxpool.Pool
	initErr error
)

// Pool returns a pool to an empty database. All tables are truncated on every call,
// so tests that use it must not run in parallel.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("needs Docker; skipped with -short")
	}
	once.Do(start)
	if initErr != nil {
		t.Fatalf("storetest: %v", initErr)
	}
	if _, err := pool.Exec(context.Background(), "TRUNCATE users, sessions, verification_codes CASCADE"); err != nil {
		t.Fatalf("storetest: truncate: %v", err)
	}
	return pool
}

// DSN is like Pool but returns the connection string, for code that opens its own pool.
func DSN(t *testing.T) string {
	t.Helper()
	Pool(t)
	return dsn
}

// Store is Pool wrapped in a store.Store.
func Store(t *testing.T) *store.Store {
	t.Helper()
	return store.NewStore(Pool(t))
}

// The container lives for the whole test binary; testcontainers' reaper removes it afterwards.
func start() {
	// On Windows testcontainers finds Docker through the docker CLI context files, and that
	// lookup sporadically fails when several test binaries start at once (go test ./...).
	// Pointing it at the standard Docker Desktop pipe skips the lookup.
	if runtime.GOOS == "windows" && os.Getenv("DOCKER_HOST") == "" {
		if initErr = os.Setenv("DOCKER_HOST", "npipe:////./pipe/docker_engine"); initErr != nil {
			return
		}
	}
	ctx := context.Background()
	c, err := postgres.Run(ctx, image,
		postgres.WithDatabase("test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		initErr = err
		return
	}
	if dsn, initErr = c.ConnectionString(ctx, "sslmode=disable"); initErr != nil {
		return
	}
	if pool, initErr = pgxpool.New(ctx, dsn); initErr != nil {
		return
	}
	initErr = store.Migrate(ctx, pool)
}

// Warm opens up to n connections at once (at most the pool size), so that a following burst of
// parallel queries really runs in parallel instead of queueing behind connection setup (race
// tests rely on it).
func Warm(t *testing.T, p *pgxpool.Pool, n int) {
	t.Helper()
	conns := make([]*pgxpool.Conn, min(n, int(p.Config().MaxConns)))
	var wg sync.WaitGroup
	errs := make(chan error, len(conns))
	for i := range conns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := p.Acquire(context.Background())
			conns[i] = c
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for _, c := range conns {
		if c != nil {
			c.Release()
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("storetest: warm: %v", err)
		}
	}
}
