package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/apperr"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/httpapi/oapi"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/password"
)

// writeProblem renders e as RFC 9457 problem details.
func writeProblem(w http.ResponseWriter, e *apperr.Error) {
	h := w.Header()
	if e.RetryAfter > 0 {
		h.Set("Retry-After", strconv.Itoa(int(math.Ceil(e.RetryAfter.Seconds()))))
	}
	if e.Status == http.StatusUnauthorized {
		h.Set("WWW-Authenticate", "Bearer")
	}
	h.Set("Content-Type", "application/problem+json")
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(toProblem(e))
}

func toProblem(e *apperr.Error) oapi.Problem {
	p := oapi.Problem{
		Type:         "about:blank",
		Title:        e.Title,
		Status:       e.Status,
		Code:         oapi.ProblemCode(e.Code),
		AttemptsLeft: e.AttemptsLeft,
	}
	if len(e.Fields) > 0 {
		fields := make([]oapi.FieldError, len(e.Fields))
		for i, f := range e.Fields {
			fields[i] = oapi.FieldError{Field: f.Field, Code: oapi.FieldErrorCode(f.Code)}
		}
		p.Errors = &fields
	}
	return p
}

// errorHandlers plug into the generated strict server.
type errorHandlers struct{ log *slog.Logger }

// request handles bodies the generated code could not decode (bad JSON, too large).
func (h errorHandlers) request(w http.ResponseWriter, r *http.Request, err error) {
	h.log.DebugContext(r.Context(), "bad request body", "err", err)
	writeProblem(w, apperr.ErrBadRequest)
}

// response maps errors returned by handlers and middlewares. Anything that is not an
// *apperr.Error is a bug or an outage: it is logged and hidden behind internal_error.
func (h errorHandlers) response(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apperr.Error
	switch {
	case errors.As(err, &ae):
	case errors.Is(err, password.ErrBusy):
		ae = apperr.ErrUnavailable
	case errors.Is(err, context.DeadlineExceeded):
		// requestTimeout fired, e.g. while waiting for a hashing slot.
		h.log.WarnContext(r.Context(), "request timed out", "err", err, "request_id", RequestID(r.Context()))
		ae = apperr.ErrUnavailable
	case errors.Is(err, context.Canceled):
		// The client went away; nobody reads the response, and it is not a server fault.
		h.log.DebugContext(r.Context(), "request canceled", "err", err, "request_id", RequestID(r.Context()))
		ae = apperr.ErrUnavailable
	default:
		h.log.ErrorContext(r.Context(), "request failed", "err", err, "request_id", RequestID(r.Context()))
		ae = apperr.ErrInternal
	}
	writeProblem(w, ae)
}
