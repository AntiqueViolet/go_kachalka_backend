package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/apperr"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/contact"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/otp"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/password"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store"
)

// errResetCode is the one answer to a wrong, expired or used code and to an unknown address:
// distinct answers (or attempts_left) would tell whether the address belongs to an account.
var errResetCode = apperr.New(http.StatusUnprocessableEntity, "invalid_code", "Invalid code")

// ForgotPassword emails a reset code if the address is verified on some account. The answer
// never depends on whether it is, nor on send limits or delivery problems; the email goes out
// in the background so that the response time does not tell either.
func (s *Service) ForgotPassword(ctx context.Context, rawEmail string) (resendAfter time.Duration, err error) {
	email, err := contact.NormalizeEmail(rawEmail)
	if err != nil {
		return 0, apperr.Validation(apperr.FieldError{Field: "email", Code: apperr.FieldInvalidEmail})
	}
	resendAfter = s.d.Codes.Cooldown()
	u, err := s.d.Store.GetUserByVerifiedEmail(ctx, email)
	if store.IsNotFound(err) {
		return resendAfter, nil
	}
	if err != nil {
		return 0, err
	}
	s.d.Codes.SendEmailCodeInBackground(ctx, otp.PurposePasswordReset, u.ID, u.Email)
	return resendAfter, nil
}

type ResetInput struct {
	Email       string
	Code        string
	NewPassword string
}

// ResetPassword sets a new password and revokes every session of the user.
func (s *Service) ResetPassword(ctx context.Context, in ResetInput) error {
	var fields []apperr.FieldError
	email, err := contact.NormalizeEmail(in.Email)
	if err != nil {
		fields = append(fields, apperr.FieldError{Field: "email", Code: apperr.FieldInvalidEmail})
	}
	if fe := password.FieldError("new_password", in.NewPassword); fe != nil {
		fields = append(fields, *fe)
	}
	if len(fields) > 0 {
		return apperr.Validation(fields...)
	}

	u, err := s.d.Store.GetUserByVerifiedEmail(ctx, email)
	if store.IsNotFound(err) {
		return errResetCode
	}
	if err != nil {
		return err
	}
	c, err := s.d.Store.LatestActiveUserCode(ctx, store.LatestActiveUserCodeParams{Purpose: string(otp.PurposePasswordReset), UserID: u.ID})
	if store.IsNotFound(err) || (err == nil && c.Target != u.Email) { // a code sent to a previous address
		return errResetCode
	}
	if err != nil {
		return err
	}
	if err := s.d.OTP.Check(ctx, s.d.Store.Queries, c, in.Code); err != nil {
		return resetCodeError(err)
	}
	hash, err := s.d.Hasher.Hash(ctx, in.NewPassword)
	if err != nil {
		return err
	}
	now := s.d.Clock.Now()
	return s.d.Store.InTx(ctx, func(q *store.Queries) error {
		if err := s.d.OTP.Consume(ctx, q, c.ID); err != nil {
			return resetCodeError(err)
		}
		if err := q.UpdatePasswordHash(ctx, store.UpdatePasswordHashParams{ID: u.ID, PasswordHash: hash, Now: now}); err != nil {
			return err
		}
		return q.RevokeUserSessions(ctx, store.RevokeUserSessionsParams{UserID: u.ID, Now: now, Reason: "password_reset"})
	})
}

// resetCodeError hides why a code was refused, see errResetCode.
func resetCodeError(err error) error {
	var ae *apperr.Error
	if errors.As(err, &ae) && (ae.Code == "invalid_code" || ae.Code == apperr.ErrCodeExpired.Code) {
		return errResetCode
	}
	return err
}
