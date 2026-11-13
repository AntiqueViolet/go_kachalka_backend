package otp

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/apperr"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/clock"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store"
)

const sendWindow = time.Hour

type Limits struct {
	Cooldown         time.Duration // minimum gap between codes to one address
	PerTargetPerHour int
}

// Service issues and checks codes stored in verification_codes.
type Service struct {
	key    []byte
	limits Limits
	clock  clock.Clock
}

func NewService(key []byte, limits Limits, c clock.Clock) *Service {
	return &Service{key: key, limits: limits, clock: c}
}

func (s *Service) Cooldown() time.Duration { return s.limits.Cooldown }

type Issued struct {
	ID   uuid.UUID
	Code string
}

// Issue creates a code after checking send limits for target. Earlier active codes with the
// same purpose are invalidated: per user when userID is set, otherwise per target.
// q must be a transaction: Issue locks target until it ends, so parallel requests can't all pass
// the limit check or leave several active codes. The caller sends Code after the commit.
func (s *Service) Issue(ctx context.Context, q *store.Queries, purpose Purpose, target string, userID *uuid.UUID) (Issued, error) {
	if err := q.LockCodeTarget(ctx, target); err != nil {
		return Issued{}, err
	}
	now := s.clock.Now()
	if err := s.checkLimits(ctx, q, target, now); err != nil {
		return Issued{}, err
	}
	var err error
	if userID != nil {
		err = q.InvalidateUserCodes(ctx, store.InvalidateUserCodesParams{Now: now, Purpose: string(purpose), UserID: *userID})
	} else {
		err = q.InvalidateTargetCodes(ctx, store.InvalidateTargetCodesParams{Now: now, Purpose: string(purpose), Target: target})
	}
	if err != nil {
		return Issued{}, err
	}
	code, err := Generate()
	if err != nil {
		return Issued{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Issued{}, err
	}
	if _, err := q.CreateCode(ctx, store.CreateCodeParams{
		ID: id, Purpose: string(purpose), Target: target, UserID: userID,
		CodeHash: Hash(s.key, id, code), ExpiresAt: now.Add(TTL), Now: now,
	}); err != nil {
		return Issued{}, err
	}
	return Issued{ID: id, Code: code}, nil
}

func (s *Service) checkLimits(ctx context.Context, q *store.Queries, target string, now time.Time) error {
	st, err := q.CodeSendStats(ctx, store.CodeSendStatsParams{Target: target, Since: now.Add(-sendWindow)})
	if err != nil {
		return err
	}
	if st.Sent == 0 {
		return nil
	}
	if wait := st.Latest.Add(s.limits.Cooldown).Sub(now); wait > 0 {
		return apperr.RateLimited(wait)
	}
	if st.Sent >= int64(s.limits.PerTargetPerHour) {
		return apperr.RateLimited(st.Oldest.Add(sendWindow).Sub(now))
	}
	return nil
}

// Check verifies input against c. Every call burns one attempt first, atomically in the database,
// so parallel requests can't try more than MaxAttempts codes. Pass the autocommit store queries,
// not a transaction, so a later rollback can't return the attempt. Check does not consume the
// code: the caller calls Consume inside its own transaction.
func (s *Service) Check(ctx context.Context, q *store.Queries, c store.VerificationCode, input string) error {
	attempts, err := q.ClaimCodeAttempt(ctx, store.ClaimCodeAttemptParams{ID: c.ID, Now: s.clock.Now(), MaxAttempts: MaxAttempts})
	if store.IsNotFound(err) {
		return apperr.ErrCodeExpired
	}
	if err != nil {
		return err
	}
	if Matches(s.key, c.ID, input, c.CodeHash) {
		return nil
	}
	return apperr.InvalidCode(max(MaxAttempts-int(attempts), 0))
}

// Consume marks the code used. It fails with ErrCodeExpired if a concurrent request won.
func (s *Service) Consume(ctx context.Context, q *store.Queries, id uuid.UUID) error {
	n, err := q.ConsumeCode(ctx, store.ConsumeCodeParams{ID: id, Now: s.clock.Now()})
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.ErrCodeExpired
	}
	return nil
}
