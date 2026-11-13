package account_test

import (
	"context"
	"testing"
	"time"
)

func TestVerifyEmail(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.user(t, "+79991234567", "a@example.com")
	if _, err := e.svc.ResendEmailCode(ctx, a); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.VerifyEmail(ctx, a, "000000x")
	wantCode(t, err, "invalid_code")
	u, err := e.svc.VerifyEmail(ctx, a, e.out.LastCode("a@example.com"))
	if err != nil || u.EmailVerifiedAt == nil {
		t.Fatalf("VerifyEmail = %+v, %v", u, err)
	}
	_, err = e.svc.VerifyEmail(ctx, a, "123456")
	wantCode(t, err, "email_already_verified")
	_, err = e.svc.ResendEmailCode(ctx, a)
	wantCode(t, err, "email_already_verified")
}

func TestVerifyEmailTakenByAnotherAccount(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	first := e.user(t, "+79991234567", "same@example.com")
	second := e.user(t, "+79991234568", "same@example.com")
	if _, err := e.svc.ResendEmailCode(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.VerifyEmail(ctx, first, e.out.LastCode("same@example.com")); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(time.Minute)
	if _, err := e.svc.ResendEmailCode(ctx, second); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.VerifyEmail(ctx, second, e.out.LastCode("same@example.com"))
	wantCode(t, err, "email_taken")
}

func TestChangeEmail(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.user(t, "+79991234567", "old@example.com")

	_, _, err := e.svc.ChangeEmail(ctx, a, "new@example.com", "wrong password")
	wantCode(t, err, "wrong_password")
	_, _, err = e.svc.ChangeEmail(ctx, a, "not-an-email", testPass)
	wantCode(t, err, "validation_failed")

	u, sent, err := e.svc.ChangeEmail(ctx, a, "New@Example.com", testPass)
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "new@example.com" || u.EmailVerifiedAt != nil || sent.ResendAfter != time.Minute {
		t.Fatalf("user %+v sent %+v", u, sent)
	}
	if _, err := e.svc.VerifyEmail(ctx, a, e.out.LastCode("new@example.com")); err != nil {
		t.Fatalf("verify new email: %v", err)
	}
	_, _, err = e.svc.ChangeEmail(ctx, a, "new@example.com", testPass)
	wantCode(t, err, "email_already_verified")
}

func TestChangeEmailRolledBackWhenRateLimited(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.user(t, "+79991234567", "old@example.com")
	if _, _, err := e.svc.ChangeEmail(ctx, a, "new@example.com", testPass); err != nil {
		t.Fatal(err)
	}
	b := e.user(t, "+79991234568", "b@example.com")
	_, _, err := e.svc.ChangeEmail(ctx, b, "new@example.com", testPass) // same address within cooldown
	wantCode(t, err, "rate_limited")
	u, _ := e.svc.Me(ctx, b)
	if u.Email != "b@example.com" {
		t.Fatalf("email changed despite rate limit: %s", u.Email)
	}
}
