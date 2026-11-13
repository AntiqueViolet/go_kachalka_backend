package notify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/wneessen/go-mail"
)

const (
	// smtpTimeout limits each network step inside go-mail.
	smtpTimeout = 10 * time.Second
	// sendLimit caps a whole delivery. It stays below the HTTP server's WriteTimeout (15s),
	// so a hanging provider ends in a 503 rather than a dropped connection.
	sendLimit = 10 * time.Second
)

type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	TLS      string // starttls | tls | none
}

// String, GoString and LogValue keep the credentials out of logs and error messages
// (some providers use an API key as the username).
func (c SMTPConfig) String() string {
	return fmt.Sprintf("smtp{host=%s port=%d from=%s tls=%s}", c.Host, c.Port, c.From, c.TLS)
}

func (c SMTPConfig) GoString() string { return c.String() }

func (c SMTPConfig) LogValue() slog.Value { return slog.StringValue(c.String()) }

// SMTP sends plain-text email through any SMTP provider.
type SMTP struct {
	host string
	from string
	opts []mail.Option
}

var _ EmailSender = (*SMTP)(nil)

func NewSMTP(cfg SMTPConfig) (*SMTP, error) {
	opts := []mail.Option{mail.WithPort(cfg.Port), mail.WithTimeout(smtpTimeout)}
	switch cfg.TLS {
	case "starttls":
		opts = append(opts, mail.WithTLSPolicy(mail.TLSMandatory))
	case "tls":
		opts = append(opts, mail.WithSSL())
	case "none":
		opts = append(opts, mail.WithTLSPolicy(mail.NoTLS))
	default:
		return nil, fmt.Errorf("notify: unknown SMTP TLS mode %q", cfg.TLS)
	}
	if cfg.Username != "" {
		opts = append(opts,
			mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover),
			mail.WithUsername(cfg.Username),
			mail.WithPassword(cfg.Password))
	}
	// Fail fast on bad options; a client is created per message because sends are rare.
	if _, err := mail.NewClient(cfg.Host, opts...); err != nil {
		return nil, fmt.Errorf("notify: smtp client: %w", err)
	}
	if err := mail.NewMsg().From(cfg.From); err != nil {
		return nil, fmt.Errorf("notify: SMTP_FROM: %w", err)
	}
	return &SMTP{host: cfg.Host, from: cfg.From, opts: opts}, nil
}

func (s *SMTP) SendEmail(ctx context.Context, to, subject, body string) error {
	m := mail.NewMsg()
	if err := m.From(s.from); err != nil {
		return err
	}
	if err := m.To(to); err != nil {
		return err
	}
	m.Subject(subject)
	m.SetBodyString(mail.TypeTextPlain, body)
	c, err := mail.NewClient(s.host, s.opts...)
	if err != nil {
		return fmt.Errorf("notify: smtp client: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, sendLimit)
	defer cancel()
	// go-mail honours ctx only while dialing; later steps rely on socket deadlines. Delivering
	// in a goroutine lets the caller give up on time; it ends by itself within those deadlines.
	done := make(chan error, 1)
	go func() { done <- deliver(ctx, c, m) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("notify: smtp: %w", ctx.Err())
	}
}

// deliver sends m over a new connection.
func deliver(ctx context.Context, c *mail.Client, m *mail.Msg) error {
	client, err := c.DialToSMTPClientWithContext(ctx)
	if err != nil {
		return fmt.Errorf("notify: smtp dial: %w", err)
	}
	if err := c.SendWithSMTPClient(client, m); err != nil {
		_ = c.CloseWithSMTPClient(client)
		return fmt.Errorf("notify: smtp send: %w", err)
	}
	// The server has accepted the message. QUIT runs aside (within smtpTimeout) and its result
	// is ignored: a failed or hanging goodbye must not make the user ask for another code.
	go func() { _ = c.CloseWithSMTPClient(client) }()
	return nil
}
