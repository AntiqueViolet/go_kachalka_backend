package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/app"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/clock"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/config"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/notify"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/password"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store/storetest"
)

const (
	phone = "+79991234567"
	email = "user@example.com"
	pass  = "correct horse battery"
)

type harness struct {
	t     *testing.T
	srv   *httptest.Server
	app   *app.App
	out   *notify.Recorder
	clock *clock.Fake
	pool  *pgxpool.Pool
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessEnv(t, nil)
}

func newHarnessEnv(t *testing.T, env map[string]string) *harness {
	t.Helper()
	pool := storetest.Pool(t)
	vars := map[string]string{"DATABASE_URL": "unused: the pool is passed directly"}
	for k, v := range env {
		vars[k] = v
	}
	cfg, err := config.LoadFrom(vars)
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	out := notify.NewRecorder()
	a, err := app.New(app.Options{
		Config: cfg, Pool: pool, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock: clk, SMS: out, Email: out,
		PasswordParams: &password.Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler)
	t.Cleanup(srv.Close)
	return &harness{t: t, srv: srv, app: a, out: out, clock: clk, pool: pool}
}

// call sends JSON and decodes a JSON object response (nil for empty bodies).
func (h *harness) call(method, path, bearer string, body any) (int, map[string]any) {
	h.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			h.t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, h.srv.URL+path, &buf)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (h *harness) want(status int, gotStatus int, body map[string]any) {
	h.t.Helper()
	if gotStatus != status {
		h.t.Fatalf("status %d, want %d; body %v", gotStatus, status, body)
	}
}

// register runs the three registration calls and returns the AuthResult body.
func (h *harness) register() map[string]any {
	h.t.Helper()
	st, body := h.call("POST", "/v1/auth/register/start", "", map[string]any{"phone": "8 999 123-45-67"})
	h.want(http.StatusAccepted, st, body)
	st, body = h.call("POST", "/v1/auth/register/verify", "", map[string]any{
		"verification_id": body["verification_id"], "code": h.out.LastCode(phone),
	})
	h.want(http.StatusOK, st, body)
	st, body = h.call("POST", "/v1/auth/register", "", map[string]any{
		"registration_token": body["registration_token"], "password": pass, "email": email, "device_name": "Pixel 9",
	})
	h.want(http.StatusCreated, st, body)
	return body
}

func tokens(body map[string]any) (access, refresh string) {
	t := body["tokens"].(map[string]any)
	return t["access_token"].(string), t["refresh_token"].(string)
}

func TestRegistrationEmailRefreshLogout(t *testing.T) {
	h := newHarness(t)
	reg := h.register()
	access, refresh := tokens(reg)

	st, me := h.call("GET", "/v1/me", access, nil)
	h.want(http.StatusOK, st, me)
	if me["phone"] != phone || me["email"] != email || me["email_verified"] != false {
		t.Fatalf("me = %v", me)
	}

	st, me = h.call("POST", "/v1/me/email/verify", access, map[string]any{"code": h.out.LastCode(email)})
	h.want(http.StatusOK, st, me)
	if me["email_verified"] != true {
		t.Fatalf("email not verified: %v", me)
	}

	st, body := h.call("POST", "/v1/auth/refresh", "", map[string]any{"refresh_token": refresh})
	h.want(http.StatusOK, st, body)
	newAccess, _ := tokens(body)

	st, body = h.call("POST", "/v1/auth/logout", newAccess, nil)
	h.want(http.StatusNoContent, st, body)
	st, body = h.call("POST", "/v1/auth/refresh", "", map[string]any{"refresh_token": refresh})
	if st != http.StatusUnauthorized {
		t.Fatalf("refresh after logout: %d %v", st, body)
	}
}

func TestProblemDetails(t *testing.T) {
	h := newHarness(t)
	st, body := h.call("POST", "/v1/auth/login", "", map[string]any{"phone": "123", "password": "x"})
	h.want(http.StatusUnprocessableEntity, st, body)
	errs := body["errors"].([]any)
	if body["code"] != "validation_failed" || errs[0].(map[string]any)["field"] != "phone" {
		t.Fatalf("body %v", body)
	}

	h.register()
	st, body = h.call("POST", "/v1/auth/register/start", "", map[string]any{"phone": phone})
	if st != http.StatusConflict || body["code"] != "phone_taken" {
		t.Fatalf("second registration: %d %v", st, body)
	}
}

func TestPasswordResetOverHTTP(t *testing.T) {
	h := newHarness(t)
	reg := h.register()
	access, _ := tokens(reg)
	st, body := h.call("POST", "/v1/me/email/verify", access, map[string]any{"code": h.out.LastCode(email)})
	h.want(http.StatusOK, st, body)

	h.clock.Advance(time.Minute)
	st, body = h.call("POST", "/v1/auth/password/forgot", "", map[string]any{"email": email})
	h.want(http.StatusAccepted, st, body)
	h.app.Wait() // the reset email is sent in the background
	st, body = h.call("POST", "/v1/auth/password/reset", "", map[string]any{
		"email": email, "code": h.out.LastCode(email), "new_password": "a brand new one",
	})
	h.want(http.StatusNoContent, st, body)

	st, body = h.call("POST", "/v1/auth/login", "", map[string]any{"phone": phone, "password": "a brand new one"})
	h.want(http.StatusOK, st, body)
}

func TestDeleteAccountOverHTTP(t *testing.T) {
	h := newHarness(t)
	access, _ := tokens(h.register())
	st, body := h.call("DELETE", "/v1/me", access, map[string]any{"password": "wrong password"})
	if st != http.StatusForbidden || body["code"] != "wrong_password" {
		t.Fatalf("wrong password: %d %v", st, body)
	}
	st, body = h.call("DELETE", "/v1/me", access, map[string]any{"password": pass})
	h.want(http.StatusNoContent, st, body)
	st, body = h.call("GET", "/v1/me", access, nil)
	if st != http.StatusUnauthorized {
		t.Fatalf("me after delete: %d %v", st, body)
	}
}

func TestCleanupRemovesDeadSessionsAndOldCodes(t *testing.T) {
	h := newHarness(t)
	h.register()
	ctx := context.Background()
	count := func(table string) int {
		t.Helper()
		var n int
		if err := h.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if err := h.app.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if s, c := count("sessions"), count("verification_codes"); s != 1 || c != 2 {
		t.Fatalf("fresh data removed: sessions=%d codes=%d", s, c)
	}
	h.clock.Advance(91 * 24 * time.Hour) // past the 60-day refresh TTL plus 30 days
	if err := h.app.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if s, c := count("sessions"), count("verification_codes"); s != 0 || c != 0 {
		t.Fatalf("stale data left: sessions=%d codes=%d", s, c)
	}
}

func TestLoginIPRateLimitIsWired(t *testing.T) {
	h := newHarnessEnv(t, map[string]string{"RATE_LOGIN_PER_IP_PER_MINUTE": "2"})
	for i := 1; i <= 3; i++ {
		st, body := h.call("POST", "/v1/auth/login", "", map[string]any{"phone": phone, "password": pass})
		want := http.StatusUnauthorized
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		h.want(want, st, body)
	}
}
