package config

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"
)

func devVars() map[string]string {
	return map[string]string{"DATABASE_URL": "postgres://localhost/test"}
}

// prodVars is a complete prod configuration apart from SMS: until a real provider is added,
// SMS_PROVIDER=log is the only error it produces.
func prodVars() map[string]string {
	v := devVars()
	maps.Copy(v, map[string]string{
		"APP_ENV":              "prod",
		"APP_SECRET":           "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		"JWT_PRIVATE_KEY_FILE": "/run/secrets/jwt.pem",
		"SMTP_HOST":            "smtp.example.com",
		"SMTP_FROM":            "noreply@example.com",
	})
	return v
}

func TestDevDefaults(t *testing.T) {
	c, err := LoadFrom(devVars())
	if err != nil {
		t.Fatal(err)
	}
	if c.Env != EnvDev || c.HTTPAddr != ":8080" || c.AccessTokenTTL != 15*time.Minute ||
		c.RefreshTokenTTL != 60*24*time.Hour || c.RefreshGrace != 30*time.Second {
		t.Fatalf("unexpected defaults: %v %v %v %v", c.HTTPAddr, c.AccessTokenTTL, c.RefreshTokenTTL, c.RefreshGrace)
	}
	if c.SMTP.Port != 587 || c.SMTP.TLS != "starttls" || c.Rate.LoginFailuresPerPhone != 5 || c.Rate.CodeCooldown != time.Minute {
		t.Fatalf("nested defaults not applied: %+v %+v", c.SMTP, c.Rate)
	}
	if !c.UsingDevSecret() || len(c.Keys.Refresh) != 32 || len(c.Keys.OTP) != 32 {
		t.Fatal("dev secret and derived keys expected")
	}
	if bytes.Equal(c.Keys.Refresh, c.Keys.OTP) {
		t.Fatal("derived keys must differ")
	}
	if c.Level != slog.LevelInfo {
		t.Fatalf("level = %v", c.Level)
	}
}

// Known answers (checked against an independent RFC 5869 implementation). A change here
// would log every user out and invalidate every pending code.
func TestKeyDerivationIsStable(t *testing.T) {
	c, err := LoadFrom(devVars())
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(c.Keys.Refresh); got != "47eceb0e3ab7a8d2866b4a593b9f702ed59bc8e2e227a4f53f43ec56fe86c316" {
		t.Errorf("refresh key %s", got)
	}
	if got := hex.EncodeToString(c.Keys.OTP); got != "2ea6e6a3a9b465eaa40c778587d7c12d6650520bf034e028ce8c0020c6d48a92" {
		t.Errorf("otp key %s", got)
	}
}

func TestDatabaseURLRequired(t *testing.T) {
	if _, err := LoadFrom(map[string]string{}); err == nil {
		t.Fatal("expected error without DATABASE_URL")
	}
	if _, err := LoadFrom(map[string]string{"DATABASE_URL": ""}); err == nil {
		t.Fatal("empty DATABASE_URL accepted")
	}
}

func TestLoadFromNilIgnoresProcessEnvironment(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/real")
	if _, err := LoadFrom(nil); err == nil {
		t.Fatal("LoadFrom(nil) read the process environment")
	}
}

func TestTrustedProxies(t *testing.T) {
	v := devVars()
	v["TRUSTED_PROXIES"] = "172.18.0.0/16, 10.0.0.5,10.1.2.3/8,::ffff:192.0.2.1,"
	c, err := LoadFrom(v)
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("172.18.0.0/16"),
		netip.MustParsePrefix("10.0.0.5/32"),
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("192.0.2.1/32"),
	}
	if !slices.Equal(c.ProxyPrefixes, want) {
		t.Fatalf("got %v, want %v", c.ProxyPrefixes, want)
	}
	for _, bad := range []string{"nonsense", "0.0.0.0/0", "::/0"} {
		v["TRUSTED_PROXIES"] = bad
		if _, err := LoadFrom(v); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXIES") {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

// Each bad value must be refused with a message that names the variable.
func TestInvalidValuesNameTheirVariable(t *testing.T) {
	for _, c := range []struct {
		vars map[string]string
		want string
	}{
		{map[string]string{"APP_ENV": "PROD"}, "APP_ENV"},
		{map[string]string{"APP_NAME": "Качалка Фитнес Клуб Москва"}, "APP_NAME"},
		{map[string]string{"APP_NAME": "Кач\nалка"}, "APP_NAME"},
		{map[string]string{"APP_NAME": "Кач" + string(rune(0x2028)) + "алка"}, "APP_NAME"}, // Unicode line separator
		{map[string]string{"SMS_PROVIDER": "twilio"}, "SMS_PROVIDER"},
		{map[string]string{"SMTP_TLS": "ssl"}, "SMTP_TLS"},
		{map[string]string{"SMTP_HOST": "smtp.example.com", "SMTP_FROM": "noreply@example.com", "SMTP_PORT": "70000"}, "SMTP_PORT"},
		{map[string]string{"SMTP_HOST": "smtp.example.com", "SMTP_FROM": "noreply@example.com", "SMTP_PORT": "0"}, "SMTP_PORT"},
		{map[string]string{"SMS_APP_HASH": "FA+9qCX9VS"}, "SMS_APP_HASH"},
		{map[string]string{"SMS_APP_HASH": "FA+9qCX9VSu\n"}, "SMS_APP_HASH"},
		{map[string]string{"SMTP_HOST": "smtp.example.com", "SMTP_FROM": "not an address"}, "SMTP_FROM"},
		{map[string]string{"ACCESS_TOKEN_TTL": "0s"}, "ACCESS_TOKEN_TTL"},
		{map[string]string{"ACCESS_TOKEN_TTL": "2h"}, "ACCESS_TOKEN_TTL"},
		{map[string]string{"REFRESH_TOKEN_TTL": "0s"}, "REFRESH_TOKEN_TTL"},
		{map[string]string{"REFRESH_TOKEN_TTL": "10m"}, "REFRESH_TOKEN_TTL"},
		{map[string]string{"REFRESH_GRACE": "-1s"}, "REFRESH_GRACE"},
		{map[string]string{"REFRESH_GRACE": "30m"}, "REFRESH_GRACE"},
		{map[string]string{"PASSWORD_HASH_CONCURRENCY": "0"}, "PASSWORD_HASH_CONCURRENCY"},
		{map[string]string{"PASSWORD_HASH_CONCURRENCY": "65"}, "PASSWORD_HASH_CONCURRENCY"},
		{map[string]string{"LOG_LEVEL": "loud"}, "LOG_LEVEL"},
		{map[string]string{"RATE_LOGIN_PER_IP_PER_MINUTE": "0"}, "RATE_LOGIN_PER_IP_PER_MINUTE"},
		{map[string]string{"RATE_LOGIN_FAILURES_PER_PHONE": "0"}, "RATE_LOGIN_FAILURES_PER_PHONE"},
		{map[string]string{"RATE_CODES_PER_TARGET_PER_HOUR": "0"}, "RATE_CODES_PER_TARGET_PER_HOUR"},
		{map[string]string{"RATE_CODES_PER_IP_PER_HOUR": "0"}, "RATE_CODES_PER_IP_PER_HOUR"},
		{map[string]string{"RATE_REFRESH_PER_IP_PER_MINUTE": "0"}, "RATE_REFRESH_PER_IP_PER_MINUTE"},
		{map[string]string{"RATE_LOGIN_FAILURES_WINDOW": "0s"}, "RATE_LOGIN_FAILURES_WINDOW"},
		{map[string]string{"RATE_LOGIN_FAILURES_WINDOW": "30s"}, "RATE_LOGIN_FAILURES_WINDOW"},
		{map[string]string{"RATE_CODE_COOLDOWN": "-1s"}, "RATE_CODE_COOLDOWN"},
	} {
		v := devVars()
		maps.Copy(v, c.vars)
		if _, err := LoadFrom(v); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: err = %v", c.vars, err)
		}
	}
}

func TestEdgeValuesAccepted(t *testing.T) {
	v := devVars()
	maps.Copy(v, map[string]string{
		"APP_NAME":                   "Качалка Фитнес Клуб",
		"ACCESS_TOKEN_TTL":           "1h",
		"REFRESH_TOKEN_TTL":          "1h",
		"REFRESH_GRACE":              "5m",
		"SMS_APP_HASH":               "FA+9qCX9VSu",
		"PASSWORD_HASH_CONCURRENCY":  "64",
		"RATE_LOGIN_FAILURES_WINDOW": "1m",
		"RATE_CODE_COOLDOWN":         "0s",
		"SMTP_HOST":                  "smtp.example.com",
		"SMTP_FROM":                  "Качалка <noreply@example.com>",
		"SMTP_PORT":                  "465",
	})
	if _, err := LoadFrom(v); err != nil {
		t.Fatal(err)
	}
}

func TestAppSecretFormat(t *testing.T) {
	for secret, ok := range map[string]bool{
		"+/+/+/+/+/+/+/+/+/+/+/+/+/+/+/+/+/+/+/+/+/8=": true,  // standard alphabet, 32 bytes
		"-_-_-_-_-_-_-_-_-_-_-_-_-_-_-_-_-_-_-_-_-_8=": false, // URL-safe alphabet
		"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=": true,  // 32 bytes
		"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8":  false, // padding missing
		"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHg==": false, // 31 bytes
	} {
		v := devVars()
		v["APP_SECRET"] = secret
		_, err := LoadFrom(v)
		if (err == nil) != ok {
			t.Errorf("%s: err = %v, want ok=%v", secret, err, ok)
		}
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Errorf("error reveals the secret: %v", err)
		}
	}
}

func TestDevSecretIsRecognisedWhenSetExplicitly(t *testing.T) {
	for _, secret := range []string{devAppSecret, devAppSecret + "\n", devAppSecret[:20] + "\r\n" + devAppSecret[20:]} {
		v := devVars()
		v["APP_SECRET"] = secret
		c, err := LoadFrom(v)
		if err != nil || !c.UsingDevSecret() {
			t.Fatalf("dev, %q: err = %v, UsingDevSecret = %v", secret, err, c.UsingDevSecret())
		}
		p := prodVars()
		p["APP_SECRET"] = secret
		if _, err := LoadFrom(p); err == nil || !strings.Contains(err.Error(), "public dev value") {
			t.Fatalf("prod, %q: err = %v", secret, err)
		}
	}
}

// validate runs first and already refuses this; derive must not fall back to the dev secret on its own.
func TestDeriveRefusesEmptySecretInProd(t *testing.T) {
	c := Config{Env: EnvProd}
	if err := c.derive(); err == nil {
		t.Fatal("derive fell back to the dev secret in prod")
	}
}

func TestProdRequiresHardening(t *testing.T) {
	v := devVars()
	v["APP_ENV"] = "prod"
	v["SMTP_TLS"] = "none"
	v["RATE_CODE_COOLDOWN"] = "0s"
	_, err := LoadFrom(v)
	if err == nil {
		t.Fatal("prod with defaults must fail")
	}
	for _, want := range []string{"APP_SECRET", "JWT_PRIVATE_KEY_FILE", "SMS_PROVIDER=log", "SMTP_HOST", "SMTP_TLS=none", "RATE_CODE_COOLDOWN=0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}

func TestProdErrorOnlyAboutSMSWhenOtherwiseComplete(t *testing.T) {
	_, err := LoadFrom(prodVars())
	if err == nil || err.Error() != "SMS_PROVIDER=log is forbidden in prod: codes would end up in logs; add a real provider (see README)" {
		t.Fatalf("want exactly the SMS_PROVIDER error, got %v", err)
	}
}

func TestConfigHidesSecrets(t *testing.T) {
	v := devVars()
	v["DATABASE_URL"] = "postgres://app:db-password@localhost/test"
	v["APP_SECRET"] = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	v["SMTP_USERNAME"] = "smtp-api-key"
	v["SMTP_PASSWORD"] = "smtp-password"
	c, err := LoadFrom(v)
	if err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("start", "config", c, "smtp", c.SMTP, "keys", c.Keys)
	secrets := []string{
		"db-password", "smtp-api-key", "smtp-password", v["APP_SECRET"],
		strings.Trim(fmt.Sprintf("%d", c.Keys.Refresh), "[]"), // as fmt prints []byte
		base64.StdEncoding.EncodeToString(c.Keys.Refresh),     // as JSON encodes []byte
	}
	for _, out := range []string{
		fmt.Sprintf("%v %+v %s %#v", c, c, c, c),
		fmt.Sprintf("%v %+v %#v %v %+v %#v", c.SMTP, c.SMTP, c.SMTP, c.Keys, c.Keys, c.Keys),
		logged.String(),
	} {
		for _, secret := range secrets {
			if strings.Contains(out, secret) {
				t.Fatalf("%q leaks into %s", secret, out)
			}
		}
	}
}
