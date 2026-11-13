package auth_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/apperr"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/auth"
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

const (
	testPhone = "+79991234567"
	testEmail = "user@example.com"
	testPass  = "correct horse battery"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef")

type env struct {
	svc   *auth.Service
	store *store.Store
	pool  *pgxpool.Pool
	clock *clock.Fake
	out   *notify.Recorder
	codes *codes.Sender
}

func newEnv(t *testing.T) *env {
	t.Helper()
	return newEnvWithLoginLimit(t, 5)
}

// newEnvWithLoginLimit sets how many logins per phone may be in flight or failed at once.
func newEnvWithLoginLimit(t *testing.T, loginFailures int) *env {
	t.Helper()
	pool := storetest.Pool(t)
	st := store.NewStore(pool)
	clk := clock.NewFake(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	out := notify.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	key, err := token.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := password.NewHasher(password.Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}, 4)
	if err != nil {
		t.Fatal(err)
	}
	otpSvc := otp.NewService(testSecret, otp.Limits{Cooldown: time.Minute, PerTargetPerHour: 5}, clk)
	sender := codes.NewSender(codes.Deps{
		Store: st, OTP: otpSvc, SMS: out, Email: out,
		Texts: notify.Texts{AppName: "Качалка", CodeTTL: otp.TTL}, Log: log,
	})
	svc := auth.New(auth.Deps{
		Store:  st,
		Hasher: hasher,
		Signer: token.NewSigner(token.SignerConfig{
			Key: key, Issuer: "test", AccessTTL: 15 * time.Minute, RegistrationTTL: 15 * time.Minute, Clock: clk,
		}),
		Refresh:       token.NewRefreshCodec(testSecret),
		OTP:           otpSvc,
		Codes:         sender,
		Clock:         clk,
		Log:           log,
		LoginFailures: ratelimit.Per(loginFailures, 15*time.Minute, clk),
		RefreshTTL:    60 * 24 * time.Hour,
		RefreshGrace:  30 * time.Second,
	})
	return &env{svc: svc, store: st, pool: pool, clock: clk, out: out, codes: sender}
}

// register walks all three registration steps.
func (e *env) register(t *testing.T, phone, email string) auth.Result {
	t.Helper()
	ctx := context.Background()
	started, err := e.svc.StartRegistration(ctx, phone)
	if err != nil {
		t.Fatalf("StartRegistration: %v", err)
	}
	rt, err := e.svc.VerifyPhone(ctx, started.VerificationID, e.out.LastCode(phone))
	if err != nil {
		t.Fatalf("VerifyPhone: %v", err)
	}
	res, err := e.svc.Register(ctx, auth.RegisterInput{RegistrationToken: rt.Token, Password: testPass, Email: email, DeviceName: "Pixel 9"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return res
}

func (e *env) login(t *testing.T) auth.Result {
	t.Helper()
	res, err := e.svc.Login(context.Background(), auth.LoginInput{Phone: testPhone, Password: testPass})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	return res
}

func wantCode(t *testing.T, err error, code string) *apperr.Error {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
	return ae
}
