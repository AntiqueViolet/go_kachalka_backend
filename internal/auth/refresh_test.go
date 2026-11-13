package auth_test

import (
	"context"
	"testing"
	"time"
)

func TestRefreshRotates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	r1 := e.register(t, testPhone, testEmail).Tokens.Refresh
	t2, err := e.svc.Refresh(ctx, r1)
	if err != nil {
		t.Fatal(err)
	}
	if t2.Refresh == r1 || t2.Access == "" {
		t.Fatal("refresh token not rotated")
	}
	if _, err := e.svc.Refresh(ctx, t2.Refresh); err != nil {
		t.Fatalf("rotated token rejected: %v", err)
	}
}

func TestRefreshReplayWithinGraceReturnsSameToken(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	r1 := e.register(t, testPhone, testEmail).Tokens.Refresh
	t2, err := e.svc.Refresh(ctx, r1)
	if err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(10 * time.Second)
	again, err := e.svc.Refresh(ctx, r1)
	if err != nil {
		t.Fatalf("replay within grace: %v", err)
	}
	if again.Refresh != t2.Refresh {
		t.Fatal("replay must return the token issued by the first rotation")
	}
}

func TestRefreshReuseRevokesSession(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	r1 := e.register(t, testPhone, testEmail).Tokens.Refresh
	t2, err := e.svc.Refresh(ctx, r1)
	if err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(31 * time.Second)
	_, err = e.svc.Refresh(ctx, r1)
	wantCode(t, err, "refresh_token_reused")
	_, err = e.svc.Refresh(ctx, t2.Refresh)
	wantCode(t, err, "invalid_refresh_token")
}

func TestRefreshExpiresAfterTTL(t *testing.T) {
	e := newEnv(t)
	r1 := e.register(t, testPhone, testEmail).Tokens.Refresh
	e.clock.Advance(61 * 24 * time.Hour)
	_, err := e.svc.Refresh(context.Background(), r1)
	wantCode(t, err, "invalid_refresh_token")
}

func TestRefreshRejectsGarbage(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Refresh(context.Background(), "garbage")
	wantCode(t, err, "invalid_refresh_token")
}
