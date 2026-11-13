package auth

import (
	"context"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/apperr"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/contact"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store"
)

type LoginInput struct {
	Phone      string
	Password   string
	DeviceName string
}

// Login checks phone and password and opens a new session.
// An unknown phone and a wrong password are indistinguishable, including by timing.
func (s *Service) Login(ctx context.Context, in LoginInput) (Result, error) {
	phone, err := contact.NormalizePhone(in.Phone)
	if err != nil {
		return Result{}, invalidPhone()
	}
	device, fe := normalizeDevice(in.DeviceName)
	if fe != nil {
		return Result{}, apperr.Validation(*fe)
	}
	// The attempt is reserved before the password is checked, so parallel guesses can't all slip
	// past the limit; anything but a wrong password gives it back, so only failures count.
	refund, allowed, wait := s.d.LoginFailures.Acquire(phone)
	if !allowed {
		return Result{}, apperr.RateLimited(wait)
	}
	failed := false
	defer func() {
		if !failed {
			refund()
		}
	}()

	u, err := s.d.Store.GetUserByPhone(ctx, phone)
	if store.IsNotFound(err) {
		if err := s.d.Hasher.VerifyDummy(ctx, in.Password); err != nil {
			return Result{}, err
		}
		failed = true
		return Result{}, apperr.ErrInvalidCredentials
	}
	if err != nil {
		return Result{}, err
	}
	ok, rehash, err := s.d.Hasher.Verify(ctx, in.Password, u.PasswordHash)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		failed = true
		return Result{}, apperr.ErrInvalidCredentials
	}
	res := Result{User: u}
	err = s.d.Store.InTx(ctx, func(q *store.Queries) error {
		// The password may have been reset or changed while it was being checked. The lock makes
		// the reset wait for this session (and revoke it) or makes this login see the new hash;
		// it also serialises the session-limit trim of parallel logins.
		hash, err := q.LockUser(ctx, u.ID)
		if store.IsNotFound(err) {
			return apperr.ErrInvalidCredentials
		}
		if err != nil {
			return err
		}
		if hash != u.PasswordHash {
			// Changed since it was checked: a reset or password change (the password no longer
			// matches) or a parallel login's rehash (it still does). Rare, so checked again here.
			ok, _, err := s.d.Hasher.Verify(ctx, in.Password, hash)
			if err != nil {
				return err
			}
			if !ok {
				return apperr.ErrInvalidCredentials
			}
		}
		sess, err := s.newSession(ctx, q, u.ID, device)
		if err != nil {
			return err
		}
		res.Tokens, err = s.tokensFor(sess)
		return err
	})
	if err != nil {
		return Result{}, err
	}
	if rehash {
		s.rehash(ctx, u, in.Password)
	}
	return res, nil
}

// rehash upgrades a hash made with outdated parameters; failure only costs another try next login.
// It leaves the hash alone if the password changed in the meantime.
func (s *Service) rehash(ctx context.Context, u store.User, pw string) {
	hash, err := s.d.Hasher.Hash(ctx, pw)
	if err == nil {
		_, err = s.d.Store.RehashPassword(ctx, store.RehashPasswordParams{ID: u.ID, OldHash: u.PasswordHash, NewHash: hash, Now: s.d.Clock.Now()})
	}
	if err != nil {
		s.d.Log.WarnContext(ctx, "password rehash failed", "err", err)
	}
}
