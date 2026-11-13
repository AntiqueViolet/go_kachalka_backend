package contact

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

func TestNormalizePhone(t *testing.T) {
	valid := []struct{ in, want string }{
		{"+7 999 123-45-67", "+79991234567"},
		{"8 (999) 123-45-67", "+79991234567"},
		{"  +79991234567  ", "+79991234567"},
		{"79991234567", "+79991234567"},
		{"9991234567", "+79991234567"},
		{"+7.999.123.45.67", "+79991234567"},
		{"+7\u00a0999\u00a0123\u00a045\u00a067", "+79991234567"},
		{"+1 650-253-0000", "+16502530000"},
	}
	for _, c := range valid {
		t.Run(c.in, func(t *testing.T) {
			if got, err := NormalizePhone(c.in); err != nil || got != c.want {
				t.Fatalf("NormalizePhone(%q) = %q, %v; want %q", c.in, got, err, c.want)
			}
		})
	}
	invalid := []string{
		"+7 495 123-45-67", // Moscow landline, cannot receive SMS
		"12345",
		"abc",
		"",
		"+7 999 FLOWERS",   // the parser would turn letters into digits
		"+7 999 abc-45-67", // a typo must not become another number
		"+79991234567;ext=123",
		"+79991234567 x123",
		"+7 999 123-45-67 доб. 12",
		"tel:+79991234567",
		"٨٩٩٩١٢٣٤٥٦٧", // Arabic-Indic digits
		strings.Repeat("1", maxPhoneInput+1),
	}
	for _, in := range invalid {
		t.Run("invalid "+in, func(t *testing.T) {
			if got, err := NormalizePhone(in); !errors.Is(err, ErrInvalidPhone) {
				t.Fatalf("NormalizePhone(%q) = %q, %v; want ErrInvalidPhone", in, got, err)
			}
		})
	}
}

func TestNormalizeEmail(t *testing.T) {
	longLocal := strings.Repeat("a", maxLocalLen)
	domain189 := strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	valid := []struct{ in, want string }{
		{" User@Example.RU ", "user@example.ru"},
		{"a.b+tag@mail.co.uk", "a.b+tag@mail.co.uk"},
		{"x@xn--e1afmkfd.xn--p1ai", "x@xn--e1afmkfd.xn--p1ai"},
		{longLocal + "@b.ru", longLocal + "@b.ru"},
		{longLocal + "@" + domain189, longLocal + "@" + domain189}, // exactly 254
	}
	for _, c := range valid {
		t.Run(c.in, func(t *testing.T) {
			if got, err := NormalizeEmail(c.in); err != nil || got != c.want {
				t.Fatalf("NormalizeEmail(%q) = %q, %v; want %q", c.in, got, err, c.want)
			}
		})
	}
	invalid := []string{
		"Name <a@b.ru>",
		"<a@b.ru>",
		"a@localhost",
		"not-an-email",
		"",
		"a@b.ru, c@d.ru",
		"a@b.ru\r\nBcc: x@y.ru",
		`"a b"@b.ru`,
		"a.@b.ru",
		"a@[127.0.0.1]",
		"a@127.0.0.1",
		"a@-b.ru",
		"a@b-.ru",
		"a@b_c.ru",
		"почта@пример.рф",
		"a\u200b@b.ru",   // zero-width space
		"\u212aate@b.ru", // Kelvin sign would lowercase to "k"
		longLocal + "a@b.ru",
		longLocal + "@" + domain189 + "d", // 255
	}
	for _, in := range invalid {
		t.Run("invalid "+in, func(t *testing.T) {
			if got, err := NormalizeEmail(in); !errors.Is(err, ErrInvalidEmail) {
				t.Fatalf("NormalizeEmail(%q) = %q, %v; want ErrInvalidEmail", in, got, err)
			}
		})
	}
}

var e164 = regexp.MustCompile(`^\+[1-9]\d{6,14}$`)

// The stored value is normalized again on every lookup, so normalization must be idempotent.
func FuzzNormalizePhone(f *testing.F) {
	for _, s := range []string{"+7 999 123-45-67", "8 (999) 123-45-67", "+1 650-253-0000", "+7 999 FLOWERS"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		a, err := NormalizePhone(s)
		if err != nil {
			return
		}
		if !e164.MatchString(a) {
			t.Fatalf("NormalizePhone(%q) = %q, not E.164", s, a)
		}
		if b, err := NormalizePhone(a); err != nil || b != a {
			t.Fatalf("not idempotent: %q -> %q -> %q, %v", s, a, b, err)
		}
	})
}

func FuzzNormalizeEmail(f *testing.F) {
	for _, s := range []string{" User@Example.RU ", "a.b+tag@mail.co.uk", "a@[127.0.0.1]"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		a, err := NormalizeEmail(s)
		if err != nil {
			return
		}
		if b, err := NormalizeEmail(a); err != nil || b != a {
			t.Fatalf("not idempotent: %q -> %q -> %q, %v", s, a, b, err)
		}
	})
}
