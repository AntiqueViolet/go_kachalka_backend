package password

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fastParams keep tests quick; production uses DefaultParams.
var fastParams = Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

func newHasher(t *testing.T, p Params, concurrency int) *Hasher {
	t.Helper()
	h, err := NewHasher(p, concurrency)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// testCtx bounds every call, so a lost semaphore slot fails the test instead of hanging it.
func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestValidate(t *testing.T) {
	cases := []struct {
		pw   string
		want error
	}{
		{"1234567", ErrTooShort},
		{strings.Repeat("e"+string(rune(0x301)), 4), ErrTooShort}, // 8 runes, but 4 "é" after NFKC
		{"пароль12", nil},                                         // 8 runes, 14 bytes
		{strings.Repeat("a", MaxLength), nil},
		{strings.Repeat("я", MaxLength), nil}, // 256 bytes: the limit counts characters
		{strings.Repeat("a", MaxLength+1), ErrTooLong},
	}
	for _, c := range cases {
		if err := Validate(c.pw); !errors.Is(err, c.want) {
			t.Errorf("Validate(%d runes) = %v, want %v", len([]rune(c.pw)), err, c.want)
		}
	}
}

func TestFieldError(t *testing.T) {
	if fe := FieldError("password", "long enough"); fe != nil {
		t.Fatalf("valid password: %+v", fe)
	}
	if fe := FieldError("new_password", "short"); fe == nil || fe.Field != "new_password" || fe.Code != "too_short" {
		t.Fatalf("short password: %+v", fe)
	}
	if fe := FieldError("password", strings.Repeat("a", 129)); fe == nil || fe.Code != "too_long" {
		t.Fatalf("long password: %+v", fe)
	}
}

func TestDefaultParamsAreOWASP(t *testing.T) {
	if want := (Params{Memory: 19456, Time: 2, Threads: 1, SaltLen: 16, KeyLen: 32}); DefaultParams != want {
		t.Fatalf("DefaultParams = %+v, want %+v", DefaultParams, want)
	}
}

func TestNewHasherRejectsBadParams(t *testing.T) {
	bad := []Params{
		{},
		{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 0},
		{Memory: 64, Time: 1, Threads: 1, SaltLen: 1, KeyLen: 32},
		{Memory: 1 << 30, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32},
		{Memory: 64, Time: 1000, Threads: 1, SaltLen: 16, KeyLen: 32},
	}
	for _, p := range bad {
		if _, err := NewHasher(p, 1); err == nil {
			t.Errorf("NewHasher(%+v) accepted", p)
		}
	}
	if _, err := NewHasher(fastParams, 0); err == nil {
		t.Error("NewHasher with concurrency 0 accepted")
	}
}

func TestHashVerifyRoundTrip(t *testing.T) {
	h := newHasher(t, fastParams, 2)
	ctx := testCtx(t)
	enc, err := h.Hash(ctx, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("unexpected encoding %q", enc)
	}
	ok, rehash, err := h.Verify(ctx, "correct horse", enc)
	if err != nil || !ok || rehash {
		t.Fatalf("Verify(correct) = %v, %v, %v", ok, rehash, err)
	}
	ok, _, err = h.Verify(ctx, "wrong horse", enc)
	if err != nil || ok {
		t.Fatalf("Verify(wrong) = %v, %v", ok, err)
	}
	again, err := h.Hash(ctx, "correct horse")
	if err != nil || again == enc {
		t.Fatal("two hashes of one password must differ (random salt)")
	}
}

func TestVerifyNormalizesUnicode(t *testing.T) {
	h := newHasher(t, fastParams, 1)
	ctx := testCtx(t)
	composed := "пароль-" + string(rune(0x0439))                          // й as one code point
	decomposed := "пароль-" + string(rune(0x0438)) + string(rune(0x0306)) // и + combining breve
	enc, err := h.Hash(ctx, composed)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _, err := h.Verify(ctx, decomposed, enc); err != nil || !ok {
		t.Fatalf("decomposed form of the same password rejected: %v, %v", ok, err)
	}
}

func TestVerifyFlagsOutdatedParams(t *testing.T) {
	ctx := testCtx(t)
	enc, err := newHasher(t, fastParams, 1).Hash(ctx, "password123")
	if err != nil {
		t.Fatal(err)
	}
	stronger := fastParams
	stronger.Time = 2
	h := newHasher(t, stronger, 1)
	ok, rehash, err := h.Verify(ctx, "password123", enc)
	if err != nil || !ok || !rehash {
		t.Fatalf("Verify = %v, %v, %v; want ok and needsRehash", ok, rehash, err)
	}
	if ok, rehash, err := h.Verify(ctx, "wrong password", enc); err != nil || ok || rehash {
		t.Fatalf("wrong password: %v, %v, %v; needsRehash must stay false", ok, rehash, err)
	}
}

func TestVerifyMalformed(t *testing.T) {
	h := newHasher(t, fastParams, 1)
	salt, key := strings.Repeat("A", 22), strings.Repeat("A", 43) // 16 and 32 zero bytes
	phc := func(params string) string { return fmt.Sprintf("$argon2id$v=19$%s$%s$%s", params, salt, key) }
	cases := []string{
		"",
		"plain",
		"$argon2i$v=19$m=64,t=1,p=1$" + salt + "$" + key,
		"$argon2id$v=18$m=64,t=1,p=1$" + salt + "$" + key,
		"junk" + phc("m=64,t=1,p=1"),
		phc("m=64,t=0,p=1"), // argon2 would panic
		phc("m=64,t=1,p=0"), // argon2 would panic
		phc("m=0,t=1,p=1"),
		phc("m=4294967295,t=1,p=1"), // 4 TiB: an unrecoverable out-of-memory crash
		phc("m=64,t=4294967295,p=1"),
		phc("m=64,t=1,p=256"),
		phc("m=064,t=1,p=1"),
		phc("m=64,t=1,p=1junk"),
		"$argon2id$v=19$m=64,t=1,p=1$!!!!$" + key,
		"$argon2id$v=19$m=64,t=1,p=1$" + strings.Repeat("A", 21) + "B$" + key, // non-canonical base64 bits
		"$argon2id$v=19$m=64,t=1,p=1$$" + key,
		"$argon2id$v=19$m=64,t=1,p=1$" + salt + "$",
	}
	for _, enc := range cases {
		if _, _, err := h.Verify(testCtx(t), "x", enc); !errors.Is(err, ErrMalformedHash) {
			t.Errorf("Verify(%q) err = %v, want ErrMalformedHash", enc, err)
		}
	}
	if len(h.slots) != 0 {
		t.Fatal("malformed hashes must not hold a slot")
	}
}

func TestSlotsAreReleased(t *testing.T) {
	h := newHasher(t, fastParams, 1)
	ctx := testCtx(t)
	enc, err := h.Hash(ctx, "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.Verify(ctx, "password123", enc); err != nil {
		t.Fatal(err)
	}
	if err := h.VerifyDummy(ctx, "password123"); err != nil {
		t.Fatal(err)
	}
	if len(h.slots) != 0 {
		t.Fatalf("%d slot(s) still held", len(h.slots))
	}
}

// Regression: a panic inside argon2 used to keep the slot forever, after which every
// call returned ErrBusy.
func TestDeriveReleasesSlotOnPanic(t *testing.T) {
	h := newHasher(t, fastParams, 1)
	func() {
		defer func() { _ = recover() }()
		_, _ = h.derive(testCtx(t), "x", make([]byte, 16), Params{Memory: 64, Time: 0, Threads: 1, KeyLen: 32})
	}()
	if len(h.slots) != 0 {
		t.Fatalf("slot leaked after panic: %d held", len(h.slots))
	}
}

func TestBusyWhenSlotsFullAndContextDone(t *testing.T) {
	h := newHasher(t, fastParams, 1)
	enc, err := h.Hash(testCtx(t), "password123")
	if err != nil {
		t.Fatal(err)
	}
	h.slots <- struct{}{} // occupy the only slot
	defer h.release()
	if _, err := h.Hash(cancelled(), "password123"); !errors.Is(err, ErrBusy) {
		t.Errorf("Hash err = %v, want ErrBusy", err)
	}
	if _, _, err := h.Verify(cancelled(), "password123", enc); !errors.Is(err, ErrBusy) {
		t.Errorf("Verify err = %v, want ErrBusy", err)
	}
	if err := h.VerifyDummy(cancelled(), "password123"); !errors.Is(err, ErrBusy) {
		t.Errorf("VerifyDummy err = %v, want ErrBusy (it must go through the same slots)", err)
	}
}

func TestFreeSlotWinsOverCancelledContext(t *testing.T) {
	h := newHasher(t, fastParams, 1)
	for range 50 { // a select with two ready cases picks at random; the fast path must not
		if _, err := h.Hash(cancelled(), "password123"); err != nil {
			t.Fatalf("Hash with a free slot failed: %v", err)
		}
	}
}

func TestVerifyDummyMatchesHasherParams(t *testing.T) {
	h := newHasher(t, fastParams, 1)
	p, _, _, err := decode(h.dummy)
	if err != nil || p != h.params {
		t.Fatalf("dummy params %+v, %v; want %+v", p, err, h.params)
	}
	if err := h.VerifyDummy(testCtx(t), "anything"); err != nil {
		t.Fatal(err)
	}
}
