package auth_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/apperr"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/auth"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store/storetest"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/token"
)

func TestLogin(t *testing.T) {
	e := newEnv(t)
	reg := e.register(t, testPhone, testEmail)
	res := e.login(t)
	if res.User.ID != reg.User.ID || res.Tokens.Access == "" {
		t.Fatalf("login result %+v", res)
	}
	ctx := context.Background()
	_, err := e.svc.Login(ctx, auth.LoginInput{Phone: testPhone, Password: "wrong password"})
	wantCode(t, err, "invalid_credentials")
	_, err = e.svc.Login(ctx, auth.LoginInput{Phone: "+79990000000", Password: testPass})
	wantCode(t, err, "invalid_credentials")
}

func TestLoginLockoutAfterFailures(t *testing.T) {
	e := newEnv(t)
	e.register(t, testPhone, testEmail)
	ctx := context.Background()
	for range 5 {
		_, err := e.svc.Login(ctx, auth.LoginInput{Phone: testPhone, Password: "wrong password"})
		wantCode(t, err, "invalid_credentials")
	}
	_, err := e.svc.Login(ctx, auth.LoginInput{Phone: testPhone, Password: testPass})
	if ae := wantCode(t, err, "rate_limited"); ae.RetryAfter <= 0 {
		t.Fatalf("retry after %v", ae.RetryAfter)
	}
	e.clock.Advance(3 * time.Minute)
	e.login(t)
}

func TestSuccessfulLoginsDoNotCountAsFailures(t *testing.T) {
	e := newEnv(t)
	e.register(t, testPhone, testEmail)
	for range 10 {
		e.login(t)
	}
	_, err := e.svc.Login(context.Background(), auth.LoginInput{Phone: testPhone, Password: "wrong password"})
	wantCode(t, err, "invalid_credentials")
}

// Parallel guesses all start before any failure is recorded; together they must still get
// no more than the 5 attempts the limit allows.
func TestParallelWrongPasswordsRespectLimit(t *testing.T) {
	e := newEnv(t)
	e.register(t, testPhone, testEmail)
	const guesses = 20
	errs := make(chan error, guesses)
	var wg sync.WaitGroup
	for range guesses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.svc.Login(context.Background(), auth.LoginInput{Phone: testPhone, Password: "wrong password"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	checked := 0
	for err := range errs {
		var ae *apperr.Error
		if !errors.As(err, &ae) {
			t.Fatalf("unexpected error %v", err)
		}
		if ae.Code == "invalid_credentials" {
			checked++
		} else if ae.Code != "rate_limited" {
			t.Fatalf("unexpected code %s", ae.Code)
		}
	}
	if checked != 5 {
		t.Fatalf("%d passwords were checked, want exactly 5", checked)
	}
}

func TestSessionLimit(t *testing.T) {
	e := newEnv(t)
	first := e.register(t, testPhone, testEmail)
	for range auth.MaxSessions {
		e.clock.Advance(time.Second)
		e.login(t)
	}
	_, err := e.svc.Refresh(context.Background(), first.Tokens.Refresh)
	wantCode(t, err, "invalid_refresh_token")
}

func TestLogout(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register(t, testPhone, testEmail)
	b := e.login(t)
	access := func(r auth.Result) token.Access {
		t.Helper()
		sid, _, err := token.NewRefreshCodec(testSecret).Decode(r.Tokens.Refresh)
		if err != nil {
			t.Fatal(err)
		}
		return token.Access{UserID: r.User.ID, SessionID: sid}
	}
	if err := e.svc.Logout(ctx, access(a), false); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Refresh(ctx, a.Tokens.Refresh)
	wantCode(t, err, "invalid_refresh_token")
	if _, err := e.svc.Refresh(ctx, b.Tokens.Refresh); err != nil {
		t.Fatalf("other session affected: %v", err)
	}
	c := e.login(t)
	if err := e.svc.Logout(ctx, access(c), true); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Refresh(ctx, c.Tokens.Refresh)
	wantCode(t, err, "invalid_refresh_token")
}

// Parallel logins must not leave more than MaxSessions live sessions: each one trims the
// sessions while holding the user row lock. (The failed-login limiter reserves an attempt for
// every login in flight, so it is raised here to let all of them run at once.)
func TestParallelLoginsKeepSessionLimit(t *testing.T) {
	const parallel = 3 * auth.MaxSessions
	e := newEnvWithLoginLimit(t, parallel)
	reg := e.register(t, testPhone, testEmail)
	storetest.Warm(t, e.pool, parallel)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := e.svc.Login(context.Background(), auth.LoginInput{Phone: testPhone, Password: testPass}); err != nil {
				t.Errorf("Login: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	var live int
	err := e.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL", reg.User.ID).Scan(&live)
	if err != nil {
		t.Fatal(err)
	}
	if live != auth.MaxSessions {
		t.Fatalf("%d live sessions, want exactly %d", live, auth.MaxSessions)
	}
}
