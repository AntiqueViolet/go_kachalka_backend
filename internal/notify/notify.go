// Package notify sends verification codes by SMS and email.
package notify

import (
	"context"
	"log/slog"
)

type SMSSender interface {
	SendSMS(ctx context.Context, phone, text string) error
}

type EmailSender interface {
	SendEmail(ctx context.Context, to, subject, body string) error
}

var (
	_ SMSSender   = (*LogSMS)(nil)
	_ EmailSender = (*LogEmail)(nil)
)

// LogSMS writes SMS to the log instead of sending. Dev only: config refuses it in prod,
// because codes in production logs would be a leak.
type LogSMS struct{ log *slog.Logger }

func NewLogSMS(l *slog.Logger) *LogSMS { return &LogSMS{log: l} }

func (s *LogSMS) SendSMS(ctx context.Context, phone, text string) error {
	s.log.InfoContext(ctx, "sms not sent (log provider)", "phone", phone, "text", text)
	return nil
}

// LogEmail writes emails to the log. Used in dev when SMTP_HOST is empty.
type LogEmail struct{ log *slog.Logger }

func NewLogEmail(l *slog.Logger) *LogEmail { return &LogEmail{log: l} }

func (s *LogEmail) SendEmail(ctx context.Context, to, subject, body string) error {
	s.log.InfoContext(ctx, "email not sent (no SMTP_HOST)", "to", to, "subject", subject, "body", body)
	return nil
}
