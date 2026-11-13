package otp_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/apperr"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/clock"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/otp"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store/storetest"
)

var key = []byte("0123456789abcdef0123456789abcdef")

const phone = "+79991234567"

func setup(t *testing.T) (*otp.Service, *store.Store, *clock.Fake) {
	t.Helper()
	c := clock.NewFake(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	return otp.NewService(key, otp.Limits{Cooldown: time.Minute, PerTargetPerHour: 3}, c), storetest.Store(t), c
}

func issue(t *testing.T, svc *otp.Service, s *store.Store) otp.Issued {
	t.Helper()
	is, err := svc.Issue(context.Background(), s.Queries, otp.PurposePhoneRegister, phone, nil)
	if err != nil {
		t.Fatal(err)
	}
	return is
}

func wantAppErr(t *testing.T, err error, code string) *apperr.Error {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
	return ae
}

func TestIssueCheckConsume(t *testing.T) {
	svc, s, _ := setup(t)
	ctx := context.Background()
	is := issue(t, svc, s)
	c, err := s.GetCode(ctx, is.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(c.CodeHash) == is.Code {
		t.Fatal("code stored in plain text")
	}
	if err := svc.Check(ctx, s.Queries, c, is.Code); err != nil {
		t.Fatalf("Check(correct) = %v", err)
	}
	if err := svc.Consume(ctx, s.Queries, c.ID); err != nil {
		t.Fatal(err)
	}
	wantAppErr(t, svc.Consume(ctx, s.Queries, c.ID), "code_expired")
}

func TestWrongCodeBurnsAttempts(t *testing.T) {
	svc, s, _ := setup(t)
	ctx := context.Background()
	is := issue(t, svc, s)
	for want := otp.MaxAttempts - 1; want >= 0; want-- {
		c, _ := s.GetCode(ctx, is.ID)
		ae := wantAppErr(t, svc.Check(ctx, s.Queries, c, "000000x"), "invalid_code")
		if *ae.AttemptsLeft != want {
			t.Fatalf("attempts_left = %d, want %d", *ae.AttemptsLeft, want)
		}
	}
	c, _ := s.GetCode(ctx, is.ID)
	wantAppErr(t, svc.Check(ctx, s.Queries, c, is.Code), "code_expired")
}

// Parallel guesses read the same row before any attempt is counted; together they must still
// get at most MaxAttempts tries, or the 10^6 code space could be brute-forced with concurrency.
func TestParallelGuessesRespectAttemptLimit(t *testing.T) {
	svc, s, _ := setup(t)
	ctx := context.Background()
	is := issue(t, svc, s)
	c, err := s.GetCode(ctx, is.ID) // one stale snapshot shared by every request
	if err != nil {
		t.Fatal(err)
	}
	const guesses = 20
	errs := make(chan error, guesses)
	var wg sync.WaitGroup
	for i := range guesses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- svc.Check(ctx, s.Queries, c, fmt.Sprintf("x%05d", i)) // never the real code
		}()
	}
	wg.Wait()
	close(errs)
	checked := 0
	for err := range errs {
		var ae *apperr.Error
		if !errors.As(err, &ae) {
			t.Fatalf("unexpected error %v", err)
		}
		switch ae.Code {
		case "invalid_code":
			checked++
		case "code_expired":
		default:
			t.Fatalf("unexpected code %s", ae.Code)
		}
	}
	if checked != otp.MaxAttempts {
		t.Fatalf("%d guesses were checked, want exactly %d", checked, otp.MaxAttempts)
	}
}

func TestExpiredCode(t *testing.T) {
	svc, s, clk := setup(t)
	is := issue(t, svc, s)
	clk.Advance(otp.TTL)
	c, _ := s.GetCode(context.Background(), is.ID)
	wantAppErr(t, svc.Check(context.Background(), s.Queries, c, is.Code), "code_expired")
}

func TestNewCodeInvalidatesPrevious(t *testing.T) {
	svc, s, clk := setup(t)
	first := issue(t, svc, s)
	clk.Advance(time.Minute)
	issue(t, svc, s)
	c, _ := s.GetCode(context.Background(), first.ID)
	if c.ConsumedAt == nil {
		t.Fatal("previous code still active")
	}
}

func TestSendLimits(t *testing.T) {
	svc, s, clk := setup(t)
	ctx := context.Background()
	issue(t, svc, s)
	_, err := svc.Issue(ctx, s.Queries, otp.PurposePhoneRegister, phone, nil)
	if ae := wantAppErr(t, err, "rate_limited"); ae.RetryAfter != time.Minute {
		t.Fatalf("cooldown retry = %v", ae.RetryAfter)
	}
	clk.Advance(time.Minute)
	issue(t, svc, s)
	clk.Advance(time.Minute)
	issue(t, svc, s) // third in the hour
	clk.Advance(time.Minute)
	_, err = svc.Issue(ctx, s.Queries, otp.PurposePhoneRegister, phone, nil)
	if ae := wantAppErr(t, err, "rate_limited"); ae.RetryAfter != 57*time.Minute {
		t.Fatalf("hourly retry = %v, want 57m", ae.RetryAfter)
	}
	clk.Advance(57 * time.Minute)
	issue(t, svc, s)
}
