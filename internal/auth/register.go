package auth

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/apperr"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/contact"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/otp"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/password"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store"
)

type Started struct {
	VerificationID uuid.UUID
	ExpiresIn      time.Duration
	ResendAfter    time.Duration
}

// StartRegistration sends an SMS code to a phone number that is not registered yet.
func (s *Service) StartRegistration(ctx context.Context, rawPhone string) (Started, error) {
	phone, err := contact.NormalizePhone(rawPhone)
	if err != nil {
		return Started{}, invalidPhone()
	}
	switch _, err := s.d.Store.GetUserByPhone(ctx, phone); {
	case err == nil:
		return Started{}, apperr.ErrPhoneTaken
	case !store.IsNotFound(err):
		return Started{}, err
	}
	id, err := s.d.Codes.SendPhoneCode(ctx, phone)
	if err != nil {
		return Started{}, err
	}
	return Started{VerificationID: id, ExpiresIn: otp.TTL, ResendAfter: s.d.Codes.Cooldown()}, nil
}

type RegistrationToken struct {
	Token     string
	ExpiresIn time.Duration
}

// VerifyPhone checks the SMS code and returns a short-lived token proving phone ownership.
func (s *Service) VerifyPhone(ctx context.Context, verificationID uuid.UUID, code string) (RegistrationToken, error) {
	c, err := s.d.Store.GetCode(ctx, verificationID)
	if store.IsNotFound(err) || (err == nil && c.Purpose != string(otp.PurposePhoneRegister)) {
		return RegistrationToken{}, apperr.ErrCodeExpired
	}
	if err != nil {
		return RegistrationToken{}, err
	}
	if err := s.d.OTP.Check(ctx, s.d.Store.Queries, c, code); err != nil {
		return RegistrationToken{}, err
	}
	if err := s.d.OTP.Consume(ctx, s.d.Store.Queries, c.ID); err != nil {
		return RegistrationToken{}, err
	}
	tok, err := s.d.Signer.IssueRegistration(c.Target)
	if err != nil {
		return RegistrationToken{}, err
	}
	return RegistrationToken{Token: tok, ExpiresIn: s.d.Signer.RegistrationTTL()}, nil
}

type RegisterInput struct {
	RegistrationToken string
	Password          string
	Email             string
	DeviceName        string
}

// Register creates the account and its first session, then emails a verification code
// (best effort: the user can request a resend).
func (s *Service) Register(ctx context.Context, in RegisterInput) (Result, error) {
	phone, err := s.d.Signer.ParseRegistration(in.RegistrationToken)
	if err != nil {
		return Result{}, apperr.ErrInvalidRegistrationToken
	}
	var fields []apperr.FieldError
	email, err := contact.NormalizeEmail(in.Email)
	if err != nil {
		fields = append(fields, apperr.FieldError{Field: "email", Code: apperr.FieldInvalidEmail})
	}
	if fe := password.FieldError("password", in.Password); fe != nil {
		fields = append(fields, *fe)
	}
	device, fe := normalizeDevice(in.DeviceName)
	if fe != nil {
		fields = append(fields, *fe)
	}
	if len(fields) > 0 {
		return Result{}, apperr.Validation(fields...)
	}

	hash, err := s.d.Hasher.Hash(ctx, in.Password)
	if err != nil {
		return Result{}, err
	}
	userID, err := uuid.NewV7()
	if err != nil {
		return Result{}, err
	}
	var res Result
	err = s.d.Store.InTx(ctx, func(q *store.Queries) error {
		u, err := q.CreateUser(ctx, store.CreateUserParams{
			ID: userID, Phone: phone, PasswordHash: hash, Email: email, Now: s.d.Clock.Now(),
		})
		if store.IsUniqueViolation(err, store.ConstraintUserPhone) {
			return apperr.ErrPhoneTaken
		}
		if err != nil {
			return err
		}
		sess, err := s.newSession(ctx, q, u.ID, device)
		if err != nil {
			return err
		}
		res.User = u
		res.Tokens, err = s.tokensFor(sess)
		return err
	})
	if err != nil {
		return Result{}, err
	}
	if err := s.d.Codes.SendEmailCode(ctx, otp.PurposeEmailVerify, res.User.ID, res.User.Email); err != nil {
		s.d.Log.WarnContext(ctx, "email verification code not sent after registration", "err", err)
	}
	return res, nil
}
