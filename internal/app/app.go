// Package app wires configuration, storage, services and the HTTP API together.
package app

import (
	"context"
	"crypto/ed25519"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/account"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/auth"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/clock"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/codes"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/config"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/httpapi"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/notify"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/otp"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/password"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/ratelimit"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/token"
)

const (
	registrationTTL = 15 * time.Minute
	keepDeadSession = 30 * 24 * time.Hour
	keepCodes       = 24 * time.Hour
)

type Options struct {
	Config config.Config
	Pool   *pgxpool.Pool
	Log    *slog.Logger

	// Optional overrides for tests; zero values mean "build from Config".
	Clock          clock.Clock
	SMS            notify.SMSSender
	Email          notify.EmailSender
	JWTKey         ed25519.PrivateKey
	PasswordParams *password.Params
}

type App struct {
	Handler http.Handler
	store   *store.Store
	clock   clock.Clock
	log     *slog.Logger
	codes   *codes.Sender
}

func New(o Options) (*App, error) {
	cfg := o.Config
	clk := o.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	key, err := jwtKey(o)
	if err != nil {
		return nil, err
	}
	params := password.DefaultParams
	if o.PasswordParams != nil {
		params = *o.PasswordParams
	}
	hasher, err := password.NewHasher(params, cfg.PasswordHashConcurrency)
	if err != nil {
		return nil, err
	}
	sms, email, err := senders(o)
	if err != nil {
		return nil, err
	}

	st := store.NewStore(o.Pool)
	signer := token.NewSigner(token.SignerConfig{
		Key: key, Issuer: cfg.JWTIssuer, AccessTTL: cfg.AccessTokenTTL, RegistrationTTL: registrationTTL, Clock: clk,
	})
	otpSvc := otp.NewService(cfg.Keys.OTP, otp.Limits{Cooldown: cfg.Rate.CodeCooldown, PerTargetPerHour: cfg.Rate.CodesPerTargetPerHour}, clk)
	sender := codes.NewSender(codes.Deps{
		Store: st, OTP: otpSvc, SMS: sms, Email: email, Log: o.Log,
		Texts: notify.Texts{AppName: cfg.AppName, SMSAppHash: cfg.SMSAppHash, CodeTTL: otp.TTL},
	})
	authSvc := auth.New(auth.Deps{
		Store: st, Hasher: hasher, Signer: signer, Refresh: token.NewRefreshCodec(cfg.Keys.Refresh),
		OTP: otpSvc, Codes: sender, Clock: clk, Log: o.Log,
		LoginFailures: ratelimit.Per(cfg.Rate.LoginFailuresPerPhone, cfg.Rate.LoginFailuresWindow, clk),
		RefreshTTL:    cfg.RefreshTokenTTL,
		RefreshGrace:  cfg.RefreshGrace,
	})
	accountSvc := account.New(account.Deps{
		Store: st, Hasher: hasher, OTP: otpSvc, Codes: sender, Clock: clk, Log: o.Log,
		// Its own limiter: sharing auth's would mix the two flows' failure counts.
		PasswordFailures: ratelimit.Per(cfg.Rate.LoginFailuresPerPhone, cfg.Rate.LoginFailuresWindow, clk),
	})

	h := httpapi.New(httpapi.Config{
		Auth: authSvc, Account: accountSvc, Signer: signer, DB: st, Log: o.Log,
		TrustedProxies: cfg.ProxyPrefixes,
		Limits: httpapi.Limits{
			Login:   ratelimit.Per(cfg.Rate.LoginPerIPPerMinute, time.Minute, clk),
			Refresh: ratelimit.Per(cfg.Rate.RefreshPerIPPerMinute, time.Minute, clk),
			Codes:   ratelimit.Per(cfg.Rate.CodesPerIPPerHour, time.Hour, clk),
		},
	})
	return &App{Handler: h, store: st, clock: clk, log: o.Log, codes: sender}, nil
}

func jwtKey(o Options) (ed25519.PrivateKey, error) {
	switch {
	case o.JWTKey != nil:
		return o.JWTKey, nil
	case o.Config.JWTPrivateKeyFile != "":
		return token.LoadPrivateKey(o.Config.JWTPrivateKeyFile)
	default: // dev only: config requires the key file in prod
		o.Log.Warn("JWT_PRIVATE_KEY_FILE is empty: using a temporary key, tokens die on restart")
		return token.GenerateKey()
	}
}

func senders(o Options) (notify.SMSSender, notify.EmailSender, error) {
	sms, email := o.SMS, o.Email
	if sms == nil {
		// config.validate guarantees "log" is the only provider, and only outside prod.
		sms = notify.NewLogSMS(o.Log)
	}
	if email == nil {
		if o.Config.SMTP.Host == "" {
			o.Log.Warn("SMTP_HOST is empty: emails are written to the log")
			email = notify.NewLogEmail(o.Log)
		} else {
			c := o.Config.SMTP
			smtp, err := notify.NewSMTP(notify.SMTPConfig{
				Host: c.Host, Port: c.Port, Username: c.Username, Password: c.Password, From: c.From, TLS: c.TLS,
			})
			if err != nil {
				return nil, nil, err
			}
			email = smtp
		}
	}
	return sms, email, nil
}

// Cleanup deletes sessions dead for over 30 days and codes older than a day.
func (a *App) Cleanup(ctx context.Context) error {
	now := a.clock.Now()
	sessions, err := a.store.DeleteStaleSessions(ctx, now.Add(-keepDeadSession))
	if err != nil {
		return err
	}
	codesDeleted, err := a.store.DeleteOldCodes(ctx, now.Add(-keepCodes))
	if err != nil {
		return err
	}
	a.log.InfoContext(ctx, "cleanup done", "sessions", sessions, "codes", codesDeleted)
	return nil
}

// Wait blocks until background email sends have finished; call it after the HTTP
// server stops so shutdown does not drop in-flight messages.
func (a *App) Wait() { a.codes.Wait() }

// RunCleanup calls Cleanup at once and then every interval until ctx is cancelled.
// The immediate pass matters: a service redeployed more often than the interval
// would otherwise never clean anything.
func (a *App) RunCleanup(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := a.Cleanup(ctx); err != nil && ctx.Err() == nil {
			a.log.ErrorContext(ctx, "cleanup failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
