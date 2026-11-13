package token

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/clock"
)

const (
	audAPI          = "api"
	audRegistration = "registration"
	leeway          = 5 * time.Second
	// maxTokenLen rejects oversized input before any decoding; real tokens are about 300 bytes.
	maxTokenLen = 4096
)

// ErrInvalid is returned for every token that fails verification. The underlying reason is
// wrapped for server-side logs; clients only ever see 401.
var ErrInvalid = errors.New("invalid token")

// SignerConfig configures a Signer. Clock may be nil (the wall clock is used); the rest is required.
type SignerConfig struct {
	Key             ed25519.PrivateKey
	Issuer          string
	AccessTTL       time.Duration
	RegistrationTTL time.Duration
	Clock           clock.Clock
}

// Signer issues and verifies EdDSA JWTs. Access and registration tokens differ by audience,
// so one can never be accepted as the other. Tokens stay valid for up to 5 s past their TTL
// (clock-skew leeway).
type Signer struct {
	cfg SignerConfig
	pub ed25519.PublicKey
	kid string
}

// NewSigner panics on an invalid configuration. It runs once at startup, and an empty Issuer
// would otherwise silently switch the issuer check off.
func NewSigner(cfg SignerConfig) *Signer {
	switch {
	case len(cfg.Key) != ed25519.PrivateKeySize:
		panic("token: SignerConfig.Key must be an Ed25519 private key")
	case cfg.Issuer == "":
		panic("token: SignerConfig.Issuer must not be empty")
	case cfg.AccessTTL <= 0 || cfg.RegistrationTTL <= 0:
		panic("token: SignerConfig TTLs must be positive")
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	pub := cfg.Key.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(pub)
	return &Signer{cfg: cfg, pub: pub, kid: hex.EncodeToString(sum[:8])}
}

// Access is the verified content of an access token.
type Access struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
}

type accessClaims struct {
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

func (s *Signer) AccessTTL() time.Duration       { return s.cfg.AccessTTL }
func (s *Signer) RegistrationTTL() time.Duration { return s.cfg.RegistrationTTL }

func (s *Signer) IssueAccess(userID, sessionID uuid.UUID) (string, error) {
	return s.sign(accessClaims{
		SessionID:        sessionID.String(),
		RegisteredClaims: s.registered(userID.String(), audAPI, s.cfg.AccessTTL),
	})
}

func (s *Signer) ParseAccess(raw string) (Access, error) {
	var c accessClaims
	if err := s.parse(raw, audAPI, &c); err != nil {
		return Access{}, err
	}
	uid, ok := parseID(c.Subject)
	if !ok {
		return Access{}, ErrInvalid
	}
	sid, ok := parseID(c.SessionID)
	if !ok {
		return Access{}, ErrInvalid
	}
	return Access{UserID: uid, SessionID: sid}, nil
}

// parseID accepts only the canonical form this package writes, never the nil UUID.
func parseID(s string) (uuid.UUID, bool) {
	id, err := uuid.Parse(s)
	return id, err == nil && id != uuid.Nil && id.String() == s
}

// IssueRegistration proves that the holder confirmed ownership of phone (E.164).
func (s *Signer) IssueRegistration(phone string) (string, error) {
	if phone == "" {
		return "", errors.New("token: empty phone")
	}
	c := s.registered(phone, audRegistration, s.cfg.RegistrationTTL)
	return s.sign(&c)
}

// ParseRegistration returns the phone the token was issued for. Only this server signs these
// tokens, so the phone is trusted as already normalized.
func (s *Signer) ParseRegistration(raw string) (phone string, err error) {
	var c jwt.RegisteredClaims
	if err := s.parse(raw, audRegistration, &c); err != nil {
		return "", err
	}
	if c.Subject == "" {
		return "", ErrInvalid
	}
	return c.Subject, nil
}

func (s *Signer) registered(sub, aud string, ttl time.Duration) jwt.RegisteredClaims {
	now := s.cfg.Clock.Now()
	return jwt.RegisteredClaims{
		Issuer:    s.cfg.Issuer,
		Subject:   sub,
		Audience:  jwt.ClaimStrings{aud},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	}
}

func (s *Signer) sign(claims jwt.Claims) (string, error) {
	t := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	t.Header["kid"] = s.kid
	return t.SignedString(s.cfg.Key)
}

func (s *Signer) parse(raw, aud string, claims jwt.Claims) error {
	if len(raw) > maxTokenLen {
		return ErrInvalid
	}
	p := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(s.cfg.Issuer),
		jwt.WithAudience(aud),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(leeway),
		jwt.WithTimeFunc(s.cfg.Clock.Now),
	)
	if _, err := p.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) { return s.pub, nil }); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	// Exactly one audience: a token valid for both purposes must not exist.
	if got, err := claims.GetAudience(); err != nil || len(got) != 1 {
		return ErrInvalid
	}
	return nil
}
