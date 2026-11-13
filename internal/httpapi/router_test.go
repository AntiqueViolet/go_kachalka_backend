package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/clock"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/ratelimit"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/token"
)

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

// newTestRouter builds the router without services: every request in these tests is
// answered by middleware before a handler runs (or deliberately panics in one).
func newTestRouter(t *testing.T, db Pinger) (http.Handler, *token.Signer, Limits) {
	t.Helper()
	clk := clock.NewFake(time.Now())
	key, err := token.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer := token.NewSigner(token.SignerConfig{Key: key, Issuer: "test", AccessTTL: time.Minute, RegistrationTTL: time.Minute, Clock: clk})
	limits := Limits{
		Login:   ratelimit.Per(1, time.Minute, clk),
		Refresh: ratelimit.Per(1, time.Minute, clk),
		Codes:   ratelimit.Per(1, time.Hour, clk),
	}
	h := New(Config{Signer: signer, DB: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Limits: limits})
	return h, signer, limits
}

type problemBody struct {
	Status int    `json:"status"`
	Code   string `json:"code"`
}

func serve(h http.Handler, method, path, body string, header http.Header) (*httptest.ResponseRecorder, problemBody) {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range header {
		r.Header[k] = v
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var p problemBody
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	return w, p
}

func TestProtectedRouteNeedsToken(t *testing.T) {
	h, _, _ := newTestRouter(t, fakeDB{})
	for _, hdr := range []http.Header{nil, {"Authorization": {"Bearer garbage"}}, {"Authorization": {"Basic abc"}}} {
		w, p := serve(h, "GET", "/v1/me", "", hdr)
		if w.Code != http.StatusUnauthorized || p.Code != "unauthorized" {
			t.Fatalf("header %v: got %d %+v", hdr, w.Code, p)
		}
		if w.Header().Get("Content-Type") != "application/problem+json" || w.Header().Get("WWW-Authenticate") != "Bearer" {
			t.Fatalf("headers %v", w.Header())
		}
	}
}

func TestBadBodies(t *testing.T) {
	h, _, _ := newTestRouter(t, fakeDB{})
	w, p := serve(h, "POST", "/v1/auth/login", "{not json", nil)
	if w.Code != http.StatusBadRequest || p.Code != "bad_request" {
		t.Fatalf("bad json: %d %+v", w.Code, p)
	}
	big := `{"phone":"` + strings.Repeat("1", maxBodyBytes) + `"}`
	w, p = serve(h, "POST", "/v1/auth/login", big, nil)
	if w.Code != http.StatusBadRequest || p.Code != "bad_request" {
		t.Fatalf("big body: %d %+v", w.Code, p)
	}
}

func TestRateLimitedByIP(t *testing.T) {
	h, _, limits := newTestRouter(t, fakeDB{})
	limits.Login.Allow("192.0.2.1") // httptest requests come from 192.0.2.1
	w, p := serve(h, "POST", "/v1/auth/login", `{"phone":"+79991234567","password":"x"}`, nil)
	if w.Code != http.StatusTooManyRequests || p.Code != "rate_limited" || w.Header().Get("Retry-After") != "60" {
		t.Fatalf("got %d %+v Retry-After=%q", w.Code, p, w.Header().Get("Retry-After"))
	}
}

func TestPanicBecomesInternalError(t *testing.T) {
	h, signer, _ := newTestRouter(t, fakeDB{})
	access, err := signer.IssueAccess(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	// No account service is wired, so the handler panics on a nil pointer.
	w, p := serve(h, "GET", "/v1/me", "", http.Header{"Authorization": {"Bearer " + access}})
	if w.Code != http.StatusInternalServerError || p.Code != "internal_error" {
		t.Fatalf("got %d %+v", w.Code, p)
	}
}

func TestHealthAndReadiness(t *testing.T) {
	h, _, _ := newTestRouter(t, fakeDB{})
	if w, _ := serve(h, "GET", "/healthz", "", nil); w.Code != http.StatusOK {
		t.Fatalf("healthz %d", w.Code)
	}
	w, _ := serve(h, "GET", "/readyz", "", http.Header{"X-Request-Id": {"abc-123"}})
	if w.Code != http.StatusOK || w.Header().Get("X-Request-ID") != "abc-123" {
		t.Fatalf("readyz %d, request id %q", w.Code, w.Header().Get("X-Request-ID"))
	}
	down, _, _ := newTestRouter(t, fakeDB{err: errors.New("db down")})
	if w, p := serve(down, "GET", "/readyz", "", nil); w.Code != http.StatusServiceUnavailable || p.Code != "service_unavailable" {
		t.Fatalf("readyz with db down: %d %+v", w.Code, p)
	}
}

func TestResponsesAreNotCached(t *testing.T) {
	h, _, _ := newTestRouter(t, fakeDB{})
	w, _ := serve(h, "GET", "/v1/me", "", nil)
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", w.Header().Get("Cache-Control"))
	}
}

func TestLowercaseBearerAccepted(t *testing.T) {
	h, signer, _ := newTestRouter(t, fakeDB{})
	access, err := signer.IssueAccess(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	for _, scheme := range []string{"bearer", "BEARER"} {
		// Past authentication the nil account service panics: 500, not 401.
		w, p := serve(h, "GET", "/v1/me", "", http.Header{"Authorization": {scheme + " " + access}})
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("%s: got %d %+v", scheme, w.Code, p)
		}
	}
}

func TestAnonymousCallDoesNotSpendCodesQuota(t *testing.T) {
	h, _, _ := newTestRouter(t, fakeDB{}) // Codes: one call per hour per IP
	if w, p := serve(h, "POST", "/v1/me/email/resend", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("resend: %d %+v", w.Code, p)
	}
	// The services are not wired in this router, so the call itself fails; it just must not be 429.
	if w, p := serve(h, "POST", "/v1/auth/register/start", `{}`, nil); w.Code == http.StatusTooManyRequests {
		t.Fatalf("register/start limited by an anonymous call: %d %+v", w.Code, p)
	}
}

func TestRequestTimeoutBecomesUnavailable(t *testing.T) {
	old := requestTimeout
	requestTimeout = 20 * time.Millisecond
	defer func() { requestTimeout = old }()

	errs := errorHandlers{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	blocked := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // like a request waiting for a hashing slot
		errs.response(w, r, r.Context().Err())
	})
	w, p := serve(withRequestID(withLimits(maxBodyBytes, blocked)), "POST", "/x", "", nil)
	if w.Code != http.StatusServiceUnavailable || p.Code != "service_unavailable" {
		t.Fatalf("got %d %+v", w.Code, p)
	}
}
