package auth

import (
	"context"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/apperr"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/token"
)

// Refresh exchanges a refresh token for a new token pair (spec section 5.3).
func (s *Service) Refresh(ctx context.Context, raw string) (Tokens, error) {
	sid, gen, err := s.d.Refresh.Decode(raw)
	if err != nil {
		return Tokens{}, apperr.ErrInvalidRefreshToken
	}
	var out Tokens
	reused := false
	err = s.d.Store.InTx(ctx, func(q *store.Queries) error {
		sess, err := q.GetSessionForUpdate(ctx, sid)
		if store.IsNotFound(err) {
			return apperr.ErrInvalidRefreshToken
		}
		if err != nil {
			return err
		}
		now := s.d.Clock.Now()
		if sess.RevokedAt != nil || !now.Before(sess.ExpiresAt) {
			return apperr.ErrInvalidRefreshToken
		}
		switch token.Decide(gen, sess.Generation, sess.RotatedAt, now, s.d.RefreshGrace) {
		case token.DecisionRotate:
			sess, err = q.RotateSession(ctx, store.RotateSessionParams{ID: sid, Now: now, ExpiresAt: now.Add(s.d.RefreshTTL)})
			if err != nil {
				return err
			}
		case token.DecisionReplay:
			if err := q.TouchSession(ctx, store.TouchSessionParams{ID: sid, Now: now}); err != nil {
				return err
			}
		case token.DecisionReuse:
			// Commit the revocation, then report the reuse outside the transaction.
			reused = true
			return q.RevokeSession(ctx, store.RevokeSessionParams{ID: sid, Now: now, Reason: "refresh_reuse"})
		default:
			return apperr.ErrInvalidRefreshToken
		}
		out, err = s.tokensFor(sess)
		return err
	})
	if err != nil {
		return Tokens{}, err
	}
	if reused {
		s.d.Log.WarnContext(ctx, "refresh token reuse detected, session revoked", "session_id", sid)
		return Tokens{}, apperr.ErrRefreshTokenReused
	}
	return out, nil
}

// Logout revokes the current session, or all sessions of the user. It is idempotent.
func (s *Service) Logout(ctx context.Context, a token.Access, allDevices bool) error {
	now := s.d.Clock.Now()
	if allDevices {
		return s.d.Store.RevokeUserSessions(ctx, store.RevokeUserSessionsParams{UserID: a.UserID, Now: now, Reason: "logout_all"})
	}
	return s.d.Store.RevokeSession(ctx, store.RevokeSessionParams{ID: a.SessionID, Now: now, Reason: "logout"})
}
