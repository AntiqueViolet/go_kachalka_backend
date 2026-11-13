// Package codes issues verification codes and delivers them by SMS or email.
package codes

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/apperr"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/notify"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/otp"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store"
)

// backgroundTimeout bounds a background send: database work plus one SMTP delivery (10 s).
// Shutdown waits for background sends, so it also keeps the stop within the compose grace period.
const backgroundTimeout = 12 * time.Second

type Deps struct {
	Store *store.Store
	OTP   *otp.Service
	SMS   notify.SMSSender
	Email notify.EmailSender
	Texts notify.Texts
	Log   *slog.Logger
}

type Sender struct {
	d  Deps
	bg sync.WaitGroup // background sends, see SendEmailCodeInBackground
}

func NewSender(d Deps) *Sender { return &Sender{d: d} }

// Cooldown is how long a client must wait before asking for another code to the same address.
func (s *Sender) Cooldown() time.Duration { return s.d.OTP.Cooldown() }

// SendPhoneCode issues a registration code for phone and sends it by SMS.
// Delivery failure is reported as ErrUnavailable: without the SMS the user cannot continue.
func (s *Sender) SendPhoneCode(ctx context.Context, phone string) (uuid.UUID, error) {
	is, err := s.issue(ctx, otp.PurposePhoneRegister, phone, nil)
	if err != nil {
		return uuid.Nil, err
	}
	text, err := s.d.Texts.PhoneCode(is.Code)
	if err != nil {
		s.discard(ctx, is.ID)
		return uuid.Nil, err
	}
	if err := s.d.SMS.SendSMS(ctx, phone, text); err != nil {
		s.discard(ctx, is.ID)
		return uuid.Nil, s.failed(ctx, "sms", err)
	}
	return is.ID, nil
}

// SendEmailCode issues an email_verify or password_reset code for the user and emails it.
func (s *Sender) SendEmailCode(ctx context.Context, purpose otp.Purpose, userID uuid.UUID, email string) error {
	is, err := s.issue(ctx, purpose, email, &userID)
	if err != nil {
		return err
	}
	if err := s.DeliverEmailCode(ctx, purpose, email, is.Code); err != nil {
		s.discard(ctx, is.ID)
		return err
	}
	return nil
}

// SendEmailCodeInBackground runs SendEmailCode after the caller has answered, so neither the
// answer nor its timing tells whether the address belongs to an account. Errors are only logged.
func (s *Sender) SendEmailCodeInBackground(ctx context.Context, purpose otp.Purpose, userID uuid.UUID, email string) {
	ctx = context.WithoutCancel(ctx)
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		ctx, cancel := context.WithTimeout(ctx, backgroundTimeout)
		defer cancel()
		if err := s.SendEmailCode(ctx, purpose, userID, email); err != nil {
			s.d.Log.WarnContext(ctx, "code not sent", "purpose", purpose, "err", err)
		}
	}()
}

// Wait blocks until background sends have finished (graceful shutdown, tests).
func (s *Sender) Wait() { s.bg.Wait() }

// DeliverEmailCode emails a code that the caller issued inside its own transaction.
func (s *Sender) DeliverEmailCode(ctx context.Context, purpose otp.Purpose, email, code string) error {
	render := s.d.Texts.EmailVerification
	if purpose == otp.PurposePasswordReset {
		render = s.d.Texts.PasswordReset
	}
	subject, body, err := render(code)
	if err != nil {
		return err
	}
	if err := s.d.Email.SendEmail(ctx, email, subject, body); err != nil {
		return s.failed(ctx, "email", err)
	}
	return nil
}

// issue creates the code in a transaction of its own, so the per-address lock that otp.Issue
// takes covers both the send-limit check and the insert.
func (s *Sender) issue(ctx context.Context, purpose otp.Purpose, target string, userID *uuid.UUID) (otp.Issued, error) {
	var is otp.Issued
	err := s.d.Store.InTx(ctx, func(q *store.Queries) error {
		var err error
		is, err = s.d.OTP.Issue(ctx, q, purpose, target, userID)
		return err
	})
	return is, err
}

// discard deletes a code that was never delivered, so a provider failure does not use up the
// address's send limits.
func (s *Sender) discard(ctx context.Context, id uuid.UUID) {
	if err := s.d.Store.DeleteCode(context.WithoutCancel(ctx), id); err != nil {
		s.d.Log.WarnContext(ctx, "undelivered code not deleted", "err", err)
	}
}

func (s *Sender) failed(ctx context.Context, channel string, err error) error {
	s.d.Log.ErrorContext(ctx, "code delivery failed", "channel", channel, "err", err)
	return apperr.ErrUnavailable
}
