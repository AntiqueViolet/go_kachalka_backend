// Package token issues and verifies access, registration and refresh tokens.
package token

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"

	"github.com/golang-jwt/jwt/v5"
)

// GenerateKey creates a new Ed25519 key for signing JWTs.
func GenerateKey() (ed25519.PrivateKey, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	return priv, err
}

// EncodePrivateKeyPEM encodes the key as PKCS#8 PEM ("PRIVATE KEY").
func EncodePrivateKeyPEM(k ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// ParsePrivateKeyPEM accepts only a PKCS#8 Ed25519 private key; RSA, EC and public keys are rejected.
func ParsePrivateKeyPEM(b []byte) (ed25519.PrivateKey, error) {
	k, err := jwt.ParseEdPrivateKeyFromPEM(b)
	if err != nil {
		return nil, err
	}
	ek, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("token: key is %T, want ed25519", k)
	}
	return ek, nil
}

// LoadPrivateKey reads the key written by cmd/keygen (JWT_PRIVATE_KEY_FILE).
func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("token: load JWT key: %w", err)
	}
	k, err := ParsePrivateKeyPEM(b)
	if err != nil {
		return nil, fmt.Errorf("token: load JWT key %q: %w", path, err)
	}
	return k, nil
}
