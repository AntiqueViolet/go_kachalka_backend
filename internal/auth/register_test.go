package auth_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/auth"
)

func TestRegisterHappyPath(t *testing.T) {
	e := newEnv(t)
	res := e.register(t, testPhone, " User@Example.com ")
	if res.User.Phone != testPhone || res.User.Email != testEmail || res.User.EmailVerifiedAt != nil {
		t.Fatalf("user %+v", res.User)
	}
	if res.Tokens.Access == "" || res.Tokens.Refresh == "" || res.Tokens.RefreshExpiresIn <= 0 {
		t.Fatalf("tokens %+v", res.Tokens)
	}
	if e.out.LastCode(testEmail) == "" {
		t.Fatal("email verification code not sent")
	}
}

func TestStartRegistrationRejectsTakenPhone(t *testing.T) {
	e := newEnv(t)
	e.register(t, testPhone, testEmail)
	_, err := e.svc.StartRegistration(context.Background(), "8 (999) 123-45-67")
	wantCode(t, err, "phone_taken")
}

func TestStartRegistrationRejectsBadPhone(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.StartRegistration(context.Background(), "12345")
	ae := wantCode(t, err, "validation_failed")
	if len(ae.Fields) != 1 || ae.Fields[0].Code != "invalid_phone" {
		t.Fatalf("fields %+v", ae.Fields)
	}
}

func TestVerifyPhoneErrors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	started, err := e.svc.StartRegistration(ctx, testPhone)
	if err != nil {
		t.Fatal(err)
	}
	ae := wantCode(t, mustFail(e.svc.VerifyPhone(ctx, started.VerificationID, "000000x")), "invalid_code")
	if *ae.AttemptsLeft != 4 {
		t.Fatalf("attempts_left = %d", *ae.AttemptsLeft)
	}
	wantCode(t, mustFail(e.svc.VerifyPhone(ctx, uuid.New(), "123456")), "code_expired")
}

func TestRegisterValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	started, _ := e.svc.StartRegistration(ctx, testPhone)
	rt, err := e.svc.VerifyPhone(ctx, started.VerificationID, e.out.LastCode(testPhone))
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Register(ctx, auth.RegisterInput{RegistrationToken: rt.Token, Password: "short", Email: "nope"})
	ae := wantCode(t, err, "validation_failed")
	if len(ae.Fields) != 2 {
		t.Fatalf("fields %+v", ae.Fields)
	}
}

func TestRegisterRejectsBadToken(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Register(context.Background(), auth.RegisterInput{RegistrationToken: "garbage", Password: testPass, Email: testEmail})
	wantCode(t, err, "invalid_registration_token")
}

func TestRegistrationTokenWorksOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	started, _ := e.svc.StartRegistration(ctx, testPhone)
	rt, err := e.svc.VerifyPhone(ctx, started.VerificationID, e.out.LastCode(testPhone))
	if err != nil {
		t.Fatal(err)
	}
	in := auth.RegisterInput{RegistrationToken: rt.Token, Password: testPass, Email: testEmail}
	if _, err := e.svc.Register(ctx, in); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Register(ctx, in)
	wantCode(t, err, "phone_taken")
}

func mustFail[T any](_ T, err error) error { return err }
