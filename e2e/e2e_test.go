//go:build e2e

// Package e2e is a smoke test against the running dev stack (docker compose up).
// Run from the repository root: go test -tags e2e ./e2e/
package e2e

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

var (
	baseURL    = env("BASE_URL", "http://localhost:8080")
	mailpitURL = env("MAILPIT_URL", "http://localhost:8025")
	client     = &http.Client{Timeout: 10 * time.Second}
	codeRe     = regexp.MustCompile(`\b\d{6}\b`)
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func TestSmoke(t *testing.T) {
	n, err := rand.Int(rand.Reader, big.NewInt(10_000_000))
	if err != nil {
		t.Fatal(err)
	}
	phone := fmt.Sprintf("+7999%07d", n.Int64())
	email := fmt.Sprintf("e2e-%07d@example.com", n.Int64())
	const pass = "e2e password 123"

	st, body := call(t, "POST", "/v1/auth/register/start", "", map[string]any{"phone": phone})
	want(t, http.StatusAccepted, st, body)
	st, body = call(t, "POST", "/v1/auth/register/verify", "", map[string]any{
		"verification_id": body["verification_id"], "code": smsCode(t, phone),
	})
	want(t, http.StatusOK, st, body)
	st, body = call(t, "POST", "/v1/auth/register", "", map[string]any{
		"registration_token": body["registration_token"], "password": pass, "email": email, "device_name": "e2e",
	})
	want(t, http.StatusCreated, st, body)
	access, refresh := tokens(t, body)

	st, body = call(t, "POST", "/v1/me/email/verify", access, map[string]any{"code": emailCode(t, email)})
	want(t, http.StatusOK, st, body)
	if body["email_verified"] != true {
		t.Fatalf("email not verified: %v", body)
	}

	st, body = call(t, "POST", "/v1/auth/login", "", map[string]any{"phone": phone, "password": pass})
	want(t, http.StatusOK, st, body)
	_, loginRefresh := tokens(t, body)

	st, body = call(t, "POST", "/v1/auth/refresh", "", map[string]any{"refresh_token": refresh})
	want(t, http.StatusOK, st, body)
	access, rotated := tokens(t, body)
	if rotated == refresh {
		t.Fatal("refresh token was not rotated")
	}
	st, body = call(t, "GET", "/v1/me", access, nil)
	want(t, http.StatusOK, st, body)
	if body["phone"] != phone {
		t.Fatalf("me: %v", body)
	}

	st, body = call(t, "POST", "/v1/auth/logout", access, map[string]any{"all_devices": true})
	want(t, http.StatusNoContent, st, body)
	for _, rt := range []string{rotated, loginRefresh} {
		st, body = call(t, "POST", "/v1/auth/refresh", "", map[string]any{"refresh_token": rt})
		if st != http.StatusUnauthorized {
			t.Fatalf("refresh after logout from all devices: %d %v", st, body)
		}
	}

	st, body = call(t, "POST", "/v1/auth/login", "", map[string]any{"phone": phone, "password": "wrong password"})
	if st != http.StatusUnauthorized || body["code"] != "invalid_credentials" {
		t.Fatalf("wrong password: %d %v", st, body)
	}
}

func call(t *testing.T, method, path, bearer string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequestWithContext(context.Background(), method, baseURL+path, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v (is `docker compose up` running?)", method, path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// tokens extracts the token pair from an auth response.
func tokens(t *testing.T, body map[string]any) (access, refresh string) {
	t.Helper()
	pair, _ := body["tokens"].(map[string]any)
	access, _ = pair["access_token"].(string)
	refresh, _ = pair["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("no token pair in %v", body)
	}
	return access, refresh
}

func want(t *testing.T, status, got int, body map[string]any) {
	t.Helper()
	if got != status {
		t.Fatalf("status %d, want %d: %v", got, status, body)
	}
}

// smsCode reads the code the log SMS provider printed for phone.
func smsCode(t *testing.T, phone string) string {
	t.Helper()
	return eventually(t, "SMS code for "+phone, func() string {
		out, err := exec.Command("docker", "compose", "-f", "../docker-compose.yml", "logs", "--no-color", "--no-log-prefix", "api").Output()
		if err != nil {
			t.Fatalf("docker compose logs: %v", err)
		}
		code := ""
		sc := bufio.NewScanner(bytes.NewReader(out))
		for sc.Scan() {
			var line struct {
				Phone string `json:"phone"`
				Text  string `json:"text"`
			}
			if json.Unmarshal(sc.Bytes(), &line) == nil && line.Phone == phone {
				code = codeRe.FindString(line.Text)
			}
		}
		return code
	})
}

// emailCode reads the latest code sent to addr from Mailpit's API.
func emailCode(t *testing.T, addr string) string {
	t.Helper()
	return eventually(t, "email code for "+addr, func() string {
		var list struct {
			Messages []struct {
				ID string `json:"ID"`
			} `json:"messages"`
		}
		getJSON(t, mailpitURL+"/api/v1/search?query="+url.QueryEscape("to:"+addr), &list)
		if len(list.Messages) == 0 {
			return ""
		}
		var msg struct {
			Text string `json:"Text"`
		}
		getJSON(t, mailpitURL+"/api/v1/message/"+list.Messages[0].ID, &msg)
		return codeRe.FindString(msg.Text)
	})
}

func getJSON(t *testing.T, u string, v any) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
}

func eventually(t *testing.T, what string, f func() string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if s := strings.TrimSpace(f()); s != "" {
			return s
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("%s did not appear within 15 s", what)
	return ""
}
