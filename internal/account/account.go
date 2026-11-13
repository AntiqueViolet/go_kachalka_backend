// Package account implements operations on the signed-in user's profile.
package account

import (
	"context"
	"log/slog"
	"time"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/apperr"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/clock"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/codes"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/otp"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/password"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/ratelimit"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/token"
)

type Deps struct {
	Store  *store.Store
	Hasher *password.Hasher
	OTP    *otp.Service
	Codes  *codes.Sender
	Clock  clock.Clock
	Log    *slog.Logger
	// PasswordFailures limits wrong current-password attempts per user (password change, email
	// change, account deletion); nil disables the limit.
	PasswordFailures *ratelimit.Limiter
}

type Service struct{ d Deps }

func New(d Deps) *Service { return &Service{d: d} }

// CodeSent tells the client how long the code lives and when it may ask for another.
type CodeSent struct {
	ExpiresIn   time.Duration
	ResendAfter time.Duration
}

func (s *Service) codeSent() CodeSent {
	return CodeSent{ExpiresIn: otp.TTL, ResendAfter: s.d.Codes.Cooldown()}
}

func (s *Service) Me(ctx context.Context, a token.Access) (store.User, error) {
	u, err := s.d.Store.GetUserByID(ctx, a.UserID)
	if store.IsNotFound(err) {
		return store.User{}, apperr.ErrUnauthorized
	}
	return u, err
}

// activeUser is Me plus a database check that the token's session is still live.
// Sensitive operations use it instead of trusting a possibly revoked 15-minute access token.
func (s *Service) activeUser(ctx context.Context, a token.Access) (store.User, error) {
	sess, err := s.d.Store.GetSession(ctx, a.SessionID)
	if store.IsNotFound(err) {
		return store.User{}, apperr.ErrUnauthorized
	}
	if err != nil {
		return store.User{}, err
	}
	if sess.UserID != a.UserID || sess.RevokedAt != nil || !s.d.Clock.Now().Before(sess.ExpiresAt) {
		return store.User{}, apperr.ErrUnauthorized
	}
	return s.Me(ctx, a)
}

// checkPassword verifies the current password. The attempt is reserved before the check, so
// parallel guesses can't all slip past the limit; only a wrong password keeps it spent.
func (s *Service) checkPassword(ctx context.Context, u store.User, pw string) error {
	refund := func() {}
	if l := s.d.PasswordFailures; l != nil {
		r, allowed, wait := l.Acquire(u.ID.String())
		if !allowed {
			return apperr.RateLimited(wait)
		}
		refund = r
	}
	ok, _, err := s.d.Hasher.Verify(ctx, pw, u.PasswordHash)
	if err != nil {
		refund()
		return err
	}
	if !ok {
		return apperr.ErrWrongPassword
	}
	refund()
	return nil
}

// ChangePassword keeps the current session and revokes all others.
func (s *Service) ChangePassword(ctx context.Context, a token.Access, oldPassword, newPassword string) error {
	if fe := password.FieldError("new_password", newPassword); fe != nil {
		return apperr.Validation(*fe)
	}
	u, err := s.activeUser(ctx, a)
	if err != nil {
		return err
	}
	if err := s.checkPassword(ctx, u, oldPassword); err != nil {
		return err
	}
	hash, err := s.d.Hasher.Hash(ctx, newPassword)
	if err != nil {
		return err
	}
	now := s.d.Clock.Now()
	return s.d.Store.InTx(ctx, func(q *store.Queries) error {
		if err := q.UpdatePasswordHash(ctx, store.UpdatePasswordHashParams{ID: u.ID, PasswordHash: hash, Now: now}); err != nil {
			return err
		}
		return q.RevokeUserSessionsExcept(ctx, store.RevokeUserSessionsExceptParams{
			UserID: u.ID, KeepID: a.SessionID, Now: now, Reason: "password_change",
		})
	})
}

// Delete removes the user; sessions and codes go with it (ON DELETE CASCADE).
func (s *Service) Delete(ctx context.Context, a token.Access, pw string) error {
	u, err := s.activeUser(ctx, a)
	if err != nil {
		return err
	}
	if err := s.checkPassword(ctx, u, pw); err != nil {
		return err
	}
	return s.d.Store.DeleteUser(ctx, u.ID)
}
