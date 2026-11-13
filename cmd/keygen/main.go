// Command keygen creates the production secrets: an Ed25519 private key for signing JWTs
// (written to a file) and APP_SECRET (printed to stdout).
//
//	keygen -jwt-key secrets/jwt.pem
package main

import (
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/token"
)

func main() {
	keyPath := flag.String("jwt-key", "jwt.pem", "where to write the JWT private key; the file must not exist")
	flag.Parse()
	if err := generate(*keyPath, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "keygen:", err)
		os.Exit(1)
	}
}

func generate(keyPath string, out io.Writer) error {
	key, err := token.GenerateKey()
	if err != nil {
		return err
	}
	pemBytes, err := token.EncodePrivateKeyPEM(key)
	if err != nil {
		return err
	}
	// Draw the secret before creating the file: a rand failure must not leave a key
	// behind that O_EXCL then refuses to overwrite on the next run.
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	if err := writeNew(keyPath, pemBytes); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "APP_SECRET=%s\n", base64.StdEncoding.EncodeToString(secret))
	return err
}

// writeNew creates path (0600, parent directory 0700) and removes it again if the
// write does not complete, so a half-written key never blocks the next run.
func writeNew(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// O_EXCL: never overwrite an existing key, that would log every user out.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
