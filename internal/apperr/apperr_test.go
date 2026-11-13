package apperr

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestCatalog pins every sentinel to the status and code from spec section 10:
// clients depend on these codes, so a typo must fail here.
func TestCatalog(t *testing.T) {
	cases := []struct {
		err    *Error
		status int
		code   string
	}{
		{ErrBadRequest, http.StatusBadRequest, "bad_request"},
		{ErrUnauthorized, http.StatusUnauthorized, "unauthorized"},
		{ErrInvalidCredentials, http.StatusUnauthorized, "invalid_credentials"},
		{ErrInvalidRegistrationToken, http.StatusUnauthorized, "invalid_registration_token"},
		{ErrInvalidRefreshToken, http.StatusUnauthorized, "invalid_refresh_token"},
		{ErrRefreshTokenReused, http.StatusUnauthorized, "refresh_token_reused"},
		{ErrWrongPassword, http.StatusForbidden, "wrong_password"},
		{ErrPhoneTaken, http.StatusConflict, "phone_taken"},
		{ErrEmailTaken, http.StatusConflict, "email_taken"},
		{ErrEmailAlreadyVerified, http.StatusConflict, "email_already_verified"},
		{ErrCodeExpired, http.StatusGone, "code_expired"},
		{ErrInternal, http.StatusInternalServerError, "internal_error"},
		{ErrUnavailable, http.StatusServiceUnavailable, "service_unavailable"},
		{Validation(), http.StatusUnprocessableEntity, "validation_failed"},
		{InvalidCode(1), http.StatusUnprocessableEntity, "invalid_code"},
		{RateLimited(time.Minute), http.StatusTooManyRequests, "rate_limited"},
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.err.Status != c.status || c.err.Code != c.code || c.err.Title == "" {
			t.Errorf("%s: got %d %q title %q, want %d %q", c.code, c.err.Status, c.err.Code, c.err.Title, c.status, c.code)
		}
		if seen[c.err.Code] {
			t.Errorf("duplicate code %q", c.err.Code)
		}
		seen[c.err.Code] = true
	}
}

func TestSentinelSurvivesWrapping(t *testing.T) {
	err := fmt.Errorf("login: %w", ErrInvalidCredentials)
	var ae *Error
	if !errors.As(err, &ae) || ae != ErrInvalidCredentials {
		t.Fatal("errors.As did not find the wrapped sentinel")
	}
	if !errors.Is(err, ErrInvalidCredentials) || errors.Is(err, ErrUnauthorized) {
		t.Fatal("errors.Is must match the wrapped sentinel and only it")
	}
}

func TestValidationKeepsFields(t *testing.T) {
	e := Validation(FieldError{Field: "password", Code: FieldTooShort}, FieldError{Field: "email", Code: FieldInvalidEmail})
	if len(e.Fields) != 2 || e.Fields[0] != (FieldError{Field: "password", Code: "too_short"}) || e.Fields[1].Code != "invalid_email" {
		t.Fatalf("fields %+v", e.Fields)
	}
}

func TestInvalidCodeCarriesAttempts(t *testing.T) {
	last := InvalidCode(0)
	if last.AttemptsLeft == nil || *last.AttemptsLeft != 0 {
		t.Fatal("attempts_left 0 must be present, not dropped")
	}
	a, b := InvalidCode(1), InvalidCode(2)
	if *a.AttemptsLeft != 1 || *b.AttemptsLeft != 2 || a.AttemptsLeft == b.AttemptsLeft {
		t.Fatal("each error needs its own attempts counter")
	}
}

func TestRateLimitedRetryAfter(t *testing.T) {
	if got := RateLimited(90 * time.Second).RetryAfter; got != 90*time.Second {
		t.Fatalf("RetryAfter = %v, want 90s", got)
	}
	for _, d := range []time.Duration{0, -time.Second, time.Millisecond} {
		if got := RateLimited(d).RetryAfter; got != time.Second {
			t.Errorf("RateLimited(%v).RetryAfter = %v, want 1s", d, got)
		}
	}
}
