// Package httpapi exposes the services over HTTP according to api/openapi.yaml.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/account"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/apperr"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/auth"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/httpapi/oapi"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/ratelimit"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/token"
)

const maxBodyBytes = 64 << 10

type Pinger interface {
	Ping(ctx context.Context) error
}

// Limits are per-client-IP limiters (spec section 8).
type Limits struct {
	Login   *ratelimit.Limiter
	Refresh *ratelimit.Limiter
	Codes   *ratelimit.Limiter // shared by every operation that sends a code
}

type Config struct {
	Auth           *auth.Service
	Account        *account.Service
	Signer         *token.Signer
	DB             Pinger
	Log            *slog.Logger
	TrustedProxies []netip.Prefix
	Limits         Limits
}

func New(c Config) http.Handler {
	ipOf := func(r *http.Request) netip.Addr { return ClientIP(r, c.TrustedProxies) }
	errs := errorHandlers{log: c.Log}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := c.DB.Ping(ctx); err != nil {
			c.Log.WarnContext(r.Context(), "readiness check failed", "err", err, "request_id", RequestID(r.Context()))
			writeProblem(w, apperr.ErrUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	strict := oapi.NewStrictHandlerWithOptions(
		handlers{auth: c.Auth, account: c.Account},
		// The last middleware is the outermost: authentication runs before the per-IP limits,
		// so anonymous (401) calls to protected operations never spend the shared quota.
		[]oapi.StrictMiddlewareFunc{
			limitByIP(ipLimits(c.Limits), ipOf),
			requireAuth(c.Signer),
		},
		oapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  errs.request,
			ResponseErrorHandlerFunc: errs.response,
		},
	)
	oapi.HandlerWithOptions(strict, oapi.StdHTTPServerOptions{BaseRouter: mux})

	var h http.Handler = mux
	h = withLimits(maxBodyBytes, h)
	h = withRecover(c.Log, h)
	h = withAccessLog(c.Log, ipOf, h)
	h = withRequestID(h)
	return h
}

// ipLimits maps operations (names as in the generated strict handler) to their per-IP limiter.
func ipLimits(l Limits) map[string]*ratelimit.Limiter {
	return map[string]*ratelimit.Limiter{
		"Login":           l.Login,
		"Refresh":         l.Refresh,
		"RegisterStart":   l.Codes,
		"ForgotPassword":  l.Codes,
		"ChangeEmail":     l.Codes,
		"ResendEmailCode": l.Codes,
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
