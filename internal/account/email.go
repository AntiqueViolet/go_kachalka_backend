package account

import (
	"context"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/apperr"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/contact"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/otp"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/token"
)

// ChangeEmail replaces the email (unverified until confirmed) and sends a code to it.
// The password is required: otherwise a stolen access token could redirect password resets.
func (s *Service) ChangeEmail(ctx context.Context, a token.Access, rawEmail, pw string) (store.User, CodeSent, error) {
	email, err := contact.NormalizeEmail(rawEmail)
	if err != nil {
		return store.User{}, CodeSent{}, apperr.Validation(apperr.FieldError{Field: "email", Code: apperr.FieldInvalidEmail})
	}
	u, err := s.activeUser(ctx, a)
	if err != nil {
		return store.User{}, CodeSent{}, err
	}
	if err := s.checkPassword(ctx, u, pw); err != nil {
		return store.User{}, CodeSent{}, err
	}
	if email == u.Email {
		if u.EmailVerifiedAt != nil {
			return store.User{}, CodeSent{}, apperr.ErrEmailAlreadyVerified
		}
		if err := s.d.Codes.SendEmailCode(ctx, otp.PurposeEmailVerify, u.ID, u.Email); err != nil {
			return store.User{}, CodeSent{}, err
		}
		return u, s.codeSent(), nil
	}

	var issued otp.Issued
	err = s.d.Store.InTx(ctx, func(q *store.Queries) error {
		var err error
		if u, err = q.SetEmail(ctx, store.SetEmailParams{ID: u.ID, Email: email, Now: s.d.Clock.Now()}); err != nil {
			return err
		}
		// Issued in the same transaction: if the address is rate limited the change is rolled back.
		issued, err = s.d.OTP.Issue(ctx, q, otp.PurposeEmailVerify, email, &u.ID)
		return err
	})
	if err != nil {
		return store.User{}, CodeSent{}, err
	}
	// Delivery failure is logged by Codes; the user can ask for a resend.
	_ = s.d.Codes.DeliverEmailCode(ctx, otp.PurposeEmailVerify, email, issued.Code)
	return u, s.codeSent(), nil
}

func (s *Service) VerifyEmail(ctx context.Context, a token.Access, code string) (store.User, error) {
	u, err := s.Me(ctx, a)
	if err != nil {
		return store.User{}, err
	}
	if u.EmailVerifiedAt != nil {
		return store.User{}, apperr.ErrEmailAlreadyVerified
	}
	c, err := s.d.Store.LatestActiveUserCode(ctx, store.LatestActiveUserCodeParams{Purpose: string(otp.PurposeEmailVerify), UserID: u.ID})
	if store.IsNotFound(err) || (err == nil && c.Target != u.Email) {
		return store.User{}, apperr.ErrCodeExpired
	}
	if err != nil {
		return store.User{}, err
	}
	if err := s.d.OTP.Check(ctx, s.d.Store.Queries, c, code); err != nil {
		return store.User{}, err
	}
	// User row first, then the code row: the same order as ChangeEmail, so the two can't deadlock.
	err = s.d.Store.InTx(ctx, func(q *store.Queries) error {
		var err error
		u, err = q.MarkEmailVerified(ctx, store.MarkEmailVerifiedParams{ID: u.ID, Now: s.d.Clock.Now()})
		if store.IsUniqueViolation(err, store.ConstraintVerifiedEmail) {
			return apperr.ErrEmailTaken
		}
		if err != nil {
			return err
		}
		// The email may have changed since the code was looked up.
		if u.Email != c.Target {
			return apperr.ErrCodeExpired
		}
		return s.d.OTP.Consume(ctx, q, c.ID)
	})
	if err != nil {
		return store.User{}, err
	}
	return u, nil
}

func (s *Service) ResendEmailCode(ctx context.Context, a token.Access) (CodeSent, error) {
	u, err := s.Me(ctx, a)
	if err != nil {
		return CodeSent{}, err
	}
	if u.EmailVerifiedAt != nil {
		return CodeSent{}, apperr.ErrEmailAlreadyVerified
	}
	if err := s.d.Codes.SendEmailCode(ctx, otp.PurposeEmailVerify, u.ID, u.Email); err != nil {
		return CodeSent{}, err
	}
	return s.codeSent(), nil
}
