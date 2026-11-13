package account_test

import (
	"context"
	"testing"
	"time"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/store"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/token"
)

func TestMe(t *testing.T) {
	e := newEnv(t)
	a := e.user(t, "+79991234567", "a@example.com")
	u, err := e.svc.Me(context.Background(), a)
	if err != nil || u.ID != a.UserID {
		t.Fatalf("Me = %+v, %v", u, err)
	}
}

func TestChangePasswordKeepsOnlyCurrentSession(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.user(t, "+79991234567", "a@example.com")
	other := e.session(t, a.UserID)

	wantCode(t, e.svc.ChangePassword(ctx, a, "wrong password", "new password 1"), "wrong_password")
	wantCode(t, e.svc.ChangePassword(ctx, a, testPass, "short"), "validation_failed")
	if err := e.svc.ChangePassword(ctx, a, testPass, "new password 1"); err != nil {
		t.Fatal(err)
	}
	cur, _ := e.store.GetSession(ctx, a.SessionID)
	oth, _ := e.store.GetSession(ctx, other)
	if cur.RevokedAt != nil || oth.RevokedAt == nil {
		t.Fatalf("current revoked=%v other revoked=%v", cur.RevokedAt != nil, oth.RevokedAt != nil)
	}
}

func TestSensitiveOpsRequireLiveSession(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.user(t, "+79991234567", "a@example.com")
	if err := e.store.RevokeSession(ctx, store.RevokeSessionParams{ID: a.SessionID, Now: e.clock.Now(), Reason: "test"}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, e.svc.ChangePassword(ctx, a, testPass, "new password 1"), "unauthorized")
	wantCode(t, e.svc.Delete(ctx, a, testPass), "unauthorized")
}

func TestDelete(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.user(t, "+79991234567", "a@example.com")
	wantCode(t, e.svc.Delete(ctx, a, "wrong password"), "wrong_password")
	if err := e.svc.Delete(ctx, a, testPass); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Me(ctx, a)
	wantCode(t, err, "unauthorized")
	if _, err := e.store.GetSession(ctx, a.SessionID); !store.IsNotFound(err) {
		t.Fatalf("session survived account deletion: %v", err)
	}
}

func TestMeForDeletedUser(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Me(context.Background(), token.Access{})
	wantCode(t, err, "unauthorized")
}

func TestWrongPasswordIsRateLimited(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.user(t, "+79991234567", "a@example.com")
	for i := 0; i < 5; i++ {
		wantCode(t, e.svc.ChangePassword(ctx, a, "wrong password", "new password 1"), "wrong_password")
	}
	wantCode(t, e.svc.ChangePassword(ctx, a, testPass, "new password 1"), "rate_limited")
	wantCode(t, e.svc.Delete(ctx, a, testPass), "rate_limited")

	e.clock.Advance(16 * time.Minute)
	if err := e.svc.ChangePassword(ctx, a, testPass, "new password 1"); err != nil {
		t.Fatal(err)
	}
}

func TestCorrectPasswordDoesNotSpendBudget(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.user(t, "+79991234567", "a@example.com")
	for i := 0; i < 4; i++ {
		wantCode(t, e.svc.ChangePassword(ctx, a, "wrong password", "new password 1"), "wrong_password")
	}
	if _, _, err := e.svc.ChangeEmail(ctx, a, "b@example.com", testPass); err != nil {
		t.Fatal(err)
	}
	wantCode(t, e.svc.ChangePassword(ctx, a, "wrong password", "new password 1"), "wrong_password")
}
