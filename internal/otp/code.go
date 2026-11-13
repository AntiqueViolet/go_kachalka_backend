// Package otp implements one-time verification codes sent by SMS and email.
package otp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
)

// Purpose says what a code confirms; the values match the CHECK constraint in the schema.
type Purpose string

const (
	PurposePhoneRegister Purpose = "phone_register"
	PurposeEmailVerify   Purpose = "email_verify"
	PurposePasswordReset Purpose = "password_reset"
)

const (
	// TTL is how long a code stays valid.
	TTL = 10 * time.Minute
	// MaxAttempts is the number of wrong guesses allowed per code.
	MaxAttempts = 5
)

var codeSpace = big.NewInt(1_000_000)

// Generate returns a uniformly random 6-digit code. rand.Int redraws out-of-range values,
// so there is no modulo bias; don't replace it with % over random bytes.
func Generate() (string, error) {
	n, err := rand.Int(rand.Reader, codeSpace)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// Hash binds the code to its record id: a stolen hash can't be matched against another record,
// and without the key the 10^6 code space can't be brute-forced offline. The id is fixed-width,
// so the encoding is unambiguous (":" is kept as written in the spec). Pass the code as entered.
func Hash(key []byte, id uuid.UUID, code string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(id[:])
	m.Write([]byte(":"))
	m.Write([]byte(code))
	return m.Sum(nil)
}

// Matches compares in constant time.
func Matches(key []byte, id uuid.UUID, code string, hash []byte) bool {
	return hmac.Equal(Hash(key, id, code), hash)
}
