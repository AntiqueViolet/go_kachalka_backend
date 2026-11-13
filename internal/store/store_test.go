package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/store"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store/storetest"
)

var now = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func createUser(t *testing.T, s *store.Store, phone, email string) store.User {
	t.Helper()
	u, err := s.CreateUser(context.Background(), store.CreateUserParams{
		ID: uuid.Must(uuid.NewV7()), Phone: phone, PasswordHash: "h", Email: email, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestPhoneIsUnique(t *testing.T) {
	s := storetest.Store(t)
	createUser(t, s, "+79990000001", "a@example.com")
	_, err := s.CreateUser(context.Background(), store.CreateUserParams{
		ID: uuid.Must(uuid.NewV7()), Phone: "+79990000001", PasswordHash: "h", Email: "b@example.com", Now: now,
	})
	if !store.IsUniqueViolation(err, store.ConstraintUserPhone) {
		t.Fatalf("err = %v, want unique violation on %s", err, store.ConstraintUserPhone)
	}
}

func TestEmailUniqueOnlyWhenVerified(t *testing.T) {
	s := storetest.Store(t)
	ctx := context.Background()
	a := createUser(t, s, "+79990000001", "same@example.com")
	b := createUser(t, s, "+79990000002", "SAME@example.com") // unverified duplicates are fine
	if _, err := s.MarkEmailVerified(ctx, store.MarkEmailVerifiedParams{ID: a.ID, Now: now}); err != nil {
		t.Fatal(err)
	}
	_, err := s.MarkEmailVerified(ctx, store.MarkEmailVerifiedParams{ID: b.ID, Now: now})
	if !store.IsUniqueViolation(err, store.ConstraintVerifiedEmail) {
		t.Fatalf("err = %v, want unique violation on %s (citext must ignore case)", err, store.ConstraintVerifiedEmail)
	}
	got, err := s.GetUserByVerifiedEmail(ctx, "same@example.com")
	if err != nil || got.ID != a.ID {
		t.Fatalf("GetUserByVerifiedEmail = %v, %v", got.ID, err)
	}
}

func TestInTxRollsBack(t *testing.T) {
	s := storetest.Store(t)
	ctx := context.Background()
	boom := errors.New("boom")
	err := s.InTx(ctx, func(q *store.Queries) error {
		if _, err := q.CreateUser(ctx, store.CreateUserParams{
			ID: uuid.Must(uuid.NewV7()), Phone: "+79990000003", PasswordHash: "h", Email: "c@example.com", Now: now,
		}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.GetUserByPhone(ctx, "+79990000003"); !store.IsNotFound(err) {
		t.Fatalf("user survived rollback: %v", err)
	}
}

func TestRevokeExcessSessionsKeepsMostRecent(t *testing.T) {
	s := storetest.Store(t)
	ctx := context.Background()
	u := createUser(t, s, "+79990000001", "a@example.com")
	var ids []uuid.UUID
	for i := range 3 {
		sess, err := s.CreateSession(ctx, store.CreateSessionParams{
			ID: uuid.Must(uuid.NewV7()), UserID: u.ID, DeviceName: "d",
			Now: now.Add(time.Duration(i) * time.Minute), ExpiresAt: now.Add(24 * time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, sess.ID)
	}
	if err := s.RevokeExcessSessions(ctx, store.RevokeExcessSessionsParams{UserID: u.ID, Now: now.Add(time.Hour), Keep: 2}); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		sess, err := s.GetSession(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if revoked := sess.RevokedAt != nil; revoked != (i == 0) {
			t.Errorf("session %d revoked = %v", i, revoked)
		}
	}
}

// Several instances may start at once against an empty database (a deploy that scales up):
// the migration lock makes one of them migrate and the others wait, so all of them start.
func TestConcurrentMigrate(t *testing.T) {
	admin := storetest.Pool(t)
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(storetest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = "migrate_race"
	drop := func() {
		if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS migrate_race WITH (FORCE)"); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(drop)
	for round := range 3 {
		drop()
		if _, err := admin.Exec(ctx, "CREATE DATABASE migrate_race"); err != nil {
			t.Fatal(err)
		}
		errs := make(chan error, 6)
		var wg sync.WaitGroup
		for range cap(errs) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				pool, err := pgxpool.NewWithConfig(ctx, cfg)
				if err != nil {
					errs <- err
					return
				}
				defer pool.Close()
				errs <- store.Migrate(ctx, pool)
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("round %d: %v", round+1, err)
			}
		}
	}
}
