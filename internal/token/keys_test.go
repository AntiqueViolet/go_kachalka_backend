package token

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateKeyPEMRoundTrip(t *testing.T) {
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pemBytes, err := EncodePrivateKeyPEM(k)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "jwt.pem")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadPrivateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Equal(k) {
		t.Fatal("loaded key differs from generated key")
	}
}

func TestParsePrivateKeyPEMRejectsOtherKeys(t *testing.T) {
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ed, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ecPKCS8, _ := x509.MarshalPKCS8PrivateKey(ec)
	rsaPKCS8, _ := x509.MarshalPKCS8PrivateKey(rs)
	ecSEC1, _ := x509.MarshalECPrivateKey(ec)
	edPublic, _ := x509.MarshalPKIXPublicKey(ed.Public())
	cases := map[string][]byte{
		"garbage":            []byte("not a pem"),
		"empty":              nil,
		"ec pkcs8":           pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecPKCS8}),
		"rsa pkcs8":          pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rsaPKCS8}),
		"ec sec1":            pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: ecSEC1}),
		"ed25519 public key": pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: edPublic}),
	}
	for name, in := range cases {
		if _, err := ParsePrivateKeyPEM(in); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestLoadPrivateKeyErrorNamesTheFile(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "broken.pem")
	if err := os.WriteFile(bad, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPrivateKey(bad); err == nil || !strings.Contains(err.Error(), "broken.pem") {
		t.Fatalf("err = %v, want it to mention the file", err)
	}
}
