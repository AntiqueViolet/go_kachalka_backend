// Package auth implements registration, login, token refresh, logout and password reset.
package auth

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/apperr"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/clock"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/codes"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/otp"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/password"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/ratelimit"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/token"
)

const (
	// MaxSessions is the number of live sessions a user may have; older ones are revoked.
	MaxSessions   = 10
	maxDeviceName = 100
)

type Deps struct {
	Store         *store.Store
	Hasher        *password.Hasher
	Signer        *token.Signer
	Refresh       *token.RefreshCodec
	OTP           *otp.Service
	Codes         *codes.Sender
	Clock         clock.Clock
	Log           *slog.Logger
	LoginFailures *ratelimit.Limiter // failed logins per phone
	RefreshTTL    time.Duration
	RefreshGrace  time.Duration
}

type Service struct{ d Deps }

func New(d Deps) *Service { return &Service{d: d} }

type Tokens struct {
	Access           string
	AccessExpiresIn  time.Duration
	Refresh          string
	RefreshExpiresIn time.Duration
}

type Result struct {
	User   store.User
	Tokens Tokens
}

// newSession creates a session and trims the user's live sessions to MaxSessions. The caller
// holds the user row lock (Login takes it, Register has just inserted the row), so concurrent
// logins trim one after another and the limit stays exact.
func (s *Service) newSession(ctx context.Context, q *store.Queries, userID uuid.UUID, device string) (store.Session, error) {
	now := s.d.Clock.Now()
	id, err := uuid.NewV7()
	if err != nil {
		return store.Session{}, err
	}
	sess, err := q.CreateSession(ctx, store.CreateSessionParams{
		ID: id, UserID: userID, DeviceName: device, Now: now, ExpiresAt: now.Add(s.d.RefreshTTL),
	})
	if err != nil {
		return store.Session{}, err
	}
	err = q.RevokeExcessSessions(ctx, store.RevokeExcessSessionsParams{UserID: userID, Now: now, Keep: MaxSessions})
	return sess, err
}

func (s *Service) tokensFor(sess store.Session) (Tokens, error) {
	access, err := s.d.Signer.IssueAccess(sess.UserID, sess.ID)
	if err != nil {
		return Tokens{}, err
	}
	return Tokens{
		Access:           access,
		AccessExpiresIn:  s.d.Signer.AccessTTL(),
		Refresh:          s.d.Refresh.Encode(sess.ID, sess.Generation),
		RefreshExpiresIn: sess.ExpiresAt.Sub(s.d.Clock.Now()),
	}, nil
}

func normalizeDevice(name string) (string, *apperr.FieldError) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "unknown", nil
	case utf8.RuneCountInString(name) > maxDeviceName:
		return "", &apperr.FieldError{Field: "device_name", Code: apperr.FieldTooLong}
	}
	return name, nil
}

func invalidPhone() error {
	return apperr.Validation(apperr.FieldError{Field: "phone", Code: apperr.FieldInvalidPhone})
}
