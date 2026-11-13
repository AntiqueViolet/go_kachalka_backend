// Package config loads and validates settings from environment variables.
package config

import (
	"bytes"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"net/netip"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/caarlos0/env/v11"
)

const (
	EnvDev  = "dev"
	EnvProd = "prod"

	minSecretLen = 32
	// devAppSecret is base64("dev-insecure-app-secret-change-me!"); used only in dev when APP_SECRET is empty.
	devAppSecret = "ZGV2LWluc2VjdXJlLWFwcC1zZWNyZXQtY2hhbmdlLW1lIQ==" //nolint:gosec // G101: public dev default, refused in prod

	// Bounds that keep a typo from quietly weakening protection.
	maxAppNameLen    = 20              // the SMS with the app hash still fits one Cyrillic segment (70)
	maxAccessTTL     = time.Hour       // access tokens can't be revoked; logout takes effect within this time
	maxRefreshGrace  = 5 * time.Minute // a stolen previous refresh token is honoured this long
	maxHashWorkers   = 64              // each password hash holds ~19 MiB
	minFailureWindow = time.Minute     // shorter windows let password guessing run at full speed
)

// smsAppHash is the 11-character hash of the Android SMS Retriever API.
var smsAppHash = regexp.MustCompile(`^[A-Za-z0-9+/]{11}$`)

type Config struct {
	Env                     string        `env:"APP_ENV" envDefault:"dev"`
	AppName                 string        `env:"APP_NAME" envDefault:"Качалка"`
	HTTPAddr                string        `env:"HTTP_ADDR" envDefault:":8080"`
	DatabaseURL             string        `env:"DATABASE_URL,required,notEmpty"`
	AppSecret               string        `env:"APP_SECRET"`
	JWTPrivateKeyFile       string        `env:"JWT_PRIVATE_KEY_FILE"`
	JWTIssuer               string        `env:"JWT_ISSUER" envDefault:"go-kachalka"`
	AccessTokenTTL          time.Duration `env:"ACCESS_TOKEN_TTL" envDefault:"15m"`
	RefreshTokenTTL         time.Duration `env:"REFRESH_TOKEN_TTL" envDefault:"1440h"`
	RefreshGrace            time.Duration `env:"REFRESH_GRACE" envDefault:"30s"`
	SMSProvider             string        `env:"SMS_PROVIDER" envDefault:"log"`
	SMSAppHash              string        `env:"SMS_APP_HASH"`
	TrustedProxies          []string      `env:"TRUSTED_PROXIES" envSeparator:","`
	PasswordHashConcurrency int           `env:"PASSWORD_HASH_CONCURRENCY" envDefault:"4"`
	LogLevel                string        `env:"LOG_LEVEL" envDefault:"info"`
	SMTP                    SMTP
	Rate                    Rate

	// Derived in Load; not read from the environment.
	Keys          Keys           `env:"-"`
	ProxyPrefixes []netip.Prefix `env:"-"`
	Level         slog.Level     `env:"-"`
}

type SMTP struct {
	Host     string `env:"SMTP_HOST"`
	Port     int    `env:"SMTP_PORT" envDefault:"587"`
	Username string `env:"SMTP_USERNAME"`
	Password string `env:"SMTP_PASSWORD"`
	From     string `env:"SMTP_FROM"`
	TLS      string `env:"SMTP_TLS" envDefault:"starttls"`
}

type Rate struct {
	LoginPerIPPerMinute   int           `env:"RATE_LOGIN_PER_IP_PER_MINUTE" envDefault:"20"`
	LoginFailuresPerPhone int           `env:"RATE_LOGIN_FAILURES_PER_PHONE" envDefault:"5"`
	LoginFailuresWindow   time.Duration `env:"RATE_LOGIN_FAILURES_WINDOW" envDefault:"15m"`
	CodeCooldown          time.Duration `env:"RATE_CODE_COOLDOWN" envDefault:"60s"`
	CodesPerTargetPerHour int           `env:"RATE_CODES_PER_TARGET_PER_HOUR" envDefault:"5"`
	CodesPerIPPerHour     int           `env:"RATE_CODES_PER_IP_PER_HOUR" envDefault:"20"`
	RefreshPerIPPerMinute int           `env:"RATE_REFRESH_PER_IP_PER_MINUTE" envDefault:"60"`
}

// Keys are HKDF-derived from APP_SECRET so one secret serves several independent purposes.
type Keys struct {
	Refresh []byte
	OTP     []byte
}

func (c Config) IsProd() bool { return c.Env == EnvProd }

// UsingDevSecret reports that the insecure, publicly known dev secret is in use.
func (c Config) UsingDevSecret() bool { return c.AppSecret == "" || isDevSecret(c.AppSecret) }

// isDevSecret also catches other spellings of the dev value (line breaks, padding bits):
// they decode to the same bytes and so to the same keys.
func isDevSecret(s string) bool {
	got, err := base64.StdEncoding.DecodeString(s)
	dev, _ := base64.StdEncoding.DecodeString(devAppSecret)
	return err == nil && bytes.Equal(got, dev)
}

// String, GoString and LogValue keep secrets (APP_SECRET, the derived keys, passwords in
// DATABASE_URL and SMTP_PASSWORD, SMTP_USERNAME which some providers use as an API key) out of logs and error messages, whatever prints a Config.
// SMTP and Keys do the same when printed on their own.
func (c Config) String() string {
	return fmt.Sprintf("config{env=%s addr=%s %v sms=%s}", c.Env, c.HTTPAddr, c.SMTP, c.SMSProvider)
}

func (c Config) GoString() string { return c.String() }

func (c Config) LogValue() slog.Value { return slog.StringValue(c.String()) }

func (s SMTP) String() string {
	return fmt.Sprintf("smtp{host=%s port=%d from=%s tls=%s}", s.Host, s.Port, s.From, s.TLS)
}

func (s SMTP) GoString() string { return s.String() }

func (s SMTP) LogValue() slog.Value { return slog.StringValue(s.String()) }

func (Keys) String() string { return "keys{redacted}" }

func (k Keys) GoString() string { return k.String() }

func (k Keys) LogValue() slog.Value { return slog.StringValue(k.String()) }

// Load reads the process environment.
func Load() (Config, error) { return load(env.Options{}) }

// LoadFrom reads only the given variables, never the process environment (for tests).
func LoadFrom(vars map[string]string) (Config, error) {
	if vars == nil {
		vars = map[string]string{} // env treats a nil map as "use the process environment"
	}
	return load(env.Options{Environment: vars})
}

func load(opts env.Options) (Config, error) {
	c, err := env.ParseAsWithOptions[Config](opts)
	if err != nil {
		return Config{}, err
	}
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	if err := c.derive(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c *Config) validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	switch c.Env {
	case EnvDev, EnvProd:
	default:
		add("APP_ENV must be %q or %q, got %q", EnvDev, EnvProd, c.Env)
	}
	if n := utf8.RuneCountInString(c.AppName); n > maxAppNameLen || strings.IndexFunc(c.AppName, notPrintable) >= 0 {
		add("APP_NAME must be at most %d characters on one line, got %q", maxAppNameLen, c.AppName)
	}
	if c.SMSProvider != "log" {
		add("SMS_PROVIDER %q is not supported (available: log)", c.SMSProvider)
	}
	if c.SMSAppHash != "" && !smsAppHash.MatchString(c.SMSAppHash) {
		add("SMS_APP_HASH must be the 11-character SMS Retriever hash, got %q", c.SMSAppHash)
	}
	switch c.SMTP.TLS {
	case "starttls", "tls", "none":
	default:
		add("SMTP_TLS must be starttls, tls or none, got %q", c.SMTP.TLS)
	}
	if c.SMTP.Host != "" {
		if c.SMTP.Port < 1 || c.SMTP.Port > 65535 {
			add("SMTP_PORT must be 1-65535, got %d", c.SMTP.Port)
		}
		if _, err := mail.ParseAddress(c.SMTP.From); err != nil {
			add("SMTP_FROM must be an email address, got %q", c.SMTP.From)
		}
	}
	if c.AccessTokenTTL <= 0 || c.AccessTokenTTL > maxAccessTTL {
		add("ACCESS_TOKEN_TTL must be in (0, %v], got %v", maxAccessTTL, c.AccessTokenTTL)
	}
	if c.RefreshTokenTTL <= 0 || c.RefreshTokenTTL < c.AccessTokenTTL {
		add("REFRESH_TOKEN_TTL must be positive and not shorter than ACCESS_TOKEN_TTL, got %v", c.RefreshTokenTTL)
	}
	if c.RefreshGrace < 0 || c.RefreshGrace > maxRefreshGrace {
		add("REFRESH_GRACE must be in [0, %v], got %v", maxRefreshGrace, c.RefreshGrace)
	}
	if c.PasswordHashConcurrency < 1 || c.PasswordHashConcurrency > maxHashWorkers {
		add("PASSWORD_HASH_CONCURRENCY must be 1-%d, got %d", maxHashWorkers, c.PasswordHashConcurrency)
	}
	r := c.Rate
	for _, lim := range []struct {
		name string
		v    int
	}{
		{"RATE_LOGIN_PER_IP_PER_MINUTE", r.LoginPerIPPerMinute},
		{"RATE_LOGIN_FAILURES_PER_PHONE", r.LoginFailuresPerPhone},
		{"RATE_CODES_PER_TARGET_PER_HOUR", r.CodesPerTargetPerHour},
		{"RATE_CODES_PER_IP_PER_HOUR", r.CodesPerIPPerHour},
		{"RATE_REFRESH_PER_IP_PER_MINUTE", r.RefreshPerIPPerMinute},
	} {
		if lim.v < 1 {
			add("%s must be at least 1, got %d", lim.name, lim.v)
		}
	}
	if r.LoginFailuresWindow < minFailureWindow {
		add("RATE_LOGIN_FAILURES_WINDOW must be at least %v, got %v", minFailureWindow, r.LoginFailuresWindow)
	}
	if r.CodeCooldown < 0 {
		add("RATE_CODE_COOLDOWN must not be negative, got %v", r.CodeCooldown)
	}
	if err := c.Level.UnmarshalText([]byte(c.LogLevel)); err != nil {
		add("LOG_LEVEL: %v", err)
	}
	for _, p := range c.TrustedProxies {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		prefix, err := parsePrefix(p)
		switch {
		case err != nil:
			add("TRUSTED_PROXIES: %q is not an IP or CIDR", p)
		case prefix.Bits() == 0:
			add("TRUSTED_PROXIES: %q would let every client set its own IP", p)
		default:
			c.ProxyPrefixes = append(c.ProxyPrefixes, prefix)
		}
	}
	if c.IsProd() {
		switch {
		case c.AppSecret == "":
			add("APP_SECRET is required in prod")
		case isDevSecret(c.AppSecret):
			add("APP_SECRET is the public dev value; generate one with keygen")
		}
		if c.JWTPrivateKeyFile == "" {
			add("JWT_PRIVATE_KEY_FILE is required in prod")
		}
		if c.SMSProvider == "log" {
			add("SMS_PROVIDER=log is forbidden in prod: codes would end up in logs; add a real provider (see README)")
		}
		if c.SMTP.Host == "" || c.SMTP.From == "" {
			add("SMTP_HOST and SMTP_FROM are required in prod")
		}
		if c.SMTP.TLS == "none" {
			add("SMTP_TLS=none is forbidden in prod")
		}
		if r.CodeCooldown == 0 {
			add("RATE_CODE_COOLDOWN=0 is forbidden in prod")
		}
	}
	return errors.Join(errs...)
}

func (c *Config) derive() error {
	raw := c.AppSecret
	if raw == "" {
		if c.IsProd() { // validate already refuses this; checked again so a reorder can't leak the dev secret
			return errors.New("APP_SECRET is required in prod")
		}
		raw = devAppSecret
	}
	secret, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return fmt.Errorf("APP_SECRET must be standard base64 as printed by keygen: %w", err)
	}
	if len(secret) < minSecretLen {
		return fmt.Errorf("APP_SECRET must decode to at least %d bytes, got %d", minSecretLen, len(secret))
	}
	if c.Keys.Refresh, err = hkdf.Key(sha256.New, secret, nil, "go-kachalka refresh v1", 32); err != nil {
		return err
	}
	if c.Keys.OTP, err = hkdf.Key(sha256.New, secret, nil, "go-kachalka otp v1", 32); err != nil {
		return err
	}
	return nil
}

func parsePrefix(s string) (netip.Prefix, error) {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	a = a.Unmap() // client addresses are compared unmapped
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// notPrintable flags line breaks (including U+2028), control and invisible format characters.
func notPrintable(r rune) bool { return !unicode.IsPrint(r) }
