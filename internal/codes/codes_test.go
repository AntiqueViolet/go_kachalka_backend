package codes_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/apperr"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/clock"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/codes"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/notify"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/otp"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store/storetest"
)

func setup(t *testing.T) (*codes.Sender, *store.Store, *notify.Recorder) {
	t.Helper()
	pool := storetest.Pool(t)
	storetest.Warm(t, pool, 20)
	st := store.NewStore(pool)
	clk := clock.NewFake(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	rec := notify.NewRecorder()
	s := codes.NewSender(codes.Deps{
		Store: st,
		OTP:   otp.NewService([]byte("0123456789abcdef0123456789abcdef"), otp.Limits{Cooldown: time.Minute, PerTargetPerHour: 5}, clk),
		SMS:   rec,
		Email: rec,
		Texts: notify.Texts{AppName: "Качалка", CodeTTL: otp.TTL},
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return s, st, rec
}

func TestSendPhoneCode(t *testing.T) {
	s, st, rec := setup(t)
	id, err := s.SendPhoneCode(context.Background(), "+79991234567")
	if err != nil {
		t.Fatal(err)
	}
	if rec.LastCode("+79991234567") == "" {
		t.Fatal("no SMS with a code")
	}
	c, err := st.GetCode(context.Background(), id)
	if err != nil || c.Purpose != string(otp.PurposePhoneRegister) || c.UserID != nil {
		t.Fatalf("stored code %+v, %v", c, err)
	}
}

func TestSendEmailCodePicksTemplate(t *testing.T) {
	s, st, rec := setup(t)
	ctx := context.Background()
	u, err := st.CreateUser(ctx, store.CreateUserParams{ID: uuid.Must(uuid.NewV7()), Phone: "+79991234567", PasswordHash: "h", Email: "a@example.com", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SendEmailCode(ctx, otp.PurposePasswordReset, u.ID, u.Email); err != nil {
		t.Fatal(err)
	}
	if rec.Count("a@example.com") != 1 || rec.LastCode("a@example.com") == "" {
		t.Fatal("reset email not sent")
	}
	c, err := st.LatestActiveUserCode(ctx, store.LatestActiveUserCodeParams{Purpose: string(otp.PurposePasswordReset), UserID: u.ID})
	if err != nil || c.Target != "a@example.com" {
		t.Fatalf("code %+v, %v", c, err)
	}
}

// A failed delivery is not counted: the user can retry at once instead of hitting the cooldown.
func TestDeliveryFailureIsUnavailable(t *testing.T) {
	s, _, rec := setup(t)
	rec.FailWith(errors.New("provider down"))
	_, err := s.SendPhoneCode(context.Background(), "+79991234567")
	if !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	rec.FailWith(nil)
	if _, err := s.SendPhoneCode(context.Background(), "+79991234567"); err != nil {
		t.Fatalf("retry after a failed delivery: %v", err)
	}
}

// Parallel requests for one address are serialised: one code goes out, the rest hit the
// cooldown, and only one code is active.
func TestParallelSendsRespectLimits(t *testing.T) {
	s, _, rec := setup(t)
	const phone = "+79991234567"
	var sent atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.SendPhoneCode(context.Background(), phone)
			var ae *apperr.Error
			switch {
			case err == nil:
				sent.Add(1)
			case !errors.As(err, &ae) || ae.Code != "rate_limited":
				t.Errorf("unexpected error %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if sent.Load() != 1 || rec.Count(phone) != 1 {
		t.Fatalf("%d codes issued, %d SMS sent; want exactly one", sent.Load(), rec.Count(phone))
	}
}

func TestBackgroundSend(t *testing.T) {
	s, st, rec := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	u, err := st.CreateUser(ctx, store.CreateUserParams{ID: uuid.Must(uuid.NewV7()), Phone: "+79991234567", PasswordHash: "h", Email: "a@example.com", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	s.SendEmailCodeInBackground(ctx, otp.PurposePasswordReset, u.ID, u.Email)
	cancel() // the request is over; the send must not depend on its context
	s.Wait()
	if rec.LastCode("a@example.com") == "" {
		t.Fatal("background email not sent")
	}
}

func TestDeliverEmailCode(t *testing.T) {
	s, _, rec := setup(t)
	if err := s.DeliverEmailCode(context.Background(), otp.PurposeEmailVerify, "b@example.com", "123456"); err != nil {
		t.Fatal(err)
	}
	if got := rec.LastCode("b@example.com"); got != "123456" {
		t.Fatalf("code %q", got)
	}
}
