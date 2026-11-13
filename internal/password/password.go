// Package password validates and hashes passwords with argon2id.
package password

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/apperr"
)

// Password length limits, in characters.
const (
	MinLength = 8
	MaxLength = 128
)

var (
	ErrTooShort = errors.New("password too short")
	ErrTooLong  = errors.New("password too long")
	// ErrBusy means the context ended while waiting for a free hashing slot.
	ErrBusy = errors.New("password hasher busy")
	// ErrMalformedHash means a stored hash is not a canonical argon2id PHC string with sane parameters.
	ErrMalformedHash = errors.New("malformed password hash")
)

// Validate checks length only (NIST SP 800-63B: no composition rules). Characters are counted
// after NFKC normalization, in the form that gets hashed.
func Validate(pw string) error {
	n := utf8.RuneCountInString(norm.NFKC.String(pw))
	switch {
	case n < MinLength:
		return ErrTooShort
	case n > MaxLength:
		return ErrTooLong
	}
	return nil
}

// FieldError converts a Validate failure into an API field error; nil when pw is valid.
func FieldError(field, pw string) *apperr.FieldError {
	switch err := Validate(pw); {
	case err == nil:
		return nil
	case errors.Is(err, ErrTooLong):
		return &apperr.FieldError{Field: field, Code: apperr.FieldTooLong}
	default:
		return &apperr.FieldError{Field: field, Code: apperr.FieldTooShort}
	}
}

// Params are argon2id parameters; Memory is in KiB.
type Params struct {
	Memory  uint32
	Time    uint32
	Threads uint8
	SaltLen uint32
	KeyLen  uint32
}

// DefaultParams follow the OWASP argon2id recommendation (19 MiB, 2 passes, 1 lane).
var DefaultParams = Params{Memory: 19456, Time: 2, Threads: 1, SaltLen: 16, KeyLen: 32}

// validate rejects parameters that would panic in argon2, exhaust memory or CPU, or weaken
// the hash. Stored hashes go through it too: a corrupted row must not take the process down.
func (p Params) validate() error {
	switch {
	case p.Time < 1 || p.Time > 10,
		p.Threads < 1 || p.Threads > 16,
		p.Memory < 8*uint32(p.Threads) || p.Memory > 256*1024,
		p.SaltLen < 8 || p.SaltLen > 64,
		p.KeyLen < 16 || p.KeyLen > 64:
		return fmt.Errorf("password: argon2id parameters out of range: %+v", p)
	}
	return nil
}

// Hasher hashes and verifies passwords. At most concurrency hashes run at once, so peak
// memory is about concurrency × Params.Memory. Passwords are NFKC-normalized first
// (NIST SP 800-63B), so one visible password typed on different keyboards gives one hash.
type Hasher struct {
	params Params
	slots  chan struct{}
	dummy  string
}

func NewHasher(p Params, concurrency int) (*Hasher, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	if concurrency < 1 {
		return nil, fmt.Errorf("password: concurrency must be >= 1, got %d", concurrency)
	}
	h := &Hasher{params: p, slots: make(chan struct{}, concurrency)}
	dummy, err := h.Hash(context.Background(), rand.Text())
	if err != nil {
		return nil, err
	}
	h.dummy = dummy
	return h, nil
}

func (h *Hasher) acquire(ctx context.Context) error {
	select {
	case h.slots <- struct{}{}:
		return nil
	default:
	}
	select {
	case h.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ErrBusy
	}
}

func (h *Hasher) release() { <-h.slots }

// derive runs argon2id while holding a slot. The deferred release frees it even if argon2 panics.
func (h *Hasher) derive(ctx context.Context, pw string, salt []byte, p Params) ([]byte, error) {
	if err := h.acquire(ctx); err != nil {
		return nil, err
	}
	defer h.release()
	return argon2.IDKey([]byte(norm.NFKC.String(pw)), salt, p.Time, p.Memory, p.Threads, p.KeyLen), nil
}

// Hash returns the password in PHC format: $argon2id$v=19$m=..,t=..,p=..$salt$key.
func (h *Hasher) Hash(ctx context.Context, pw string) (string, error) {
	salt := make([]byte, h.params.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := h.derive(ctx, pw, salt, h.params)
	if err != nil {
		return "", err
	}
	return encode(h.params, salt, key), nil
}

// Verify reports whether pw matches encoded. needsRehash is true only when the password
// matched and encoded was made with other parameters.
func (h *Hasher) Verify(ctx context.Context, pw, encoded string) (ok, needsRehash bool, err error) {
	p, salt, key, err := decode(encoded)
	if err != nil {
		return false, false, err
	}
	other, err := h.derive(ctx, pw, salt, p)
	if err != nil {
		return false, false, err
	}
	ok = subtle.ConstantTimeCompare(key, other) == 1
	return ok, ok && p != h.params, nil
}

// VerifyDummy spends the same time as Verify so responses don't reveal whether an account exists.
func (h *Hasher) VerifyDummy(ctx context.Context, pw string) error {
	_, _, err := h.Verify(ctx, pw, h.dummy)
	return err
}

func encode(p Params, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

// decode accepts only the canonical form produced by encode, with parameters within bounds.
func decode(encoded string) (Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return Params{}, nil, nil, ErrMalformedHash
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil ||
		parts[3] != fmt.Sprintf("m=%d,t=%d,p=%d", p.Memory, p.Time, p.Threads) {
		return Params{}, nil, nil, ErrMalformedHash
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil {
		return Params{}, nil, nil, ErrMalformedHash
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return Params{}, nil, nil, ErrMalformedHash
	}
	p.SaltLen = uint32(len(salt))
	p.KeyLen = uint32(len(key))
	if p.validate() != nil {
		return Params{}, nil, nil, ErrMalformedHash
	}
	return p, salt, key, nil
}
