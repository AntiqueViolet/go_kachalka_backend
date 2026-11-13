// Package apperr defines client-facing errors with stable codes.
// The HTTP layer renders them as RFC 9457 problem details; any other error becomes 500.
package apperr

import (
	"net/http"
	"time"
)

// Field error codes used inside validation_failed.
const (
	FieldInvalidPhone = "invalid_phone"
	FieldInvalidEmail = "invalid_email"
	FieldTooShort     = "too_short"
	FieldTooLong      = "too_long"
	FieldRequired     = "required"
)

type FieldError struct {
	Field string `json:"field"`
	Code  string `json:"code"`
}

// Error is an error the client sees. Code is stable and machine-readable, Title is for people.
// Fields is set only for validation_failed, AttemptsLeft only for invalid_code,
// RetryAfter only for rate_limited.
type Error struct {
	Status       int
	Code         string
	Title        string
	Fields       []FieldError
	AttemptsLeft *int
	RetryAfter   time.Duration
}

func (e *Error) Error() string { return e.Code }

func New(status int, code, title string) *Error {
	return &Error{Status: status, Code: code, Title: title}
}

// Validation builds a 422 validation_failed error listing the bad fields.
func Validation(fields ...FieldError) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Title: "Validation failed", Fields: fields}
}

// InvalidCode is returned for a wrong verification code; attemptsLeft may be 0 on the last try.
func InvalidCode(attemptsLeft int) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "invalid_code", Title: "Invalid code", AttemptsLeft: &attemptsLeft}
}

// RateLimited is returned when a limit is exceeded; RetryAfter goes to the Retry-After header.
// It is at least one second, so a wait computed at a window boundary never drops the header.
func RateLimited(retryAfter time.Duration) *Error {
	return &Error{Status: http.StatusTooManyRequests, Code: "rate_limited", Title: "Too many requests", RetryAfter: max(retryAfter, time.Second)}
}

// Shared sentinel errors. Never mutate them; compare with errors.Is.
var (
	ErrBadRequest               = New(http.StatusBadRequest, "bad_request", "Bad request")
	ErrUnauthorized             = New(http.StatusUnauthorized, "unauthorized", "Unauthorized")
	ErrInvalidCredentials       = New(http.StatusUnauthorized, "invalid_credentials", "Invalid phone or password")
	ErrInvalidRegistrationToken = New(http.StatusUnauthorized, "invalid_registration_token", "Invalid registration token")
	ErrInvalidRefreshToken      = New(http.StatusUnauthorized, "invalid_refresh_token", "Invalid refresh token")
	ErrRefreshTokenReused       = New(http.StatusUnauthorized, "refresh_token_reused", "Refresh token reused")
	ErrWrongPassword            = New(http.StatusForbidden, "wrong_password", "Wrong password")
	ErrPhoneTaken               = New(http.StatusConflict, "phone_taken", "Phone number already registered")
	ErrEmailTaken               = New(http.StatusConflict, "email_taken", "Email already used by another account")
	ErrEmailAlreadyVerified     = New(http.StatusConflict, "email_already_verified", "Email already verified")
	ErrCodeExpired              = New(http.StatusGone, "code_expired", "Code expired")
	ErrInternal                 = New(http.StatusInternalServerError, "internal_error", "Internal error")
	ErrUnavailable              = New(http.StatusServiceUnavailable, "service_unavailable", "Service unavailable")
)
