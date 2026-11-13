package token

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/clock"
)

func newTestSigner(t *testing.T, c clock.Clock) *Signer {
	t.Helper()
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return NewSigner(SignerConfig{Key: k, Issuer: "test", AccessTTL: 15 * time.Minute, RegistrationTTL: 15 * time.Minute, Clock: c})
}

// signWith signs arbitrary claims with the signer's real key, to build forged-but-signed tokens.
func signWith(t *testing.T, s *Signer, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tok.Header["kid"] = s.kid
	raw, err := tok.SignedString(s.cfg.Key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// flipSigChar changes the first character of the signature, which always changes its bytes
// (the last characters may only carry padding bits).
func flipSigChar(raw string) string {
	i := strings.LastIndex(raw, ".") + 1
	b := []byte(raw)
	if b[i] == 'A' {
		b[i] = 'B'
	} else {
		b[i] = 'A'
	}
	return string(b)
}

func TestAccessRoundTrip(t *testing.T) {
	s := newTestSigner(t, clock.NewFake(time.Now()))
	uid, sid := uuid.New(), uuid.New()
	raw, err := s.IssueAccess(uid, sid)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ParseAccess(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != uid || got.SessionID != sid {
		t.Fatalf("got %+v", got)
	}
}

// The lifetime is TTL plus a 5 s leeway; the literals below pin both.
func TestAccessLifetime(t *testing.T) {
	c := clock.NewFake(time.Now())
	s := newTestSigner(t, c)
	raw, err := s.IssueAccess(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	c.Advance(15*time.Minute - time.Second)
	if _, err := s.ParseAccess(raw); err != nil {
		t.Fatalf("rejected 1 s before expiry: %v", err)
	}
	c.Advance(5 * time.Second) // TTL + 4 s: inside the leeway
	if _, err := s.ParseAccess(raw); err != nil {
		t.Fatalf("rejected inside the leeway: %v", err)
	}
	c.Advance(2 * time.Second) // TTL + 6 s
	_, err = s.ParseAccess(raw)
	if !errors.Is(err, ErrInvalid) || !errors.Is(err, jwt.ErrTokenExpired) {
		t.Fatalf("err = %v, want ErrInvalid wrapping jwt.ErrTokenExpired", err)
	}
}

func TestAccessRejectsOtherKey(t *testing.T) {
	c := clock.NewFake(time.Now())
	raw, err := newTestSigner(t, c).IssueAccess(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newTestSigner(t, c).ParseAccess(raw); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// Every case is signed with the real key (or forges the algorithm), so only the claim and
// header checks stand between it and acceptance.
func TestParseAccessRejectsForgedTokens(t *testing.T) {
	c := clock.NewFake(time.Now())
	s := newTestSigner(t, c)
	now := c.Now()
	uid, sid := uuid.NewString(), uuid.NewString()
	base := func() jwt.MapClaims {
		return jwt.MapClaims{"iss": "test", "aud": []string{"api"}, "sub": uid, "sid": sid, "exp": now.Add(time.Minute).Unix()}
	}
	with := func(f func(m jwt.MapClaims)) string {
		m := base()
		f(m)
		return signWith(t, s, m)
	}

	if _, err := s.ParseAccess(with(func(jwt.MapClaims) {})); err != nil {
		t.Fatalf("baseline token rejected: %v", err)
	}
	if _, err := s.ParseAccess(with(func(m jwt.MapClaims) { m["exp"] = now.Add(-3 * time.Second).Unix() })); err != nil {
		t.Fatalf("token expired 3 s ago (inside leeway) rejected: %v", err)
	}

	payload, err := json.Marshal(base())
	if err != nil {
		t.Fatal(err)
	}
	none := b64([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + b64(payload) + "."
	hs := b64([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + b64(payload)
	mac := hmac.New(sha256.New, s.pub) // the classic public-key-as-HMAC-secret attack
	mac.Write([]byte(hs))
	good := with(func(jwt.MapClaims) {})

	cases := map[string]string{
		"registration audience":  with(func(m jwt.MapClaims) { m["aud"] = []string{"registration"} }),
		"no audience":            with(func(m jwt.MapClaims) { delete(m, "aud") }),
		"both audiences":         with(func(m jwt.MapClaims) { m["aud"] = []string{"api", "registration"} }),
		"wrong issuer":           with(func(m jwt.MapClaims) { m["iss"] = "other" }),
		"no issuer":              with(func(m jwt.MapClaims) { delete(m, "iss") }),
		"no expiry":              with(func(m jwt.MapClaims) { delete(m, "exp") }),
		"expired":                with(func(m jwt.MapClaims) { m["exp"] = now.Add(-time.Minute).Unix() }),
		"not yet valid":          with(func(m jwt.MapClaims) { m["nbf"] = now.Add(time.Hour).Unix() }),
		"no session":             with(func(m jwt.MapClaims) { delete(m, "sid") }),
		"garbage session":        with(func(m jwt.MapClaims) { m["sid"] = "zzz" }),
		"numeric session":        with(func(m jwt.MapClaims) { m["sid"] = 5 }),
		"no subject":             with(func(m jwt.MapClaims) { delete(m, "sub") }),
		"phone subject":          with(func(m jwt.MapClaims) { m["sub"] = "+79991234567" }),
		"nil uuid subject":       with(func(m jwt.MapClaims) { m["sub"] = uuid.Nil.String() }),
		"urn subject":            with(func(m jwt.MapClaims) { m["sub"] = "urn:uuid:" + uid }),
		"braced subject":         with(func(m jwt.MapClaims) { m["sub"] = "{" + uid + "}" }),
		"oversized":              with(func(m jwt.MapClaims) { m["pad"] = strings.Repeat("a", 5000) }),
		"alg none, valid claims": none,
		"HS256 with public key":  hs + "." + b64(mac.Sum(nil)),
		"tampered signature":     flipSigChar(good),
		"empty":                  "",
		"not a jwt":              "not.a.jwt",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := s.ParseAccess(raw); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestHeaderHasAlgAndKid(t *testing.T) {
	s := newTestSigner(t, clock.NewFake(time.Now()))
	raw, err := s.IssueAccess(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := jwt.NewParser().ParseUnverified(raw, jwt.MapClaims{})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(s.pub)
	if tok.Header["alg"] != "EdDSA" || tok.Header["kid"] != hex.EncodeToString(sum[:8]) {
		t.Fatalf("header %v", tok.Header)
	}
}

func TestAudiencesAreNotInterchangeable(t *testing.T) {
	s := newTestSigner(t, clock.NewFake(time.Now()))
	reg, err := s.IssueRegistration("+79991234567")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ParseAccess(reg); !errors.Is(err, ErrInvalid) {
		t.Fatalf("registration token accepted as access: %v", err)
	}
	acc, err := s.IssueAccess(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ParseRegistration(acc); !errors.Is(err, ErrInvalid) {
		t.Fatalf("access token accepted as registration: %v", err)
	}
}

func TestRegistrationRoundTrip(t *testing.T) {
	s := newTestSigner(t, clock.NewFake(time.Now()))
	raw, err := s.IssueRegistration("+79991234567")
	if err != nil {
		t.Fatal(err)
	}
	phone, err := s.ParseRegistration(raw)
	if err != nil || phone != "+79991234567" {
		t.Fatalf("got %q, %v", phone, err)
	}
	if _, err := s.IssueRegistration(""); err == nil {
		t.Fatal("registration token issued for an empty phone")
	}
}

func TestParseRegistrationRejectsForgedTokens(t *testing.T) {
	c := clock.NewFake(time.Now())
	s := newTestSigner(t, c)
	base := func() jwt.MapClaims {
		return jwt.MapClaims{"iss": "test", "aud": []string{"registration"}, "sub": "+79991234567", "exp": c.Now().Add(time.Minute).Unix()}
	}
	for name, f := range map[string]func(m jwt.MapClaims){
		"api audience":  func(m jwt.MapClaims) { m["aud"] = []string{"api"} },
		"empty subject": func(m jwt.MapClaims) { m["sub"] = "" },
		"no subject":    func(m jwt.MapClaims) { delete(m, "sub") },
		"no expiry":     func(m jwt.MapClaims) { delete(m, "exp") },
	} {
		t.Run(name, func(t *testing.T) {
			m := base()
			f(m)
			if _, err := s.ParseRegistration(signWith(t, s, m)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestNewSignerRejectsBadConfig(t *testing.T) {
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ok := SignerConfig{Key: k, Issuer: "test", AccessTTL: time.Minute, RegistrationTTL: time.Minute}
	bad := map[string]func(c *SignerConfig){
		"no key":       func(c *SignerConfig) { c.Key = nil },
		"short key":    func(c *SignerConfig) { c.Key = k[:10] },
		"seed as key":  func(c *SignerConfig) { c.Key = k[:32] },
		"empty issuer": func(c *SignerConfig) { c.Issuer = "" }, // would disable the issuer check
		"zero ttl":     func(c *SignerConfig) { c.AccessTTL = 0 },
		"zero reg ttl": func(c *SignerConfig) { c.RegistrationTTL = 0 },
	}
	for name, f := range bad {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("NewSigner accepted the config")
				}
			}()
			c := ok
			f(&c)
			NewSigner(c)
		})
	}
	s := NewSigner(ok) // nil Clock falls back to the wall clock
	raw, err := s.IssueAccess(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ParseAccess(raw); err != nil {
		t.Fatalf("signer with default clock: %v", err)
	}
}
