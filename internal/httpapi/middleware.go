package httpapi

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/apperr"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/httpapi/oapi"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/ratelimit"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/token"
)

// ---- net/http middlewares (whole server) ----

type requestIDKey struct{}

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// withRequestID keeps a sane incoming X-Request-ID or makes one, and echoes it back.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validRequestID.MatchString(id) {
			id = rand.Text()[:16]
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// RequestID returns the id assigned by withRequestID.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// withAccessLog logs one line per request. Bodies are never logged: they hold passwords and codes.
func withAccessLog(log *slog.Logger, ipOf func(*http.Request) netip.Addr, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		level := slog.LevelInfo
		if strings.HasPrefix(r.URL.Path, "/healthz") || strings.HasPrefix(r.URL.Path, "/readyz") {
			level = slog.LevelDebug
		}
		log.Log(r.Context(), level, "http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", ipOf(r).String(),
			"request_id", RequestID(r.Context()),
		)
	})
}

// withRecover turns a panic into a logged 500 instead of a dropped connection.
func withRecover(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(v) // net/http's deliberate abort; let the server handle it
				}
				log.ErrorContext(r.Context(), "panic", "value", v, "stack", string(debug.Stack()), "request_id", RequestID(r.Context()))
				writeProblem(w, apperr.ErrInternal)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// requestTimeout bounds one request. http.Server.WriteTimeout does not cancel r.Context(), so
// without it a request waiting for a password-hashing slot would wait forever and the designed
// 503 would never fire. Below the server's WriteTimeout (15 s), above the SMTP send limit (10 s).
// A variable only so tests can shorten it.
var requestTimeout = 12 * time.Second

// withLimits caps the request body and the request time, and forbids caching of API responses
// (they carry tokens).
func withLimits(maxBody int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
		defer cancel()
		r = r.WithContext(ctx)
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// ---- strict middlewares (per operation; operationID as in the generated code) ----

// protectedOps lists operations with `security: bearerAuth` in api/openapi.yaml.
var protectedOps = map[string]bool{
	"Logout":          true,
	"GetMe":           true,
	"DeleteMe":        true,
	"ChangeEmail":     true,
	"VerifyEmail":     true,
	"ResendEmailCode": true,
	"ChangePassword":  true,
}

type principalKey struct{}

func withPrincipal(ctx context.Context, a token.Access) context.Context {
	return context.WithValue(ctx, principalKey{}, a)
}

// principal returns the caller of a protected operation.
func principal(ctx context.Context) (token.Access, error) {
	a, ok := ctx.Value(principalKey{}).(token.Access)
	if !ok {
		return token.Access{}, apperr.ErrUnauthorized
	}
	return a, nil
}

// requireAuth validates the bearer access token for protected operations.
func requireAuth(signer *token.Signer) oapi.StrictMiddlewareFunc {
	return func(f oapi.StrictHandlerFunc, op string) oapi.StrictHandlerFunc {
		if !protectedOps[op] {
			return f
		}
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
			// The auth scheme is case-insensitive (RFC 7235).
			const prefix = "Bearer "
			h := r.Header.Get("Authorization")
			if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
				return nil, apperr.ErrUnauthorized
			}
			raw := h[len(prefix):]
			a, err := signer.ParseAccess(raw)
			if err != nil {
				return nil, apperr.ErrUnauthorized
			}
			return f(withPrincipal(ctx, a), w, r, req)
		}
	}
}

// limitKey groups IPv6 clients by /64: one host usually controls a whole /64, so per-address
// buckets would let it multiply its limit (and the limiter's memory).
func limitKey(a netip.Addr) string {
	if a.Is6() && !a.Is4In6() {
		if p, err := a.Prefix(64); err == nil {
			return p.String()
		}
	}
	return a.String()
}

// limitByIP applies a per-client-IP limiter to the listed operations.
func limitByIP(limits map[string]*ratelimit.Limiter, ipOf func(*http.Request) netip.Addr) oapi.StrictMiddlewareFunc {
	return func(f oapi.StrictHandlerFunc, op string) oapi.StrictHandlerFunc {
		lim, ok := limits[op]
		if !ok {
			return f
		}
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
			if ok, wait := lim.Allow(limitKey(ipOf(r))); !ok {
				return nil, apperr.RateLimited(wait)
			}
			return f(ctx, w, r, req)
		}
	}
}
