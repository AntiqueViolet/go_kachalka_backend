package main

import (
	"bytes"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/token"
)

func TestGenerate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jwt.pem")
	var out bytes.Buffer
	if err := generate(path, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := token.LoadPrivateKey(path); err != nil {
		t.Fatalf("written key does not load: %v", err)
	}
	line := strings.TrimSpace(out.String())
	secret, ok := strings.CutPrefix(line, "APP_SECRET=")
	if !ok {
		t.Fatalf("output %q", line)
	}
	raw, err := base64.StdEncoding.DecodeString(secret)
	if err != nil || len(raw) != 32 {
		t.Fatalf("APP_SECRET decodes to %d bytes, %v", len(raw), err)
	}
	if err := generate(path, &out); err == nil {
		t.Fatal("existing key was overwritten")
	}
}
