package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var texts = Texts{AppName: "Качалка", CodeTTL: 10 * time.Minute}

func TestPhoneCodeText(t *testing.T) {
	got, err := texts.PhoneCode("123456")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Качалка: код 123456. Никому его не сообщайте."; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	withHash := texts
	withHash.SMSAppHash = "FA+9qCX9VSu"
	got, err = withHash.PhoneCode("123456")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "\nFA+9qCX9VSu") {
		t.Fatalf("app hash must be the last line: %q", got)
	}
	if n := utf8.RuneCountInString(got); n > 70 {
		t.Fatalf("%d characters: no longer fits one Cyrillic SMS (70)", n)
	}
}

func TestEmailTexts(t *testing.T) {
	subject, body, err := texts.EmailVerification("654321")
	if err != nil {
		t.Fatal(err)
	}
	if subject != "Качалка: подтверждение email" || !strings.Contains(body, "654321") || !strings.Contains(body, "10 минут") {
		t.Fatalf("subject %q body %q", subject, body)
	}
	subject, body, err = texts.PasswordReset("111222")
	if err != nil {
		t.Fatal(err)
	}
	if subject != "Качалка: сброс пароля" || !strings.Contains(body, "111222") || !strings.Contains(body, "10 минут") {
		t.Fatalf("subject %q body %q", subject, body)
	}
}

func TestMinutes(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second:  "1 минуту",
		time.Minute:       "1 минуту",
		90 * time.Second:  "2 минуты",
		4 * time.Minute:   "4 минуты",
		5 * time.Minute:   "5 минут",
		11 * time.Minute:  "11 минут",
		12 * time.Minute:  "12 минут",
		21 * time.Minute:  "21 минуту",
		22 * time.Minute:  "22 минуты",
		111 * time.Minute: "111 минут",
	} {
		if got := minutes(d); got != want {
			t.Errorf("minutes(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestRecorder(t *testing.T) {
	r := NewRecorder()
	ctx := context.Background()
	if err := r.SendSMS(ctx, "+79991234567", "Качалка: код 123456."); err != nil {
		t.Fatal(err)
	}
	if err := r.SendEmail(ctx, "a@b.ru", "s", "Код: 654321"); err != nil {
		t.Fatal(err)
	}
	if got := r.LastCode("+79991234567"); got != "123456" {
		t.Fatalf("LastCode = %q", got)
	}
	if got := r.LastCode("a@b.ru"); got != "654321" {
		t.Fatalf("LastCode = %q", got)
	}
	if r.Count("+79991234567") != 1 || r.LastCode("nobody") != "" {
		t.Fatal("unexpected Count/LastCode")
	}
	boom := errors.New("boom")
	r.FailWith(boom)
	if err := r.SendSMS(ctx, "+79991234567", "x"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	r.FailWith(nil)
	if err := r.SendSMS(ctx, "+79991234567", "Качалка: код 777888."); err != nil || r.LastCode("+79991234567") != "777888" {
		t.Fatalf("FailWith(nil) did not restore sending: err=%v", err)
	}
}

// In dev the code is read from the log, so the log senders must keep it.
func TestLogSendersKeepTheCode(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	ctx := context.Background()
	if err := NewLogSMS(log).SendSMS(ctx, "+79991234567", "Качалка: код 123456."); err != nil {
		t.Fatal(err)
	}
	if err := NewLogEmail(log).SendEmail(ctx, "a@b.ru", "s", "Код: 654321"); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "123456") || !strings.Contains(out, "654321") {
		t.Fatalf("codes missing from the log:\n%s", out)
	}
}

func TestSMTPConfigHidesPassword(t *testing.T) {
	cfg := SMTPConfig{Host: "smtp.example.com", Port: 587, Username: "smtp-api-key", Password: "smtp-password", From: "noreply@example.com", TLS: "starttls"}
	var logged bytes.Buffer
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("smtp", "config", cfg)
	if out := fmt.Sprintf("%v %+v %#v %s", cfg, cfg, cfg, logged.String()); strings.Contains(out, "smtp-password") || strings.Contains(out, "smtp-api-key") {
		t.Fatalf("credentials leak: %s", out)
	}
}

func TestNewSMTPValidatesConfig(t *testing.T) {
	ok := SMTPConfig{Host: "localhost", Port: 1025, From: "noreply@example.com", TLS: "none"}
	if _, err := NewSMTP(ok); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	badTLS := ok
	badTLS.TLS = "maybe"
	if _, err := NewSMTP(badTLS); err == nil {
		t.Fatal("unknown TLS mode accepted")
	}
	badFrom := ok
	badFrom.From = "not an address"
	if _, err := NewSMTP(badFrom); err == nil {
		t.Fatal("bad From accepted")
	}
	noHost := ok
	noHost.Host = ""
	if _, err := NewSMTP(noHost); err == nil {
		t.Fatal("empty host accepted")
	}
	for _, mode := range []string{"starttls", "tls"} {
		withTLS := ok
		withTLS.TLS = mode
		if _, err := NewSMTP(withTLS); err != nil {
			t.Fatalf("TLS mode %q rejected: %v", mode, err)
		}
	}
}
