package auth_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/auth"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store"
)

// forgot requests a reset code and waits for the background email.
func (e *env) forgot(t *testing.T, email string) {
	t.Helper()
	if _, err := e.svc.ForgotPassword(context.Background(), email); err != nil {
		t.Fatalf("ForgotPassword(%s): %v", email, err)
	}
	e.codes.Wait()
}

func (e *env) verifyEmail(t *testing.T, res auth.Result) {
	t.Helper()
	if _, err := e.store.MarkEmailVerified(context.Background(), store.MarkEmailVerifiedParams{ID: res.User.ID, Now: e.clock.Now()}); err != nil {
		t.Fatal(err)
	}
}

func TestPasswordReset(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	reg := e.register(t, testPhone, testEmail)
	e.verifyEmail(t, reg)
	e.clock.Advance(time.Minute) // the verification email started a per-address cooldown

	e.forgot(t, testEmail)
	const newPass = "brand new password"
	if err := e.svc.ResetPassword(ctx, auth.ResetInput{Email: testEmail, Code: e.out.LastCode(testEmail), NewPassword: newPass}); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Refresh(ctx, reg.Tokens.Refresh)
	wantCode(t, err, "invalid_refresh_token")
	_, err = e.svc.Login(ctx, auth.LoginInput{Phone: testPhone, Password: testPass})
	wantCode(t, err, "invalid_credentials")
	if _, err := e.svc.Login(ctx, auth.LoginInput{Phone: testPhone, Password: newPass}); err != nil {
		t.Fatalf("login with new password: %v", err)
	}
}

func TestForgotPasswordIsSilentForUnknownAndUnverified(t *testing.T) {
	e := newEnv(t)
	e.register(t, testPhone, testEmail) // email stays unverified: 1 verification email
	for _, addr := range []string{"nobody@example.com", testEmail} {
		e.forgot(t, addr)
	}
	if n := e.out.Count(testEmail); n != 1 {
		t.Fatalf("emails to unverified address = %d, want only the verification email", n)
	}
	if e.out.Count("nobody@example.com") != 0 {
		t.Fatal("email sent to unknown address")
	}
}

// Every refused code gets the same answer, so a reset attempt does not reveal whether the
// address belongs to an account (the attacker can request a code for any address himself).
func TestResetPasswordErrorsDoNotRevealTheAccount(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	reg := e.register(t, testPhone, testEmail)
	e.verifyEmail(t, reg)
	reset := func(email, code string) error {
		return e.svc.ResetPassword(ctx, auth.ResetInput{Email: email, Code: code, NewPassword: "whatever123"})
	}
	same := func(name string, err error) {
		t.Helper()
		if ae := wantCode(t, err, "invalid_code"); ae.AttemptsLeft != nil || ae.Status != 422 {
			t.Fatalf("%s: %+v, want the plain invalid_code without attempts_left", name, ae)
		}
	}

	same("unknown address", reset("nobody@example.com", "123456"))
	same("no code requested", reset(testEmail, "123456"))
	e.clock.Advance(time.Minute) // the verification email started a per-address cooldown
	e.forgot(t, testEmail)
	code := e.out.LastCode(testEmail)
	for i := range 5 {
		same(fmt.Sprintf("wrong code %d", i+1), reset(testEmail, "000000x"))
	}
	same("attempts used up", reset(testEmail, code))
	wantCode(t, e.svc.ResetPassword(ctx, auth.ResetInput{Email: testEmail, Code: code, NewPassword: "short"}), "validation_failed")
}

func TestResetCodeIsSingleUse(t *testing.T) {
	e := newEnv(t)
	reg := e.register(t, testPhone, testEmail)
	e.verifyEmail(t, reg)
	e.clock.Advance(time.Minute)
	e.forgot(t, testEmail)
	in := auth.ResetInput{Email: testEmail, Code: e.out.LastCode(testEmail), NewPassword: "brand new password"}
	if err := e.svc.ResetPassword(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	wantCode(t, e.svc.ResetPassword(context.Background(), in), "invalid_code")
}
