package account_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/account"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/apperr"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/clock"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/codes"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/notify"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/otp"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/password"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/ratelimit"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store/storetest"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/token"
)

const testPass = "correct horse battery"

type env struct {
	svc    *account.Service
	store  *store.Store
	clock  *clock.Fake
	out    *notify.Recorder
	hasher *password.Hasher
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st := storetest.Store(t)
	clk := clock.NewFake(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	out := notify.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hasher, err := password.NewHasher(password.Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}, 4)
	if err != nil {
		t.Fatal(err)
	}
	otpSvc := otp.NewService([]byte("0123456789abcdef0123456789abcdef"), otp.Limits{Cooldown: time.Minute, PerTargetPerHour: 5}, clk)
	svc := account.New(account.Deps{
		Store:  st,
		Hasher: hasher,
		OTP:    otpSvc,
		Codes: codes.NewSender(codes.Deps{
			Store: st, OTP: otpSvc, SMS: out, Email: out,
			Texts: notify.Texts{AppName: "Качалка", CodeTTL: otp.TTL}, Log: log,
		}),
		Clock:            clk,
		Log:              log,
		PasswordFailures: ratelimit.Per(5, 15*time.Minute, clk),
	})
	return &env{svc: svc, store: st, clock: clk, out: out, hasher: hasher}
}

// user creates an account with testPass and one live session, returning its access identity.
func (e *env) user(t *testing.T, phone, email string) token.Access {
	t.Helper()
	ctx := context.Background()
	hash, err := e.hasher.Hash(ctx, testPass)
	if err != nil {
		t.Fatal(err)
	}
	u, err := e.store.CreateUser(ctx, store.CreateUserParams{ID: uuid.Must(uuid.NewV7()), Phone: phone, PasswordHash: hash, Email: email, Now: e.clock.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return token.Access{UserID: u.ID, SessionID: e.session(t, u.ID)}
}

func (e *env) session(t *testing.T, userID uuid.UUID) uuid.UUID {
	t.Helper()
	s, err := e.store.CreateSession(context.Background(), store.CreateSessionParams{
		ID: uuid.Must(uuid.NewV7()), UserID: userID, DeviceName: "test", Now: e.clock.Now(), ExpiresAt: e.clock.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return s.ID
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
}
