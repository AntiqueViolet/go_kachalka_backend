# go_kachalka backend — часть 1: фундамент. План реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Каркас Go-модуля и все библиотеки предметной области, не зависящие от БД и HTTP: время, ошибки API, нормализация контактов, пароли, коды, токены, лимиты, отправка сообщений, конфигурация.

**Architecture:** Маленькие пакеты в `internal/` с одной ответственностью и unit-тестами без внешних зависимостей. Время подменяется через `clock.Clock`, ключи и параметры передаются явно — пакеты не читают окружение сами (кроме `config`). Конструкторы, которые получают уже проверенный `config`, паникуют на заведомо неверных аргументах (ошибка программиста); всё, что может не получиться во время работы, возвращает ошибку.

**Tech Stack:** Go 1.27 (toolchain 1.27.4), `golang.org/x/crypto/argon2`, `golang.org/x/text` (NFKC), `google/uuid`, `crypto/hkdf`, `golang-jwt/jwt/v5` (EdDSA), `nyaruka/phonenumbers`, `wneessen/go-mail`, `caarlos0/env/v11`.

**Спецификация:** `docs/superpowers/specs/2026-10-22-auth-backend-design.md`
**Части плана:** 1 — фундамент (этот файл) → 2 — БД, сервисы, HTTP API → 3 — комплект поставки. Каждая часть заканчивается рабочим, протестированным кодом.

---

## Правила выполнения

- **Коммиты и push делает только владелец репозитория.** На шагах «Контрольная точка» исполнитель не запускает `git commit`/`git push`, а выдаёт владельцу готовые команды из шага и ждёт подтверждения.
- Весь код в шагах заранее собран и прогнан тестами (Windows, Go 1.27.4, Docker Desktop). Переносите файлы как есть; если тест ведёт себя не так, как написано в шаге, — остановитесь и разберитесь, а не подгоняйте код.
- Команды выполняются из корня репозитория в PowerShell или Git Bash, если не сказано иное.
- Версии зависимостей фиксируются командами `go get пакет@версия` — именно с ними код проверен.

## Файлы части 1

| Файл | Ответственность |
|---|---|
| `.gitattributes`, `.gitignore` | LF в репозитории; секреты и данные сервера не попадают в git |
| `go.mod` | модуль `github.com/AntiqueViolet/go_kachalka_backend`, Go 1.27, toolchain 1.27.4 |
| `internal/clock/` | текущее время; `Fake` для тестов |
| `internal/apperr/` | ошибки для клиента со стабильными кодами (RFC 9457) |
| `internal/contact/` | нормализация телефона (E.164, только мобильные) и email |
| `internal/password/` | валидация длины, argon2id, ограничение параллельности, фиктивная проверка |
| `internal/otp/code.go` | генерация 6-значных кодов и их HMAC |
| `internal/token/` | Ed25519-ключи, access/registration JWT, refresh-токены и правила ротации |
| `internal/ratelimit/` | лимитеры в памяти по ключу |
| `internal/notify/` | интерфейсы отправки, log-заглушки, Recorder для тестов, тексты, SMTP |
| `internal/config/` | чтение и проверка переменных окружения, вывод ключей из `APP_SECRET` |

---

### Task 1: Каркас репозитория

**Files:**
- Create: `.gitattributes`, `.gitignore`
- Create (командой): `go.mod`

- [ ] **Step 1: Создать `.gitattributes`**

<!-- file: .gitattributes -->
```text
# LF everywhere: shell scripts, Caddyfile and Dockerfile run inside Linux containers.
* text=auto eol=lf
```

- [ ] **Step 2: Создать `.gitignore`**

<!-- file: .gitignore -->
```text
# Secrets and server data (.env.prod, .env.bak and similar copies included)
.env
.env.*
!.env.example
deploy/secrets/
deploy/backups/
/secrets/
/backups/
*.pem
*.key
*.dump
*.sql.gz
*.log

# Build and test output (go build on Windows writes api.exe to the repo root)
/bin/
/dist/
*.exe
*.test
*.out

# Editors and OS
.idea/
.vscode/
.DS_Store
Thumbs.db
desktop.ini
```

- [ ] **Step 3: Создать модуль и зафиксировать версию Go**

```bash
go mod init github.com/AntiqueViolet/go_kachalka_backend
go mod edit -go=1.27.0 -toolchain=go1.27.4
```

- [ ] **Step 4: Проверить версию**

Run: `go version`
Expected: `go version go1.27.4 ...` (при первом запуске Go сам скачает toolchain 1.27.4: в нём закрыты уязвимости stdlib, которые находит `govulncheck`).

- [ ] **Step 5: Контрольная точка** — выдать владельцу команды:

```bash
git add docs/superpowers .gitattributes .gitignore go.mod
git commit -m "chore: init Go module"
```

---

### Task 2: Пакет `clock`

**Files:**
- Create: `internal/clock/clock.go`
- Test: `internal/clock/clock_test.go`

- [ ] **Step 1: Написать тест**

<!-- file: internal/clock/clock_test.go -->
```go
package clock

import (
	"sync"
	"testing"
	"time"
)

var (
	_ Clock = Real{}
	_ Clock = (*Fake)(nil)
)

func TestRealTracksWallClockInUTCMicroseconds(t *testing.T) {
	for range 100 {
		before := time.Now().Truncate(time.Microsecond)
		got := Real{}.Now()
		after := time.Now()
		if got.Location() != time.UTC {
			t.Fatalf("location = %v, want UTC", got.Location())
		}
		if got.Nanosecond()%1000 != 0 {
			t.Fatalf("nanoseconds %d not truncated to microseconds", got.Nanosecond())
		}
		if got.Before(before) || got.After(after) {
			t.Fatalf("Now() = %v, want within [%v, %v]", got, before, after)
		}
	}
}

func TestNewFakeNormalizesToUTCMicroseconds(t *testing.T) {
	in := time.Date(2026, 1, 1, 15, 0, 0, 123_456_789, time.FixedZone("UTC+3", 3*60*60))
	got := NewFake(in).Now()
	if want := time.Date(2026, 1, 1, 12, 0, 0, 123_456_000, time.UTC); !got.Equal(want) || got.Location() != time.UTC {
		t.Fatalf("Now() = %v, want %v in UTC", got, want)
	}
}

func TestFakeAdvance(t *testing.T) {
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c := NewFake(start)
	if got := c.Now(); !got.Equal(start) {
		t.Fatalf("Now() before Advance = %v, want %v", got, start)
	}
	c.Advance(90 * time.Second)
	c.Advance(-30 * time.Second)
	if got, want := c.Now(), start.Add(60*time.Second); !got.Equal(want) {
		t.Fatalf("Now() = %v, want %v", got, want)
	}
}

func TestFakeConcurrentUse(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewFake(start)
	const workers, steps = 8, 1000
	var wg sync.WaitGroup
	for range workers {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range steps {
				c.Advance(time.Second)
			}
		}()
		go func() {
			defer wg.Done()
			for range steps {
				_ = c.Now()
			}
		}()
	}
	wg.Wait()
	if got, want := c.Now(), start.Add(workers*steps*time.Second); !got.Equal(want) {
		t.Fatalf("Now() = %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/clock/`
Expected: FAIL — ошибки компиляции `undefined: Clock`, `undefined: Real`, `undefined: Fake`, `undefined: NewFake`.

- [ ] **Step 3: Реализация**

<!-- file: internal/clock/clock.go -->
```go
// Package clock abstracts the current time so tests can control it.
package clock

import (
	"sync"
	"time"
)

// Clock returns the current time in UTC with microsecond precision.
type Clock interface {
	Now() time.Time
}

// Real is the wall clock. Times are UTC with microsecond precision to match Postgres timestamptz.
type Real struct{}

func (Real) Now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// Fake is a manually advanced clock for tests. It is safe for concurrent use; create it with NewFake.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

// NewFake returns a clock stopped at t, normalized the same way as Real.
func NewFake(t time.Time) *Fake { return &Fake{now: t.UTC().Truncate(time.Microsecond)} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Advance moves the clock by d, which may be negative.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}
```

- [ ] **Step 4: Тест проходит**

Run: `go test ./internal/clock/`
Expected: `ok  github.com/AntiqueViolet/go_kachalka_backend/internal/clock`

- [ ] **Step 5: Контрольная точка** — выдать владельцу команды:

```bash
git add internal/clock
git commit -m "feat(clock): add real and fake clocks"
```

---

### Task 3: Пакет `apperr`

Все ошибки, которые видит клиент, — значения `*apperr.Error` со статусом и стабильным `code`. HTTP-слой (часть 2) превращает их в problem details; любая другая ошибка станет `500 internal_error`.

**Files:**
- Create: `internal/apperr/apperr.go`
- Test: `internal/apperr/apperr_test.go`

- [ ] **Step 1: Написать тест**

<!-- file: internal/apperr/apperr_test.go -->
```go
package apperr

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestCatalog pins every sentinel to the status and code from spec section 10:
// clients depend on these codes, so a typo must fail here.
func TestCatalog(t *testing.T) {
	cases := []struct {
		err    *Error
		status int
		code   string
	}{
		{ErrBadRequest, http.StatusBadRequest, "bad_request"},
		{ErrUnauthorized, http.StatusUnauthorized, "unauthorized"},
		{ErrInvalidCredentials, http.StatusUnauthorized, "invalid_credentials"},
		{ErrInvalidRegistrationToken, http.StatusUnauthorized, "invalid_registration_token"},
		{ErrInvalidRefreshToken, http.StatusUnauthorized, "invalid_refresh_token"},
		{ErrRefreshTokenReused, http.StatusUnauthorized, "refresh_token_reused"},
		{ErrWrongPassword, http.StatusForbidden, "wrong_password"},
		{ErrPhoneTaken, http.StatusConflict, "phone_taken"},
		{ErrEmailTaken, http.StatusConflict, "email_taken"},
		{ErrEmailAlreadyVerified, http.StatusConflict, "email_already_verified"},
		{ErrCodeExpired, http.StatusGone, "code_expired"},
		{ErrInternal, http.StatusInternalServerError, "internal_error"},
		{ErrUnavailable, http.StatusServiceUnavailable, "service_unavailable"},
		{Validation(), http.StatusUnprocessableEntity, "validation_failed"},
		{InvalidCode(1), http.StatusUnprocessableEntity, "invalid_code"},
		{RateLimited(time.Minute), http.StatusTooManyRequests, "rate_limited"},
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.err.Status != c.status || c.err.Code != c.code || c.err.Title == "" {
			t.Errorf("%s: got %d %q title %q, want %d %q", c.code, c.err.Status, c.err.Code, c.err.Title, c.status, c.code)
		}
		if seen[c.err.Code] {
			t.Errorf("duplicate code %q", c.err.Code)
		}
		seen[c.err.Code] = true
	}
}

func TestSentinelSurvivesWrapping(t *testing.T) {
	err := fmt.Errorf("login: %w", ErrInvalidCredentials)
	var ae *Error
	if !errors.As(err, &ae) || ae != ErrInvalidCredentials {
		t.Fatal("errors.As did not find the wrapped sentinel")
	}
	if !errors.Is(err, ErrInvalidCredentials) || errors.Is(err, ErrUnauthorized) {
		t.Fatal("errors.Is must match the wrapped sentinel and only it")
	}
}

func TestValidationKeepsFields(t *testing.T) {
	e := Validation(FieldError{Field: "password", Code: FieldTooShort}, FieldError{Field: "email", Code: FieldInvalidEmail})
	if len(e.Fields) != 2 || e.Fields[0] != (FieldError{Field: "password", Code: "too_short"}) || e.Fields[1].Code != "invalid_email" {
		t.Fatalf("fields %+v", e.Fields)
	}
}

func TestInvalidCodeCarriesAttempts(t *testing.T) {
	last := InvalidCode(0)
	if last.AttemptsLeft == nil || *last.AttemptsLeft != 0 {
		t.Fatal("attempts_left 0 must be present, not dropped")
	}
	a, b := InvalidCode(1), InvalidCode(2)
	if *a.AttemptsLeft != 1 || *b.AttemptsLeft != 2 || a.AttemptsLeft == b.AttemptsLeft {
		t.Fatal("each error needs its own attempts counter")
	}
}

func TestRateLimitedRetryAfter(t *testing.T) {
	if got := RateLimited(90 * time.Second).RetryAfter; got != 90*time.Second {
		t.Fatalf("RetryAfter = %v, want 90s", got)
	}
	for _, d := range []time.Duration{0, -time.Second, time.Millisecond} {
		if got := RateLimited(d).RetryAfter; got != time.Second {
			t.Errorf("RateLimited(%v).RetryAfter = %v, want 1s", d, got)
		}
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/apperr/`
Expected: FAIL — ошибки компиляции `undefined: Error`, `undefined: ErrBadRequest` и другие имена пакета.

- [ ] **Step 3: Реализация**

<!-- file: internal/apperr/apperr.go -->
```go
// Package apperr defines client-facing errors with stable codes.
// The HTTP layer renders them as RFC 9457 problem details; any other error becomes 500.
package apperr

import (
	"net/http"
	"time"
)

// Field error codes used inside validation_failed.
const (
	FieldInvalidPhone = "invalid_phone"
	FieldInvalidEmail = "invalid_email"
	FieldTooShort     = "too_short"
	FieldTooLong      = "too_long"
	FieldRequired     = "required"
)

type FieldError struct {
	Field string `json:"field"`
	Code  string `json:"code"`
}

// Error is an error the client sees. Code is stable and machine-readable, Title is for people.
// Fields is set only for validation_failed, AttemptsLeft only for invalid_code,
// RetryAfter only for rate_limited.
type Error struct {
	Status       int
	Code         string
	Title        string
	Fields       []FieldError
	AttemptsLeft *int
	RetryAfter   time.Duration
}

func (e *Error) Error() string { return e.Code }

func New(status int, code, title string) *Error {
	return &Error{Status: status, Code: code, Title: title}
}

// Validation builds a 422 validation_failed error listing the bad fields.
func Validation(fields ...FieldError) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Title: "Validation failed", Fields: fields}
}

// InvalidCode is returned for a wrong verification code; attemptsLeft may be 0 on the last try.
func InvalidCode(attemptsLeft int) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "invalid_code", Title: "Invalid code", AttemptsLeft: &attemptsLeft}
}

// RateLimited is returned when a limit is exceeded; RetryAfter goes to the Retry-After header.
// It is at least one second, so a wait computed at a window boundary never drops the header.
func RateLimited(retryAfter time.Duration) *Error {
	return &Error{Status: http.StatusTooManyRequests, Code: "rate_limited", Title: "Too many requests", RetryAfter: max(retryAfter, time.Second)}
}

// Shared sentinel errors. Never mutate them; compare with errors.Is.
var (
	ErrBadRequest               = New(http.StatusBadRequest, "bad_request", "Bad request")
	ErrUnauthorized             = New(http.StatusUnauthorized, "unauthorized", "Unauthorized")
	ErrInvalidCredentials       = New(http.StatusUnauthorized, "invalid_credentials", "Invalid phone or password")
	ErrInvalidRegistrationToken = New(http.StatusUnauthorized, "invalid_registration_token", "Invalid registration token")
	ErrInvalidRefreshToken      = New(http.StatusUnauthorized, "invalid_refresh_token", "Invalid refresh token")
	ErrRefreshTokenReused       = New(http.StatusUnauthorized, "refresh_token_reused", "Refresh token reused")
	ErrWrongPassword            = New(http.StatusForbidden, "wrong_password", "Wrong password")
	ErrPhoneTaken               = New(http.StatusConflict, "phone_taken", "Phone number already registered")
	ErrEmailTaken               = New(http.StatusConflict, "email_taken", "Email already used by another account")
	ErrEmailAlreadyVerified     = New(http.StatusConflict, "email_already_verified", "Email already verified")
	ErrCodeExpired              = New(http.StatusGone, "code_expired", "Code expired")
	ErrInternal                 = New(http.StatusInternalServerError, "internal_error", "Internal error")
	ErrUnavailable              = New(http.StatusServiceUnavailable, "service_unavailable", "Service unavailable")
)
```

- [ ] **Step 4: Тест проходит**

Run: `go test ./internal/apperr/`
Expected: `ok`

- [ ] **Step 5: Контрольная точка** — выдать владельцу команды:

```bash
git add internal/apperr
git commit -m "feat(apperr): add client-facing error codes"
```

---

### Task 4: Пакет `contact`

Номер без `+` считается российским; принимаются только номера, способные принять SMS (`MOBILE`, `FIXED_LINE_OR_MOBILE`). Email приводится к нижнему регистру и должен быть «голым» адресом с точкой в домене.

**Files:**
- Create: `internal/contact/contact.go`
- Test: `internal/contact/contact_test.go`

- [ ] **Step 1: Написать тест**

<!-- file: internal/contact/contact_test.go -->
```go
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
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/contact/`
Expected: FAIL — ошибки компиляции, например `undefined: NormalizePhone`, `undefined: NormalizeEmail`.

- [ ] **Step 3: Зависимость**

```bash
go get github.com/nyaruka/phonenumbers@v1.8.1
```

- [ ] **Step 4: Реализация**

<!-- file: internal/contact/contact.go -->
```go
// Package contact normalizes phone numbers and email addresses.
package contact

import (
	"errors"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/nyaruka/phonenumbers"
)

var (
	// ErrInvalidPhone means the input is not a valid number that can receive SMS.
	ErrInvalidPhone = errors.New("invalid phone number")
	// ErrInvalidEmail means the input is not a plain ASCII address with a host-name domain.
	ErrInvalidEmail = errors.New("invalid email")
)

// defaultRegion is used for numbers entered without a leading "+".
const defaultRegion = "RU"

const (
	maxPhoneInput = 32
	maxEmailLen   = 254
	maxLocalLen   = 64
	maxLabelLen   = 63
)

// NormalizePhone parses a user-entered number and returns it in E.164.
// Only ASCII digits, "+", spaces and the usual separators are accepted: the parser would
// otherwise turn letters into digits and drop extensions, sending the SMS to another number.
// Only numbers that can receive SMS (mobile or fixed-line-or-mobile) pass.
func NormalizePhone(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxPhoneInput || strings.IndexFunc(raw, notPhoneRune) >= 0 {
		return "", ErrInvalidPhone
	}
	num, err := phonenumbers.Parse(raw, defaultRegion)
	if err != nil || !phonenumbers.IsValidNumber(num) {
		return "", ErrInvalidPhone
	}
	switch phonenumbers.GetNumberType(num) {
	case phonenumbers.MOBILE, phonenumbers.FIXED_LINE_OR_MOBILE:
	default:
		return "", ErrInvalidPhone
	}
	return phonenumbers.Format(num, phonenumbers.E164), nil
}

func notPhoneRune(r rune) bool {
	return (r < '0' || r > '9') && !strings.ContainsRune("+ -().\u00a0", r)
}

// NormalizeEmail trims and lowercases an address and checks that it is a bare ASCII addr-spec
// with a local part of at most 64 characters and a host-name domain.
// Non-ASCII addresses are rejected on purpose: Go and Postgres citext lowercase them
// differently, and an IDN domain and its punycode form would be two keys for one mailbox.
func NormalizeEmail(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > maxEmailLen || !isASCII(s) {
		return "", ErrInvalidEmail
	}
	s = strings.ToLower(s)
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Name != "" || addr.Address != s {
		return "", ErrInvalidEmail
	}
	at := strings.LastIndexByte(s, '@')
	if at > maxLocalLen || !isHostname(s[at+1:]) {
		return "", ErrInvalidEmail
	}
	return s, nil
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// isHostname accepts lowercase host names: at least two labels of 1–63 letters, digits or
// hyphens, no label starting or ending with a hyphen, and a top-level label with a letter.
// IP addresses and literals such as [127.0.0.1] are rejected.
func isHostname(d string) bool {
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "" || len(l) > maxLabelLen || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := range len(l) {
			if c := l[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return strings.ContainsFunc(labels[len(labels)-1], func(r rune) bool { return r >= 'a' && r <= 'z' })
}
```

- [ ] **Step 5: Тест проходит**

```bash
go mod tidy
go test ./internal/contact/
```
Expected: `ok`. Если падает только кейс `+7 495 123-45-67`, значит версия phonenumbers отличается от `v1.8.1` — проверьте `go.mod`.

- [ ] **Step 6: Контрольная точка** — выдать владельцу команды:

```bash
git add go.mod go.sum internal/contact
git commit -m "feat(contact): normalize phone numbers and emails"
```

---

### Task 5: Пакет `password`

argon2id с параметрами OWASP (19 МиБ, 2 прохода), формат PHC, признак «пора перехэшировать» (только при совпавшем пароле), семафор на число одновременных хэширований (слот освобождается через `defer` даже при панике) и фиктивная проверка для неизвестных номеров. Параметры проверяются на границы и при создании `Hasher`, и при разборе хранимого хэша: испорченная строка в БД не должна ронять процесс. Пароль перед хэшированием приводится к NFKC (NIST SP 800-63B). `FieldError` переводит ошибку валидации в ошибку поля API.

**Files:**
- Create: `internal/password/password.go`
- Test: `internal/password/password_test.go`

- [ ] **Step 1: Написать тест** (тест из того же пакета: занимает слот семафора напрямую)

<!-- file: internal/password/password_test.go -->
```go
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
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/password/`
Expected: FAIL — ошибки компиляции, например `undefined: Params`, `undefined: NewHasher` и т. д.

- [ ] **Step 3: Зависимость**

```bash
go get golang.org/x/crypto@v0.58.0
go get golang.org/x/text@v0.43.0
```

- [ ] **Step 4: Реализация**

<!-- file: internal/password/password.go -->
```go
// Package password validates and hashes passwords with argon2id.
package password

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/apperr"
)

// Password length limits, in characters.
const (
	MinLength = 8
	MaxLength = 128
)

var (
	ErrTooShort = errors.New("password too short")
	ErrTooLong  = errors.New("password too long")
	// ErrBusy means the context ended while waiting for a free hashing slot.
	ErrBusy = errors.New("password hasher busy")
	// ErrMalformedHash means a stored hash is not a canonical argon2id PHC string with sane parameters.
	ErrMalformedHash = errors.New("malformed password hash")
)

// Validate checks length only (NIST SP 800-63B: no composition rules). Characters are counted
// after NFKC normalization, in the form that gets hashed.
func Validate(pw string) error {
	n := utf8.RuneCountInString(norm.NFKC.String(pw))
	switch {
	case n < MinLength:
		return ErrTooShort
	case n > MaxLength:
		return ErrTooLong
	}
	return nil
}

// FieldError converts a Validate failure into an API field error; nil when pw is valid.
func FieldError(field, pw string) *apperr.FieldError {
	switch err := Validate(pw); {
	case err == nil:
		return nil
	case errors.Is(err, ErrTooLong):
		return &apperr.FieldError{Field: field, Code: apperr.FieldTooLong}
	default:
		return &apperr.FieldError{Field: field, Code: apperr.FieldTooShort}
	}
}

// Params are argon2id parameters; Memory is in KiB.
type Params struct {
	Memory  uint32
	Time    uint32
	Threads uint8
	SaltLen uint32
	KeyLen  uint32
}

// DefaultParams follow the OWASP argon2id recommendation (19 MiB, 2 passes, 1 lane).
var DefaultParams = Params{Memory: 19456, Time: 2, Threads: 1, SaltLen: 16, KeyLen: 32}

// validate rejects parameters that would panic in argon2, exhaust memory or CPU, or weaken
// the hash. Stored hashes go through it too: a corrupted row must not take the process down.
func (p Params) validate() error {
	switch {
	case p.Time < 1 || p.Time > 10,
		p.Threads < 1 || p.Threads > 16,
		p.Memory < 8*uint32(p.Threads) || p.Memory > 256*1024,
		p.SaltLen < 8 || p.SaltLen > 64,
		p.KeyLen < 16 || p.KeyLen > 64:
		return fmt.Errorf("password: argon2id parameters out of range: %+v", p)
	}
	return nil
}

// Hasher hashes and verifies passwords. At most concurrency hashes run at once, so peak
// memory is about concurrency × Params.Memory. Passwords are NFKC-normalized first
// (NIST SP 800-63B), so one visible password typed on different keyboards gives one hash.
type Hasher struct {
	params Params
	slots  chan struct{}
	dummy  string
}

func NewHasher(p Params, concurrency int) (*Hasher, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	if concurrency < 1 {
		return nil, fmt.Errorf("password: concurrency must be >= 1, got %d", concurrency)
	}
	h := &Hasher{params: p, slots: make(chan struct{}, concurrency)}
	dummy, err := h.Hash(context.Background(), rand.Text())
	if err != nil {
		return nil, err
	}
	h.dummy = dummy
	return h, nil
}

func (h *Hasher) acquire(ctx context.Context) error {
	select {
	case h.slots <- struct{}{}:
		return nil
	default:
	}
	select {
	case h.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ErrBusy
	}
}

func (h *Hasher) release() { <-h.slots }

// derive runs argon2id while holding a slot. The deferred release frees it even if argon2 panics.
func (h *Hasher) derive(ctx context.Context, pw string, salt []byte, p Params) ([]byte, error) {
	if err := h.acquire(ctx); err != nil {
		return nil, err
	}
	defer h.release()
	return argon2.IDKey([]byte(norm.NFKC.String(pw)), salt, p.Time, p.Memory, p.Threads, p.KeyLen), nil
}

// Hash returns the password in PHC format: $argon2id$v=19$m=..,t=..,p=..$salt$key.
func (h *Hasher) Hash(ctx context.Context, pw string) (string, error) {
	salt := make([]byte, h.params.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := h.derive(ctx, pw, salt, h.params)
	if err != nil {
		return "", err
	}
	return encode(h.params, salt, key), nil
}

// Verify reports whether pw matches encoded. needsRehash is true only when the password
// matched and encoded was made with other parameters.
func (h *Hasher) Verify(ctx context.Context, pw, encoded string) (ok, needsRehash bool, err error) {
	p, salt, key, err := decode(encoded)
	if err != nil {
		return false, false, err
	}
	other, err := h.derive(ctx, pw, salt, p)
	if err != nil {
		return false, false, err
	}
	ok = subtle.ConstantTimeCompare(key, other) == 1
	return ok, ok && p != h.params, nil
}

// VerifyDummy spends the same time as Verify so responses don't reveal whether an account exists.
func (h *Hasher) VerifyDummy(ctx context.Context, pw string) error {
	_, _, err := h.Verify(ctx, pw, h.dummy)
	return err
}

func encode(p Params, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

// decode accepts only the canonical form produced by encode, with parameters within bounds.
func decode(encoded string) (Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return Params{}, nil, nil, ErrMalformedHash
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil ||
		parts[3] != fmt.Sprintf("m=%d,t=%d,p=%d", p.Memory, p.Time, p.Threads) {
		return Params{}, nil, nil, ErrMalformedHash
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil {
		return Params{}, nil, nil, ErrMalformedHash
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return Params{}, nil, nil, ErrMalformedHash
	}
	p.SaltLen = uint32(len(salt))
	p.KeyLen = uint32(len(key))
	if p.validate() != nil {
		return Params{}, nil, nil, ErrMalformedHash
	}
	return p, salt, key, nil
}
```

- [ ] **Step 5: Тест проходит**

```bash
go mod tidy
go test ./internal/password/
```
Expected: `ok`

- [ ] **Step 6: Контрольная точка** — выдать владельцу команды:

```bash
git add go.mod go.sum internal/password
git commit -m "feat(password): argon2id hashing with bounded concurrency"
```

---

### Task 6: Коды подтверждения (`otp/code.go`)

Чистые функции: генерация равномерного 6-значного кода и HMAC, привязанный к id записи. Хранение и проверка в БД — в части 2 (`otp/service.go`).

**Files:**
- Create: `internal/otp/code.go`
- Test: `internal/otp/code_test.go`

- [ ] **Step 1: Написать тест**

<!-- file: internal/otp/code_test.go -->
```go
package otp

import (
	"encoding/hex"
	"regexp"
	"testing"

	"github.com/google/uuid"
)

func TestGenerateFormat(t *testing.T) {
	six := regexp.MustCompile(`^\d{6}$`)
	seen := map[string]bool{}
	for range 200 {
		c, err := Generate()
		if err != nil {
			t.Fatal(err)
		}
		if !six.MatchString(c) {
			t.Fatalf("code %q is not 6 digits", c)
		}
		seen[c] = true
	}
	if len(seen) < 190 {
		t.Fatalf("only %d distinct codes out of 200", len(seen))
	}
}

func TestHashBindsKeyIDAndCode(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	id := uuid.New()
	h := Hash(key, id, "123456")
	if !Matches(key, id, "123456", h) {
		t.Fatal("same inputs must match")
	}
	if Matches(key, id, "123457", h) {
		t.Fatal("different code matched")
	}
	if Matches(key, uuid.New(), "123456", h) {
		t.Fatal("different id matched")
	}
	if Matches([]byte("another-key-another-key-another-k"), id, "123456", h) {
		t.Fatal("different key matched")
	}
}

var (
	testKey = []byte("0123456789abcdef0123456789abcdef")
	testID  = uuid.MustParse("0190a0b0-c0d0-7e0f-8a1b-2c3d4e5f6071")
)

func TestGenerateSpreadsOverWholeSpace(t *testing.T) {
	const n = 20000
	var seen [6][10]bool
	var first [10]int
	for range n {
		c, err := Generate()
		if err != nil {
			t.Fatal(err)
		}
		for i := range 6 {
			seen[i][c[i]-'0'] = true
		}
		first[c[0]-'0']++
	}
	for pos := range seen {
		for d, ok := range seen[pos] {
			if !ok {
				t.Errorf("digit %d never appeared at position %d in %d codes", d, pos, n)
			}
		}
	}
	// expected 2000 +/- 42 (1 sd); +/-350 is >8 sd, so a false failure is practically impossible.
	for d, got := range first {
		if got < 1650 || got > 2350 {
			t.Errorf("leading digit %d appeared %d times, expected about 2000", d, got)
		}
	}
}

func TestHashKnownAnswer(t *testing.T) {
	got := hex.EncodeToString(Hash(testKey, testID, "123456"))
	const want = "0ad929f2a82f319a1e82df7d043c05a0dd74aaca42fda847b39d366c57355cb9"
	if got != want {
		t.Fatalf("Hash = %s, want %s", got, want)
	}
}

func TestMatchesRejectsMalformedHash(t *testing.T) {
	h := Hash(testKey, testID, "123456")
	bad := map[string][]byte{
		"nil":       nil,
		"empty":     {},
		"truncated": h[:len(h)-1],
		"extended":  append(append([]byte{}, h...), 0),
		"zeros":     make([]byte, len(h)),
	}
	for name, b := range bad {
		if Matches(testKey, testID, "123456", b) {
			t.Errorf("%s hash matched", name)
		}
	}
}

func TestMatchesRejectsMalformedCode(t *testing.T) {
	h := Hash(testKey, testID, "123456")
	for _, c := range []string{"", "12345", "1234567", "12345a", " 123456", "123456\n", "123456\x00", "１２３４５６"} {
		if Matches(testKey, testID, c, h) {
			t.Errorf("code %q matched", c)
		}
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/otp/`
Expected: FAIL — сборка не проходит: сначала `no required module provides package github.com/google/uuid` (модуль добавится на следующем шаге), без него — `undefined: Generate`, `undefined: Hash`, `undefined: Matches`.

- [ ] **Step 3: Зависимость**

```bash
go get github.com/google/uuid@v1.6.0
```

- [ ] **Step 4: Реализация**

<!-- file: internal/otp/code.go -->
```go
// Package otp implements one-time verification codes sent by SMS and email.
package otp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
)

// Purpose says what a code confirms; the values match the CHECK constraint in the schema.
type Purpose string

const (
	PurposePhoneRegister Purpose = "phone_register"
	PurposeEmailVerify   Purpose = "email_verify"
	PurposePasswordReset Purpose = "password_reset"
)

const (
	// TTL is how long a code stays valid.
	TTL = 10 * time.Minute
	// MaxAttempts is the number of wrong guesses allowed per code.
	MaxAttempts = 5
)

var codeSpace = big.NewInt(1_000_000)

// Generate returns a uniformly random 6-digit code. rand.Int redraws out-of-range values,
// so there is no modulo bias; don't replace it with % over random bytes.
func Generate() (string, error) {
	n, err := rand.Int(rand.Reader, codeSpace)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// Hash binds the code to its record id: a stolen hash can't be matched against another record,
// and without the key the 10^6 code space can't be brute-forced offline. The id is fixed-width,
// so the encoding is unambiguous (":" is kept as written in the spec). Pass the code as entered.
func Hash(key []byte, id uuid.UUID, code string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(id[:])
	m.Write([]byte(":"))
	m.Write([]byte(code))
	return m.Sum(nil)
}

// Matches compares in constant time.
func Matches(key []byte, id uuid.UUID, code string, hash []byte) bool {
	return hmac.Equal(Hash(key, id, code), hash)
}
```

- [ ] **Step 5: Тест проходит**

```bash
go mod tidy
go test ./internal/otp/
```
Expected: `ok`

- [ ] **Step 6: Контрольная точка** — выдать владельцу команды:

```bash
git add go.mod go.sum internal/otp
git commit -m "feat(otp): generate and HMAC verification codes"
```

---

### Task 7: Ключи и JWT (`token`)

Access-токен (`aud=api`, `sub`=user, `sid`=session) и registration-токен (`aud=registration`, `sub`=телефон) подписываются одним Ed25519-ключом; разные audience не дают принять один токен вместо другого. Разрешён только алгоритм EdDSA.

**Files:**
- Create: `internal/token/keys.go`, `internal/token/jwt.go`
- Test: `internal/token/keys_test.go`, `internal/token/jwt_test.go`

- [ ] **Step 1: Написать тесты**

`internal/token/keys_test.go`:

<!-- file: internal/token/keys_test.go -->
```go
package token

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateKeyPEMRoundTrip(t *testing.T) {
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pemBytes, err := EncodePrivateKeyPEM(k)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "jwt.pem")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadPrivateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Equal(k) {
		t.Fatal("loaded key differs from generated key")
	}
}

func TestParsePrivateKeyPEMRejectsOtherKeys(t *testing.T) {
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ed, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ecPKCS8, _ := x509.MarshalPKCS8PrivateKey(ec)
	rsaPKCS8, _ := x509.MarshalPKCS8PrivateKey(rs)
	ecSEC1, _ := x509.MarshalECPrivateKey(ec)
	edPublic, _ := x509.MarshalPKIXPublicKey(ed.Public())
	cases := map[string][]byte{
		"garbage":            []byte("not a pem"),
		"empty":              nil,
		"ec pkcs8":           pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecPKCS8}),
		"rsa pkcs8":          pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rsaPKCS8}),
		"ec sec1":            pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: ecSEC1}),
		"ed25519 public key": pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: edPublic}),
	}
	for name, in := range cases {
		if _, err := ParsePrivateKeyPEM(in); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestLoadPrivateKeyErrorNamesTheFile(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "broken.pem")
	if err := os.WriteFile(bad, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPrivateKey(bad); err == nil || !strings.Contains(err.Error(), "broken.pem") {
		t.Fatalf("err = %v, want it to mention the file", err)
	}
}
```

`internal/token/jwt_test.go`:

<!-- file: internal/token/jwt_test.go -->
```go
package token

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/clock"
)

func newTestSigner(t *testing.T, c clock.Clock) *Signer {
	t.Helper()
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return NewSigner(SignerConfig{Key: k, Issuer: "test", AccessTTL: 15 * time.Minute, RegistrationTTL: 15 * time.Minute, Clock: c})
}

// signWith signs arbitrary claims with the signer's real key, to build forged-but-signed tokens.
func signWith(t *testing.T, s *Signer, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tok.Header["kid"] = s.kid
	raw, err := tok.SignedString(s.cfg.Key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// flipSigChar changes the first character of the signature, which always changes its bytes
// (the last characters may only carry padding bits).
func flipSigChar(raw string) string {
	i := strings.LastIndex(raw, ".") + 1
	b := []byte(raw)
	if b[i] == 'A' {
		b[i] = 'B'
	} else {
		b[i] = 'A'
	}
	return string(b)
}

func TestAccessRoundTrip(t *testing.T) {
	s := newTestSigner(t, clock.NewFake(time.Now()))
	uid, sid := uuid.New(), uuid.New()
	raw, err := s.IssueAccess(uid, sid)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ParseAccess(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != uid || got.SessionID != sid {
		t.Fatalf("got %+v", got)
	}
}

// The lifetime is TTL plus a 5 s leeway; the literals below pin both.
func TestAccessLifetime(t *testing.T) {
	c := clock.NewFake(time.Now())
	s := newTestSigner(t, c)
	raw, err := s.IssueAccess(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	c.Advance(15*time.Minute - time.Second)
	if _, err := s.ParseAccess(raw); err != nil {
		t.Fatalf("rejected 1 s before expiry: %v", err)
	}
	c.Advance(5 * time.Second) // TTL + 4 s: inside the leeway
	if _, err := s.ParseAccess(raw); err != nil {
		t.Fatalf("rejected inside the leeway: %v", err)
	}
	c.Advance(2 * time.Second) // TTL + 6 s
	_, err = s.ParseAccess(raw)
	if !errors.Is(err, ErrInvalid) || !errors.Is(err, jwt.ErrTokenExpired) {
		t.Fatalf("err = %v, want ErrInvalid wrapping jwt.ErrTokenExpired", err)
	}
}

func TestAccessRejectsOtherKey(t *testing.T) {
	c := clock.NewFake(time.Now())
	raw, err := newTestSigner(t, c).IssueAccess(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newTestSigner(t, c).ParseAccess(raw); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// Every case is signed with the real key (or forges the algorithm), so only the claim and
// header checks stand between it and acceptance.
func TestParseAccessRejectsForgedTokens(t *testing.T) {
	c := clock.NewFake(time.Now())
	s := newTestSigner(t, c)
	now := c.Now()
	uid, sid := uuid.NewString(), uuid.NewString()
	base := func() jwt.MapClaims {
		return jwt.MapClaims{"iss": "test", "aud": []string{"api"}, "sub": uid, "sid": sid, "exp": now.Add(time.Minute).Unix()}
	}
	with := func(f func(m jwt.MapClaims)) string {
		m := base()
		f(m)
		return signWith(t, s, m)
	}

	if _, err := s.ParseAccess(with(func(jwt.MapClaims) {})); err != nil {
		t.Fatalf("baseline token rejected: %v", err)
	}
	if _, err := s.ParseAccess(with(func(m jwt.MapClaims) { m["exp"] = now.Add(-3 * time.Second).Unix() })); err != nil {
		t.Fatalf("token expired 3 s ago (inside leeway) rejected: %v", err)
	}

	payload, err := json.Marshal(base())
	if err != nil {
		t.Fatal(err)
	}
	none := b64([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + b64(payload) + "."
	hs := b64([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + b64(payload)
	mac := hmac.New(sha256.New, s.pub) // the classic public-key-as-HMAC-secret attack
	mac.Write([]byte(hs))
	good := with(func(jwt.MapClaims) {})

	cases := map[string]string{
		"registration audience":  with(func(m jwt.MapClaims) { m["aud"] = []string{"registration"} }),
		"no audience":            with(func(m jwt.MapClaims) { delete(m, "aud") }),
		"both audiences":         with(func(m jwt.MapClaims) { m["aud"] = []string{"api", "registration"} }),
		"wrong issuer":           with(func(m jwt.MapClaims) { m["iss"] = "other" }),
		"no issuer":              with(func(m jwt.MapClaims) { delete(m, "iss") }),
		"no expiry":              with(func(m jwt.MapClaims) { delete(m, "exp") }),
		"expired":                with(func(m jwt.MapClaims) { m["exp"] = now.Add(-time.Minute).Unix() }),
		"not yet valid":          with(func(m jwt.MapClaims) { m["nbf"] = now.Add(time.Hour).Unix() }),
		"no session":             with(func(m jwt.MapClaims) { delete(m, "sid") }),
		"garbage session":        with(func(m jwt.MapClaims) { m["sid"] = "zzz" }),
		"numeric session":        with(func(m jwt.MapClaims) { m["sid"] = 5 }),
		"no subject":             with(func(m jwt.MapClaims) { delete(m, "sub") }),
		"phone subject":          with(func(m jwt.MapClaims) { m["sub"] = "+79991234567" }),
		"nil uuid subject":       with(func(m jwt.MapClaims) { m["sub"] = uuid.Nil.String() }),
		"urn subject":            with(func(m jwt.MapClaims) { m["sub"] = "urn:uuid:" + uid }),
		"braced subject":         with(func(m jwt.MapClaims) { m["sub"] = "{" + uid + "}" }),
		"oversized":              with(func(m jwt.MapClaims) { m["pad"] = strings.Repeat("a", 5000) }),
		"alg none, valid claims": none,
		"HS256 with public key":  hs + "." + b64(mac.Sum(nil)),
		"tampered signature":     flipSigChar(good),
		"empty":                  "",
		"not a jwt":              "not.a.jwt",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := s.ParseAccess(raw); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestHeaderHasAlgAndKid(t *testing.T) {
	s := newTestSigner(t, clock.NewFake(time.Now()))
	raw, err := s.IssueAccess(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := jwt.NewParser().ParseUnverified(raw, jwt.MapClaims{})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(s.pub)
	if tok.Header["alg"] != "EdDSA" || tok.Header["kid"] != hex.EncodeToString(sum[:8]) {
		t.Fatalf("header %v", tok.Header)
	}
}

func TestAudiencesAreNotInterchangeable(t *testing.T) {
	s := newTestSigner(t, clock.NewFake(time.Now()))
	reg, err := s.IssueRegistration("+79991234567")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ParseAccess(reg); !errors.Is(err, ErrInvalid) {
		t.Fatalf("registration token accepted as access: %v", err)
	}
	acc, err := s.IssueAccess(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ParseRegistration(acc); !errors.Is(err, ErrInvalid) {
		t.Fatalf("access token accepted as registration: %v", err)
	}
}

func TestRegistrationRoundTrip(t *testing.T) {
	s := newTestSigner(t, clock.NewFake(time.Now()))
	raw, err := s.IssueRegistration("+79991234567")
	if err != nil {
		t.Fatal(err)
	}
	phone, err := s.ParseRegistration(raw)
	if err != nil || phone != "+79991234567" {
		t.Fatalf("got %q, %v", phone, err)
	}
	if _, err := s.IssueRegistration(""); err == nil {
		t.Fatal("registration token issued for an empty phone")
	}
}

func TestParseRegistrationRejectsForgedTokens(t *testing.T) {
	c := clock.NewFake(time.Now())
	s := newTestSigner(t, c)
	base := func() jwt.MapClaims {
		return jwt.MapClaims{"iss": "test", "aud": []string{"registration"}, "sub": "+79991234567", "exp": c.Now().Add(time.Minute).Unix()}
	}
	for name, f := range map[string]func(m jwt.MapClaims){
		"api audience":  func(m jwt.MapClaims) { m["aud"] = []string{"api"} },
		"empty subject": func(m jwt.MapClaims) { m["sub"] = "" },
		"no subject":    func(m jwt.MapClaims) { delete(m, "sub") },
		"no expiry":     func(m jwt.MapClaims) { delete(m, "exp") },
	} {
		t.Run(name, func(t *testing.T) {
			m := base()
			f(m)
			if _, err := s.ParseRegistration(signWith(t, s, m)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestNewSignerRejectsBadConfig(t *testing.T) {
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ok := SignerConfig{Key: k, Issuer: "test", AccessTTL: time.Minute, RegistrationTTL: time.Minute}
	bad := map[string]func(c *SignerConfig){
		"no key":       func(c *SignerConfig) { c.Key = nil },
		"short key":    func(c *SignerConfig) { c.Key = k[:10] },
		"seed as key":  func(c *SignerConfig) { c.Key = k[:32] },
		"empty issuer": func(c *SignerConfig) { c.Issuer = "" }, // would disable the issuer check
		"zero ttl":     func(c *SignerConfig) { c.AccessTTL = 0 },
		"zero reg ttl": func(c *SignerConfig) { c.RegistrationTTL = 0 },
	}
	for name, f := range bad {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("NewSigner accepted the config")
				}
			}()
			c := ok
			f(&c)
			NewSigner(c)
		})
	}
	s := NewSigner(ok) // nil Clock falls back to the wall clock
	raw, err := s.IssueAccess(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ParseAccess(raw); err != nil {
		t.Fatalf("signer with default clock: %v", err)
	}
}
```

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `go test ./internal/token/`
Expected: FAIL — ошибки компиляции, например `undefined: GenerateKey`, `undefined: NewSigner` и т. д.

- [ ] **Step 3: Зависимость**

```bash
go get github.com/golang-jwt/jwt/v5@v5.3.1
```

- [ ] **Step 4: Реализация**

`internal/token/keys.go`:

<!-- file: internal/token/keys.go -->
```go
// Package token issues and verifies access, registration and refresh tokens.
package token

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"

	"github.com/golang-jwt/jwt/v5"
)

// GenerateKey creates a new Ed25519 key for signing JWTs.
func GenerateKey() (ed25519.PrivateKey, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	return priv, err
}

// EncodePrivateKeyPEM encodes the key as PKCS#8 PEM ("PRIVATE KEY").
func EncodePrivateKeyPEM(k ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// ParsePrivateKeyPEM accepts only a PKCS#8 Ed25519 private key; RSA, EC and public keys are rejected.
func ParsePrivateKeyPEM(b []byte) (ed25519.PrivateKey, error) {
	k, err := jwt.ParseEdPrivateKeyFromPEM(b)
	if err != nil {
		return nil, err
	}
	ek, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("token: key is %T, want ed25519", k)
	}
	return ek, nil
}

// LoadPrivateKey reads the key written by cmd/keygen (JWT_PRIVATE_KEY_FILE).
func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("token: load JWT key: %w", err)
	}
	k, err := ParsePrivateKeyPEM(b)
	if err != nil {
		return nil, fmt.Errorf("token: load JWT key %q: %w", path, err)
	}
	return k, nil
}
```

`internal/token/jwt.go`:

<!-- file: internal/token/jwt.go -->
```go
package token

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/clock"
)

const (
	audAPI          = "api"
	audRegistration = "registration"
	leeway          = 5 * time.Second
	// maxTokenLen rejects oversized input before any decoding; real tokens are about 300 bytes.
	maxTokenLen = 4096
)

// ErrInvalid is returned for every token that fails verification. The underlying reason is
// wrapped for server-side logs; clients only ever see 401.
var ErrInvalid = errors.New("invalid token")

// SignerConfig configures a Signer. Clock may be nil (the wall clock is used); the rest is required.
type SignerConfig struct {
	Key             ed25519.PrivateKey
	Issuer          string
	AccessTTL       time.Duration
	RegistrationTTL time.Duration
	Clock           clock.Clock
}

// Signer issues and verifies EdDSA JWTs. Access and registration tokens differ by audience,
// so one can never be accepted as the other. Tokens stay valid for up to 5 s past their TTL
// (clock-skew leeway).
type Signer struct {
	cfg SignerConfig
	pub ed25519.PublicKey
	kid string
}

// NewSigner panics on an invalid configuration. It runs once at startup, and an empty Issuer
// would otherwise silently switch the issuer check off.
func NewSigner(cfg SignerConfig) *Signer {
	switch {
	case len(cfg.Key) != ed25519.PrivateKeySize:
		panic("token: SignerConfig.Key must be an Ed25519 private key")
	case cfg.Issuer == "":
		panic("token: SignerConfig.Issuer must not be empty")
	case cfg.AccessTTL <= 0 || cfg.RegistrationTTL <= 0:
		panic("token: SignerConfig TTLs must be positive")
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	pub := cfg.Key.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(pub)
	return &Signer{cfg: cfg, pub: pub, kid: hex.EncodeToString(sum[:8])}
}

// Access is the verified content of an access token.
type Access struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
}

type accessClaims struct {
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

func (s *Signer) AccessTTL() time.Duration       { return s.cfg.AccessTTL }
func (s *Signer) RegistrationTTL() time.Duration { return s.cfg.RegistrationTTL }

func (s *Signer) IssueAccess(userID, sessionID uuid.UUID) (string, error) {
	return s.sign(accessClaims{
		SessionID:        sessionID.String(),
		RegisteredClaims: s.registered(userID.String(), audAPI, s.cfg.AccessTTL),
	})
}

func (s *Signer) ParseAccess(raw string) (Access, error) {
	var c accessClaims
	if err := s.parse(raw, audAPI, &c); err != nil {
		return Access{}, err
	}
	uid, ok := parseID(c.Subject)
	if !ok {
		return Access{}, ErrInvalid
	}
	sid, ok := parseID(c.SessionID)
	if !ok {
		return Access{}, ErrInvalid
	}
	return Access{UserID: uid, SessionID: sid}, nil
}

// parseID accepts only the canonical form this package writes, never the nil UUID.
func parseID(s string) (uuid.UUID, bool) {
	id, err := uuid.Parse(s)
	return id, err == nil && id != uuid.Nil && id.String() == s
}

// IssueRegistration proves that the holder confirmed ownership of phone (E.164).
func (s *Signer) IssueRegistration(phone string) (string, error) {
	if phone == "" {
		return "", errors.New("token: empty phone")
	}
	c := s.registered(phone, audRegistration, s.cfg.RegistrationTTL)
	return s.sign(&c)
}

// ParseRegistration returns the phone the token was issued for. Only this server signs these
// tokens, so the phone is trusted as already normalized.
func (s *Signer) ParseRegistration(raw string) (phone string, err error) {
	var c jwt.RegisteredClaims
	if err := s.parse(raw, audRegistration, &c); err != nil {
		return "", err
	}
	if c.Subject == "" {
		return "", ErrInvalid
	}
	return c.Subject, nil
}

func (s *Signer) registered(sub, aud string, ttl time.Duration) jwt.RegisteredClaims {
	now := s.cfg.Clock.Now()
	return jwt.RegisteredClaims{
		Issuer:    s.cfg.Issuer,
		Subject:   sub,
		Audience:  jwt.ClaimStrings{aud},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	}
}

func (s *Signer) sign(claims jwt.Claims) (string, error) {
	t := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	t.Header["kid"] = s.kid
	return t.SignedString(s.cfg.Key)
}

func (s *Signer) parse(raw, aud string, claims jwt.Claims) error {
	if len(raw) > maxTokenLen {
		return ErrInvalid
	}
	p := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(s.cfg.Issuer),
		jwt.WithAudience(aud),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(leeway),
		jwt.WithTimeFunc(s.cfg.Clock.Now),
	)
	if _, err := p.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) { return s.pub, nil }); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	// Exactly one audience: a token valid for both purposes must not exist.
	if got, err := claims.GetAudience(); err != nil || len(got) != 1 {
		return ErrInvalid
	}
	return nil
}
```

- [ ] **Step 5: Тесты проходят**

```bash
go mod tidy
go test ./internal/token/
```
Expected: `ok`

- [ ] **Step 6: Контрольная точка** — выдать владельцу команды:

```bash
git add go.mod go.sum internal/token
git commit -m "feat(token): Ed25519 keys and access/registration JWTs"
```

---

### Task 8: Refresh-токены (`token/refresh.go`)

Токен = base64url(id сессии ‖ поколение ‖ HMAC). Сервер не хранит токены: он вычисляет их заново, поэтому повтор в окне 30 секунд возвращает тот же токен. `Decide` реализует правила раздела 5.3 спецификации: окно повтора ограничено с обеих сторон (от −5 с до +30 с от ротации), поколения меньше 1 отклоняются. `Decode` принимает только каноническое написание токена (точная длина, строгое base64), а `NewRefreshCodec` требует ключ не короче 32 байт.

**Files:**
- Create: `internal/token/refresh.go`
- Test: `internal/token/refresh_test.go`

- [ ] **Step 1: Написать тест**

<!-- file: internal/token/refresh_test.go -->
```go
package token

import (
	"encoding/base64"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

var testRefreshKey = []byte("0123456789abcdef0123456789abcdef")

func TestRefreshRoundTrip(t *testing.T) {
	c := NewRefreshCodec(testRefreshKey)
	sid := uuid.New()
	raw := c.Encode(sid, 7)
	gotSID, gotGen, err := c.Decode(raw)
	if err != nil || gotSID != sid || gotGen != 7 {
		t.Fatalf("Decode = %v, %d, %v", gotSID, gotGen, err)
	}
	if raw != c.Encode(sid, 7) {
		t.Fatal("encoding must be deterministic")
	}
}

// Vectors computed independently from the spec formula: base64url(sid ‖ gen ‖ HMAC-SHA256(key,
// "refresh:v1" ‖ sid ‖ gen)). Tokens live on devices for 60 days, so the format must not drift.
func TestRefreshKnownAnswer(t *testing.T) {
	c := NewRefreshCodec(testRefreshKey)
	sid := uuid.MustParse("0192f3a4-5b6c-7d8e-9fa0-b1c2d3e4f506")
	vectors := map[int64]string{
		1:             "AZLzpFtsfY6foLHC0-T1BgAAAAAAAAABn1gx9dtZMAYycA3oX9N253U9vh2nnH0DWP44xpSv878",
		7:             "AZLzpFtsfY6foLHC0-T1BgAAAAAAAAAHnGMOnf7xj0v4xMsXyMcPSwxjB3xDlRU-L6Y_GDWcqC8",
		math.MaxInt64: "AZLzpFtsfY6foLHC0-T1Bn__________M34YiEgGo-O81_kdQcybUaGVZlJOg3xVxfcRmcvzH44",
	}
	for gen, want := range vectors {
		if got := c.Encode(sid, gen); got != want {
			t.Errorf("Encode(gen %d) = %s, want %s", gen, got, want)
		}
	}
}

func TestRefreshRejectsTampering(t *testing.T) {
	c := NewRefreshCodec(testRefreshKey)
	b, err := base64.RawURLEncoding.DecodeString(c.Encode(uuid.New(), 5))
	if err != nil {
		t.Fatal(err)
	}
	reencode := func(f func(b []byte)) string {
		cp := append([]byte(nil), b...)
		f(cp)
		return base64.RawURLEncoding.EncodeToString(cp)
	}
	cases := map[string]string{
		"session first byte":  reencode(func(b []byte) { b[0] ^= 1 }),
		"session last byte":   reencode(func(b []byte) { b[15] ^= 1 }),
		"generation high":     reencode(func(b []byte) { b[16] ^= 1 }),
		"generation 5 to 6":   reencode(func(b []byte) { b[23] = 6 }), // forging the next generation
		"mac first byte":      reencode(func(b []byte) { b[24] ^= 1 }),
		"mac last byte":       reencode(func(b []byte) { b[len(b)-1] ^= 1 }),
		"empty":               "",
		"not base64":          strings.Repeat("!", encodedRefreshLen),
		"too short":           "AAAA",
		"other key":           NewRefreshCodec([]byte("ffffffffffffffffffffffffffffffff")).Encode(uuid.New(), 1),
		"generation 0":        c.Encode(uuid.New(), 0),
		"negative generation": c.Encode(uuid.New(), -1),
	}
	for name, raw := range cases {
		if _, _, err := c.Decode(raw); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

// One token must have exactly one accepted spelling.
func TestRefreshRejectsNonCanonicalEncoding(t *testing.T) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	c := NewRefreshCodec(testRefreshKey)
	raw := c.Encode(uuid.New(), 1)
	// 56 bytes leave 2 unused bits in the last character; setting one must not be accepted.
	last := strings.IndexByte(alphabet, raw[len(raw)-1])
	lf, crlf := string([]byte{10}), string([]byte{13, 10})
	variants := map[string]string{
		"padding bits set":  raw[:len(raw)-1] + string(alphabet[last|1]),
		"line feed inside":  raw[:30] + lf + raw[30:],
		"trailing crlf":     raw + crlf,
		"leading line feed": lf + raw,
	}
	for name, v := range variants {
		if _, _, err := c.Decode(v); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestNewRefreshCodecKey(t *testing.T) {
	for _, key := range [][]byte{nil, testRefreshKey[:31]} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%d-byte key accepted", len(key))
				}
			}()
			NewRefreshCodec(key)
		}()
	}
	key := append([]byte(nil), testRefreshKey...)
	c := NewRefreshCodec(key)
	raw := c.Encode(uuid.New(), 1)
	clear(key) // the caller reusing its buffer must not break the codec
	if _, _, err := c.Decode(raw); err != nil {
		t.Fatalf("codec depends on the caller's key buffer: %v", err)
	}
}

func TestDecide(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	const grace = 30 * time.Second
	cases := []struct {
		name               string
		presented, current int64
		rotatedAt          *time.Time
		want               Decision
	}{
		{"current", 5, 5, at(-time.Minute), DecisionRotate},
		{"first rotation", 1, 1, nil, DecisionRotate},
		{"previous within grace", 4, 5, at(-10 * time.Second), DecisionReplay},
		{"previous exactly at grace", 4, 5, at(-grace), DecisionReplay},
		{"previous just after grace", 4, 5, at(-grace - time.Nanosecond), DecisionReuse},
		{"previous long after grace", 4, 5, at(-time.Minute), DecisionReuse},
		{"previous, never rotated", 4, 5, nil, DecisionReuse},
		{"older", 3, 5, at(-10 * time.Second), DecisionReuse},
		{"rotation 2 s in the future (skew)", 4, 5, at(2 * time.Second), DecisionReplay},
		{"rotation exactly replaySkew ahead", 4, 5, at(replaySkew), DecisionReplay},
		{"rotation just beyond replaySkew ahead", 4, 5, at(replaySkew + time.Nanosecond), DecisionReuse},
		{"rotation a year in the future (clock jumped back)", 4, 5, at(365 * 24 * time.Hour), DecisionReuse},
		{"future generation", 6, 5, at(-10 * time.Second), DecisionInvalid},
		{"generation 0", 0, 1, at(-10 * time.Second), DecisionInvalid},
		{"negative generation", -1, 1, at(-10 * time.Second), DecisionInvalid},
		{"corrupt session generation", 1, 0, nil, DecisionInvalid},
		{"no overflow at the extremes", math.MaxInt64, math.MinInt64, at(-10 * time.Second), DecisionInvalid},
	}
	for _, c := range cases {
		if got := Decide(c.presented, c.current, c.rotatedAt, now, grace); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	// REFRESH_GRACE=0 turns replays off completely, skew window included.
	for _, d := range []time.Duration{0, 2 * time.Second, -2 * time.Second} {
		if got := Decide(4, 5, at(d), now, 0); got != DecisionReuse {
			t.Errorf("grace 0, rotated %v from now: got %v, want reuse", d, got)
		}
	}
}

func TestDecisionString(t *testing.T) {
	want := map[Decision]string{DecisionRotate: "rotate", DecisionReplay: "replay", DecisionReuse: "reuse", DecisionInvalid: "invalid", 0: "Decision(0)"}
	for d, w := range want {
		if d.String() != w {
			t.Errorf("Decision(%d).String() = %q, want %q", int(d), d.String(), w)
		}
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/token/`
Expected: FAIL — ошибки компиляции, например `undefined: NewRefreshCodec`, `undefined: Decide`, `undefined: DecisionRotate`.

- [ ] **Step 3: Реализация**

<!-- file: internal/token/refresh.go -->
```go
package token

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	refreshDomain = "refresh:v1"
	refreshRawLen = 16 + 8 + sha256.Size
	minRefreshKey = 32
	// replaySkew is how far before the recorded rotation time a replay is still accepted
	// (clocks of several instances may differ slightly).
	replaySkew = 5 * time.Second
)

// encodedRefreshLen is the only accepted token length. Checking it first also rejects line
// breaks, which Go's base64 decoder skips even in strict mode.
var encodedRefreshLen = base64.RawURLEncoding.EncodedLen(refreshRawLen)

// RefreshCodec derives refresh tokens from (session id, generation) with an HMAC.
// The server stores no token material: it recomputes the token, which makes replays idempotent.
type RefreshCodec struct {
	key []byte
}

// NewRefreshCodec panics if key is shorter than 32 bytes: with a weak key anyone could mint tokens.
// It runs once at startup with the key derived from APP_SECRET.
func NewRefreshCodec(key []byte) *RefreshCodec {
	if len(key) < minRefreshKey {
		panic("token: refresh key must be at least 32 bytes")
	}
	return &RefreshCodec{key: bytes.Clone(key)}
}

// Encode returns the token for the given generation (≥ 1) of a session.
func (c *RefreshCodec) Encode(sessionID uuid.UUID, generation int64) string {
	buf := make([]byte, 0, refreshRawLen)
	buf = append(buf, sessionID[:]...)
	buf = binary.BigEndian.AppendUint64(buf, uint64(generation))
	buf = append(buf, c.mac(sessionID, generation)...)
	return base64.RawURLEncoding.EncodeToString(buf)
}

// Decode verifies the MAC and returns the session and generation. Only the canonical spelling
// is accepted: exact length, strict base64 and a generation of at least 1.
func (c *RefreshCodec) Decode(raw string) (uuid.UUID, int64, error) {
	if len(raw) != encodedRefreshLen {
		return uuid.Nil, 0, ErrInvalid
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(b) != refreshRawLen {
		return uuid.Nil, 0, ErrInvalid
	}
	var sid uuid.UUID
	copy(sid[:], b[:16])
	gen := int64(binary.BigEndian.Uint64(b[16:24]))
	if gen < 1 || !hmac.Equal(b[24:], c.mac(sid, gen)) {
		return uuid.Nil, 0, ErrInvalid
	}
	return sid, gen, nil
}

func (c *RefreshCodec) mac(sessionID uuid.UUID, generation int64) []byte {
	m := hmac.New(sha256.New, c.key)
	m.Write([]byte(refreshDomain))
	m.Write(sessionID[:])
	m.Write(binary.BigEndian.AppendUint64(nil, uint64(generation)))
	return m.Sum(nil)
}

// Decision is what the auth service should do with a presented refresh token.
type Decision int

const (
	// DecisionRotate: the current token; advance the generation.
	DecisionRotate Decision = iota + 1
	// DecisionReplay: the previous token within the grace window; return the current token again.
	DecisionReplay
	// DecisionReuse: an already spent token; treat as theft and revoke the session.
	DecisionReuse
	// DecisionInvalid: a generation the server never issued.
	DecisionInvalid
)

func (d Decision) String() string {
	switch d {
	case DecisionRotate:
		return "rotate"
	case DecisionReplay:
		return "replay"
	case DecisionReuse:
		return "reuse"
	case DecisionInvalid:
		return "invalid"
	}
	return fmt.Sprintf("Decision(%d)", int(d))
}

// Decide applies the rotation rules from spec section 5.3. current is the session's generation
// (sessions start at 1); rotatedAt is nil until the first rotation. A replay is accepted from
// replaySkew before the rotation time to grace after it, so a clock that jumped back can't widen
// it; grace ≤ 0 turns replays off. A generation above current (e.g. after restoring an older
// database backup) is invalid.
func Decide(presented, current int64, rotatedAt *time.Time, now time.Time, grace time.Duration) Decision {
	switch {
	case presented < 1 || presented > current:
		return DecisionInvalid
	case presented == current:
		return DecisionRotate
	case presented == current-1 && rotatedAt != nil && inReplayWindow(now.Sub(*rotatedAt), grace):
		return DecisionReplay
	default:
		return DecisionReuse
	}
}

func inReplayWindow(sinceRotation, grace time.Duration) bool {
	return grace > 0 && sinceRotation >= -replaySkew && sinceRotation <= grace
}
```

- [ ] **Step 4: Тест проходит, в том числе многократно** (подмена токена случайна)

```bash
go test ./internal/token/
go test -count=200 -run 'TestRefresh' ./internal/token/
```
Expected: оба `ok`

- [ ] **Step 5: Контрольная точка** — выдать владельцу команды:

```bash
git add internal/token
git commit -m "feat(token): stateless rotating refresh tokens"
```

---

### Task 9: Пакет `ratelimit`

Собственная корзина токенов на ключ (IP или номер), без внешних зависимостей. `Allow` расходует событие (отказ ничего не расходует и возвращает точное время ожидания). `Acquire` резервирует событие заранее и даёт `refund`: так вход считает только неудачи, но параллельные попытки не проскакивают мимо лимита. `refund` идемпотентен и работает и спустя время (у `x/time/rate` возврат через `CancelAt` после любой паузы молча не срабатывает — поэтому своя реализация). Пополнение идёт только при движении часов вперёд, удаляются только полные корзины (они ничем не отличаются от новых), `Per` паникует на нулевых и отрицательных параметрах.

**Files:**
- Create: `internal/ratelimit/ratelimit.go`
- Test: `internal/ratelimit/ratelimit_test.go`

- [ ] **Step 1: Написать тест**

<!-- file: internal/ratelimit/ratelimit_test.go -->
```go
package ratelimit

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/clock"
)

func newClock() *clock.Fake { return clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) }

func TestAllowBurstThenExactWait(t *testing.T) {
	c := newClock()
	l := Per(2, time.Minute, c)
	for i := range 2 {
		if ok, _ := l.Allow("ip"); !ok {
			t.Fatalf("call %d denied", i+1)
		}
	}
	if ok, wait := l.Allow("ip"); ok || wait != 30*time.Second {
		t.Fatalf("third call: ok=%v wait=%v, want denied with exactly 30s", ok, wait)
	}
	if ok, _ := l.Allow("other"); !ok {
		t.Fatal("keys must be independent")
	}
	c.Advance(29 * time.Second)
	if ok, _ := l.Allow("ip"); ok {
		t.Fatal("allowed before the next token")
	}
	c.Advance(time.Second)
	if ok, _ := l.Allow("ip"); !ok {
		t.Fatal("denied after refill")
	}
}

func TestAcquireRefundCountsOnlyKeptEvents(t *testing.T) {
	c := newClock()
	l := Per(5, 15*time.Minute, c) // failed logins per phone
	for range 10 {
		refund, ok, _ := l.Acquire("phone")
		if !ok {
			t.Fatal("refunded events must not count")
		}
		refund() // a successful login
	}
	for range 5 {
		if _, ok, _ := l.Acquire("phone"); !ok {
			t.Fatal("failure denied before the limit")
		}
	}
	_, ok, wait := l.Acquire("phone")
	if ok || wait != 3*time.Minute {
		t.Fatalf("sixth failure: ok=%v wait=%v, want denied with exactly 3m", ok, wait)
	}
	c.Advance(3 * time.Minute)
	if _, ok, _ := l.Acquire("phone"); !ok {
		t.Fatal("denied after one event refilled")
	}
}

// Regression: refunds made after any time had passed (a real password check) were silently lost.
func TestRefundWorksAfterTimePassed(t *testing.T) {
	c := newClock()
	l := Per(5, 15*time.Minute, c)
	for range 20 {
		refund, ok, _ := l.Acquire("phone")
		if !ok {
			t.Fatal("refunded events must not count")
		}
		c.Advance(150 * time.Millisecond) // the password check
		refund()
		refund() // a second call must not give back more than was taken
	}
	for range 5 {
		if _, ok, _ := l.Acquire("phone"); !ok {
			t.Fatal("burst not intact after refunds")
		}
	}
	if _, ok, _ := l.Acquire("phone"); ok {
		t.Fatal("refunds raised the bucket above its burst")
	}
}

func TestDoubleRefundDoesNotOverCredit(t *testing.T) {
	c := newClock()
	l := Per(5, 15*time.Minute, c)
	var refund func()
	for range 5 {
		refund, _, _ = l.Acquire("phone")
	}
	c.Advance(time.Millisecond)
	refund()
	refund()
	if _, ok, _ := l.Acquire("phone"); !ok {
		t.Fatal("refunded event not available")
	}
	if _, ok, _ := l.Acquire("phone"); ok {
		t.Fatal("a double refund gave back two events")
	}
}

func TestAcquireIsAtomicUnderConcurrency(t *testing.T) {
	l := Per(5, 15*time.Minute, newClock())
	var granted atomic.Int32
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok, _ := l.Acquire("phone"); ok {
				granted.Add(1)
			}
		}()
	}
	wg.Wait()
	if granted.Load() != 5 {
		t.Fatalf("%d parallel attempts granted, want exactly 5", granted.Load())
	}
}

func TestAllowIsAtomicUnderConcurrency(t *testing.T) {
	l := Per(100, time.Minute, newClock())
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if ok, _ := l.Allow("k"); ok {
					allowed.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 100 {
		t.Fatalf("%d events allowed, want exactly 100", allowed.Load())
	}
}

func TestSweepDropsOnlyFullBuckets(t *testing.T) {
	c := newClock()
	l := Per(5, 15*time.Minute, c)
	for range 5 {
		l.Allow("drained")
	}
	l.Allow("idle")
	c.Advance(4 * time.Minute) // "idle" is full again, "drained" has about 1.3 events
	l.Allow("trigger")         // more than sweepEvery since the last sweep
	if _, ok := l.buckets["idle"]; ok {
		t.Fatal("full bucket not swept")
	}
	if _, ok := l.buckets["drained"]; !ok {
		t.Fatal("sweep reset a bucket that was still limiting")
	}
}

func TestClockGoingBackwardsDoesNotRecredit(t *testing.T) {
	c := newClock()
	l := Per(1, time.Hour, c)
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("first call denied")
	}
	c.Advance(-2 * time.Hour)
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("allowed after the clock jumped back")
	}
	c.Advance(2 * time.Hour)
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("allowed after the clock returned")
	}
	c.Advance(time.Hour)
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("denied after a real hour")
	}
}

func TestPerRejectsBadArguments(t *testing.T) {
	cases := map[string]func(){
		"zero events":     func() { Per(0, time.Minute, newClock()) },
		"negative events": func() { Per(-1, time.Minute, newClock()) },
		"zero period":     func() { Per(5, 0, newClock()) },
		"negative period": func() { Per(5, -time.Minute, newClock()) },
		"nil clock":       func() { Per(5, time.Minute, nil) },
	}
	for name, f := range cases {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			f()
		}()
	}
}

// Many keys, fresh keys and a moving clock exercise the map and the refund path under -race.
func TestConcurrentAcquireRefundManyKeys(t *testing.T) {
	c := newClock()
	l := Per(5, time.Minute, c)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 20 {
				if refund, ok, _ := l.Acquire(strconv.Itoa((i + j) % 7)); ok {
					refund()
				}
				l.Allow(strconv.Itoa(i*100 + j))
				c.Advance(time.Millisecond)
			}
		}()
	}
	wg.Wait()
}

// A client that sleeps exactly the advertised wait must pass, and a denial never advertises 0.
func TestWaitIsHonest(t *testing.T) {
	periods := []time.Duration{time.Second, time.Minute, 15 * time.Minute, time.Hour + 7*time.Minute + 13*time.Second + 17*time.Millisecond}
	for _, p := range periods {
		for _, n := range []int{1, 3, 7, 12, 14, 17, 22, 60, 1000} {
			for _, frac := range []time.Duration{0, 3, 2} { // refill none, 1/3 or 1/2 of a token first
				c := newClock()
				l := Per(n, p, c)
				for range n {
					l.Allow("k")
				}
				if frac != 0 {
					c.Advance(p / time.Duration(n) / frac)
				}
				ok, wait := l.Allow("k")
				if ok || wait <= 0 {
					t.Fatalf("Per(%d, %v): ok=%v wait=%v", n, p, ok, wait)
				}
				c.Advance(wait)
				if ok, w := l.Allow("k"); !ok {
					t.Fatalf("Per(%d, %v): denied (wait %v) after sleeping the advertised %v", n, p, w, wait)
				}
			}
		}
	}
}

// Rates where float rounding lands just below a whole token; epsilon must absorb it.
func TestAwkwardRates(t *testing.T) {
	for _, r := range []struct {
		n int
		p time.Duration
	}{
		{394, 3544818 * time.Microsecond},
		{850, 3332850 * time.Microsecond},
		{841, 5230179 * time.Microsecond},
		{1854, 774972 * time.Millisecond},
		{1753, 14225595 * time.Microsecond},
	} {
		c := newClock()
		l := Per(r.n, r.p, c)
		for range r.n {
			l.Allow("k")
		}
		_, wait := l.Allow("k")
		c.Advance(wait)
		if ok, _ := l.Allow("k"); !ok {
			t.Errorf("Per(%d, %v): denied after the advertised wait %v", r.n, r.p, wait)
		}
	}
}

func TestRefillIsCappedAtBurst(t *testing.T) {
	c := newClock()
	l := Per(5, time.Second, c)
	for range 5 {
		l.Allow("k")
	}
	c.Advance(30 * time.Second) // shorter than sweepEvery, so a sweep can't hide a missing cap
	granted := 0
	for range 10 {
		if ok, _ := l.Allow("k"); ok {
			granted++
		}
	}
	if granted != 5 {
		t.Fatalf("%d events after a long idle period, want the burst of 5", granted)
	}
}

func TestLateRefundIsCappedAtBurst(t *testing.T) {
	c := newClock()
	l := Per(5, 15*time.Minute, c)
	refund, _, _ := l.Acquire("phone")
	c.Advance(15 * time.Minute) // time alone has restored the event
	refund()
	for range 5 {
		if _, ok, _ := l.Acquire("phone"); !ok {
			t.Fatal("burst not available")
		}
	}
	if _, ok, _ := l.Acquire("phone"); ok {
		t.Fatal("a late refund raised the bucket above its burst")
	}
}

func TestSweepResumesAfterClockStepsBack(t *testing.T) {
	c := newClock()
	l := Per(1, time.Minute, c)
	l.Allow("a")
	c.Advance(-24 * time.Hour)
	l.Allow("e")
	c.Advance(2 * time.Minute)
	l.Allow("f") // "e" is full again, so this call must sweep it
	if _, ok := l.buckets["e"]; ok {
		t.Fatal("sweeping stalled after the clock stepped back")
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/ratelimit/`
Expected: FAIL — ошибки компиляции, например `undefined: Per`.

- [ ] **Step 3: Реализация**

<!-- file: internal/ratelimit/ratelimit.go -->
```go
// Package ratelimit provides in-memory per-key token buckets.
// State is lost on restart and not shared between instances (spec section 8).
package ratelimit

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/clock"
)

const (
	sweepEvery = time.Minute
	// epsilon absorbs float rounding in refills, so a token that is due is never missed.
	epsilon = 1e-9
)

// Limiter holds one token bucket per key (client IP, phone number). It is safe for concurrent use.
type Limiter struct {
	mu        sync.Mutex
	n         float64       // events per period, also the burst size
	period    time.Duration // time to refill a whole bucket
	clock     clock.Clock
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	last   time.Time // refills are counted from here; a clock moving backwards adds nothing
}

// Per allows n events per period for each key, with bursts of up to n. It panics on n < 1,
// a non-positive period or a nil clock: a zero window would silently allow or block everything.
func Per(n int, period time.Duration, c clock.Clock) *Limiter {
	if n < 1 || period <= 0 || c == nil {
		panic(fmt.Sprintf("ratelimit: invalid Per(%d, %v)", n, period))
	}
	return &Limiter{n: float64(n), period: period, clock: c, buckets: make(map[string]*bucket)}
}

// Allow consumes one event for key. A denied call consumes nothing and returns the time
// until the next event becomes available.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.take(key)
}

// Acquire is Allow with an undo. The event is taken up front, so parallel requests can't all
// pass a check made before their outcome is known; refund gives it back, which lets a caller
// count only failures (for example, refund after a successful login). refund may be called
// any number of times, including after a denial; only the first call of a granted event counts.
func (l *Limiter) Acquire(key string) (refund func(), ok bool, wait time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ok, wait = l.take(key); !ok {
		return func() {}, false, wait
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			// Capped at the burst: if the request outlived period/n, time may have refilled it already.
			b := l.bucket(key)
			b.tokens = min(l.n, b.tokens+1)
		})
	}, true, 0
}

// take consumes one token or reports how long until one is available. l.mu must be held.
func (l *Limiter) take(key string) (bool, time.Duration) {
	b := l.bucket(key)
	if b.tokens >= 1-epsilon {
		b.tokens = max(0, b.tokens-1)
		return true, 0
	}
	// Ceil: a client that waits exactly this long must find the token there.
	return false, time.Duration(math.Ceil((1 - b.tokens) * float64(l.period) / l.n))
}

// bucket returns the refilled bucket for key, creating a full one if needed. l.mu must be held.
func (l *Limiter) bucket(key string) *bucket {
	now := l.clock.Now()
	if now.Sub(l.lastSweep) > sweepEvery || now.Before(l.lastSweep) {
		// A full bucket behaves exactly like a fresh one, so dropping it changes nothing.
		for k, b := range l.buckets {
			if l.refill(b, now); b.tokens >= l.n-epsilon {
				delete(l.buckets, k)
			}
		}
		l.lastSweep = now
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.n, last: now}
		l.buckets[key] = b
	}
	l.refill(b, now)
	return b
}

func (l *Limiter) refill(b *bucket, now time.Time) {
	if now.After(b.last) {
		b.tokens = min(l.n, b.tokens+float64(now.Sub(b.last))/float64(l.period)*l.n)
		b.last = now
	}
}
```

- [ ] **Step 4: Тест проходит, в том числе под детектором гонок**

```bash
go test ./internal/ratelimit/
go test -race -count=5 ./internal/ratelimit/
```
Expected: оба `ok`. Детектору гонок нужен cgo (gcc); если его нет, достаточно первой команды — CI прогоняет `-race`.

- [ ] **Step 5: Контрольная точка** — выдать владельцу команды:

```bash
git add internal/ratelimit
git commit -m "feat(ratelimit): in-memory per-key limiters"
```

---

### Task 10: Пакет `notify`

Интерфейсы `SMSSender`/`EmailSender`, log-заглушки (SMS в dev, email при пустом `SMTP_HOST`), `Recorder` для тестов (перехватывает коды), тексты из встроенных шаблонов и SMTP-отправитель. Отправка письма целиком ограничена 10 секундами и прерывается вместе с запросом (go-mail применяет контекст только к соединению); `QUIT` после принятого письма идёт в фоне, и его сбой или зависание ошибкой не считаются. SMTP проверяется на поддельном сервере прямо в тесте: обязательный STARTTLS, пароль не уходит без шифрования, таймаут, `QUIT`.

**Files:**
- Create: `internal/notify/notify.go`, `internal/notify/recorder.go`, `internal/notify/texts.go`, `internal/notify/smtp.go`, `internal/notify/templates/sms.tmpl`, `internal/notify/templates/email.tmpl`
- Test: `internal/notify/notify_test.go`, `internal/notify/smtp_test.go`

- [ ] **Step 1: Написать тесты** — `internal/notify/notify_test.go`:

<!-- file: internal/notify/notify_test.go -->
```go
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
```

`internal/notify/smtp_test.go` (минимальный SMTP-сервер для проверки отправителя):

<!-- file: internal/notify/smtp_test.go -->
```go
package notify

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a minimal SMTP server: enough of the protocol for go-mail, with switches for
// the failure modes the sender must survive.
type fakeSMTP struct {
	ext      []string // extra EHLO lines, e.g. "AUTH PLAIN LOGIN"
	hang     bool     // send the greeting, then never answer
	dropQuit bool     // close the connection instead of answering QUIT
	hangQuit bool     // never answer QUIT

	mu    sync.Mutex
	conns []net.Conn
	cmds  []string
	data  string
}

func (f *fakeSMTP) start(t *testing.T) SMTPConfig {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		l.Close()
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, c := range f.conns {
			c.Close() // unblocks a sender still waiting on a hanging server
		}
	})
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.conns = append(f.conns, c)
			f.mu.Unlock()
			go f.serve(c)
		}
	}()
	return SMTPConfig{Host: "127.0.0.1", Port: l.Addr().(*net.TCPAddr).Port, From: "noreply@example.com", TLS: "none"}
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	reply := func(s string) { fmt.Fprint(c, s+"\r\n") }
	reply("220 fake ESMTP")
	if f.hang {
		_, _ = io.Copy(io.Discard, c)
		return
	}
	r := bufio.NewReader(c)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		f.mu.Lock()
		f.cmds = append(f.cmds, cmd)
		f.mu.Unlock()
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			lines := append([]string{"fake"}, f.ext...)
			for i, l := range lines {
				sep := "-"
				if i == len(lines)-1 {
					sep = " "
				}
				reply("250" + sep + l)
			}
		case strings.HasPrefix(cmd, "AUTH"):
			reply("535 5.7.8 authentication failed")
		case cmd == "DATA":
			reply("354 go ahead")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			reply("250 2.0.0 queued")
		case cmd == "QUIT":
			switch {
			case f.hangQuit:
				_, _ = io.Copy(io.Discard, c)
			case !f.dropQuit:
				reply("221 bye")
			}
			return
		default:
			reply("250 OK")
		}
	}
}

func (f *fakeSMTP) sent(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.cmds {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func newSMTP(t *testing.T, cfg SMTPConfig) *SMTP {
	t.Helper()
	s, err := NewSMTP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSMTPSends(t *testing.T) {
	f := &fakeSMTP{}
	s := newSMTP(t, f.start(t))
	err := s.SendEmail(context.Background(), "user@example.com", "Качалка: код\r\nBcc: evil@example.com", "Код: 123456")
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	data := f.data
	f.mu.Unlock()
	if !strings.Contains(data, "123456") {
		t.Fatalf("code missing from the message:\n%s", data)
	}
	if strings.Contains(data, "\nBcc:") {
		t.Fatalf("CRLF in the subject injected a header:\n%s", data)
	}
	if err := s.SendEmail(context.Background(), "a@example.com, evil@example.com", "s", "b"); err == nil {
		t.Fatal("a list of recipients was accepted")
	}
}

func TestSMTPStartTLSIsMandatory(t *testing.T) {
	f := &fakeSMTP{} // does not offer STARTTLS
	cfg := f.start(t)
	cfg.TLS = "starttls"
	if err := newSMTP(t, cfg).SendEmail(context.Background(), "user@example.com", "s", "b"); err == nil {
		t.Fatal("sent without TLS although STARTTLS is required")
	}
	if f.sent("MAIL") {
		t.Fatal("the message went out in clear text")
	}
}

func TestSMTPNoPlainPasswordWithoutTLS(t *testing.T) {
	f := &fakeSMTP{ext: []string{"AUTH PLAIN LOGIN"}}
	cfg := f.start(t)
	cfg.Username, cfg.Password = "user", "secret"
	if err := newSMTP(t, cfg).SendEmail(context.Background(), "user@example.com", "s", "b"); err == nil {
		t.Fatal("sent although the only auth methods expose the password")
	}
	if f.sent("AUTH") {
		t.Fatal("the password was offered over an unencrypted connection")
	}
}

func TestSMTPHonoursContext(t *testing.T) {
	f := &fakeSMTP{hang: true}
	s := newSMTP(t, f.start(t))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := s.SendEmail(ctx, "user@example.com", "s", "b"); err == nil {
		t.Fatal("no error from a server that never answers")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("returned after %v, the context allowed 200ms", d)
	}
}

// Once the message is accepted, a broken goodbye must neither fail the send nor delay it.
func TestSMTPFailedQuitIsNotAnError(t *testing.T) {
	for name, f := range map[string]*fakeSMTP{"dropped": {dropQuit: true}, "hanging": {hangQuit: true}} {
		start := time.Now()
		if err := newSMTP(t, f.start(t)).SendEmail(context.Background(), "user@example.com", "s", "b"); err != nil {
			t.Errorf("%s QUIT: message was accepted, but SendEmail returned %v", name, err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s QUIT: SendEmail waited %v for the goodbye", name, d)
		}
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/notify/`
Expected: FAIL — ошибки компиляции, например `undefined: Texts`, `undefined: NewRecorder`, `undefined: SMTPConfig`.

- [ ] **Step 3: Зависимость**

```bash
go get github.com/wneessen/go-mail@v0.8.1
```

- [ ] **Step 4: Интерфейсы и log-заглушки** — `internal/notify/notify.go`:

<!-- file: internal/notify/notify.go -->
```go
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
```

- [ ] **Step 5: Recorder** — `internal/notify/recorder.go`:

<!-- file: internal/notify/recorder.go -->
```go
package notify

import (
	"context"
	"regexp"
	"sync"
)

// Message is one captured SMS (Subject empty) or email.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Recorder captures messages in memory instead of sending them. Used by tests.
type Recorder struct {
	mu   sync.Mutex
	msgs []Message
	fail error
}

var (
	_ SMSSender   = (*Recorder)(nil)
	_ EmailSender = (*Recorder)(nil)
)

func NewRecorder() *Recorder { return &Recorder{} }

// FailWith makes subsequent sends return err (nil restores normal behaviour).
func (r *Recorder) FailWith(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fail = err
}

func (r *Recorder) SendSMS(_ context.Context, phone, text string) error {
	return r.add(Message{To: phone, Body: text})
}

func (r *Recorder) SendEmail(_ context.Context, to, subject, body string) error {
	return r.add(Message{To: to, Subject: subject, Body: body})
}

func (r *Recorder) add(m Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	r.msgs = append(r.msgs, m)
	return nil
}

// Count returns how many messages were sent to the recipient.
func (r *Recorder) Count(to string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, m := range r.msgs {
		if m.To == to {
			n++
		}
	}
	return n
}

var codeRe = regexp.MustCompile(`\b\d{6}\b`)

// LastCode returns the 6-digit code from the latest message to the recipient, or "".
func (r *Recorder) LastCode(to string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.msgs) - 1; i >= 0; i-- {
		if r.msgs[i].To == to {
			return codeRe.FindString(r.msgs[i].Body)
		}
	}
	return ""
}
```

- [ ] **Step 6: Шаблоны** — `internal/notify/templates/sms.tmpl`:

<!-- file: internal/notify/templates/sms.tmpl -->
```text
{{define "sms_phone_code"}}
{{.AppName}}: код {{.Code}}. Никому его не сообщайте.{{if .AppHash}}
{{.AppHash}}{{end}}
{{end}}
```

`internal/notify/templates/email.tmpl`:

<!-- file: internal/notify/templates/email.tmpl -->
```text
{{define "email_verify_subject"}}{{.AppName}}: подтверждение email{{end}}

{{define "email_verify_body"}}
Здравствуйте!

Код подтверждения email: {{.Code}}
Код действует {{.TTL}}.

Если вы не регистрировались в приложении {{.AppName}}, просто проигнорируйте это письмо.
{{end}}

{{define "password_reset_subject"}}{{.AppName}}: сброс пароля{{end}}

{{define "password_reset_body"}}
Здравствуйте!

Код для сброса пароля: {{.Code}}
Код действует {{.TTL}}.

Если вы не запрашивали сброс пароля, просто проигнорируйте это письмо — пароль останется прежним.
{{end}}
```

- [ ] **Step 7: Тексты** — `internal/notify/texts.go`:

<!-- file: internal/notify/texts.go -->
```go
package notify

import (
	"embed"
	"fmt"
	"math"
	"strings"
	"text/template"
	"time"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var templates = template.Must(template.ParseFS(templateFS, "templates/*.tmpl"))

// Texts renders message texts. Templates live in templates/ and are embedded in the binary.
type Texts struct {
	AppName    string
	SMSAppHash string        // optional Android SMS Retriever hash
	CodeTTL    time.Duration // shown to the user
}

type textData struct {
	AppName string
	Code    string
	AppHash string
	TTL     string // "10 минут"
}

func (t Texts) data(code string) textData {
	return textData{AppName: t.AppName, Code: code, AppHash: t.SMSAppHash, TTL: minutes(t.CodeTTL)}
}

// minutes renders d in whole minutes, rounded up so the text never promises more time than
// there is, with the Russian plural form: 1 минуту, 2 минуты, 5 минут, 11 минут, 21 минуту.
func minutes(d time.Duration) string {
	n := int(math.Ceil(d.Minutes()))
	form := "минут"
	switch {
	case n%100 >= 11 && n%100 <= 14:
	case n%10 == 1:
		form = "минуту"
	case n%10 >= 2 && n%10 <= 4:
		form = "минуты"
	}
	return fmt.Sprintf("%d %s", n, form)
}

func (t Texts) PhoneCode(code string) (string, error) {
	return render("sms_phone_code", t.data(code))
}

func (t Texts) EmailVerification(code string) (subject, body string, err error) {
	return renderMail("email_verify", t.data(code))
}

func (t Texts) PasswordReset(code string) (subject, body string, err error) {
	return renderMail("password_reset", t.data(code))
}

func renderMail(name string, d textData) (string, string, error) {
	subject, err := render(name+"_subject", d)
	if err != nil {
		return "", "", err
	}
	body, err := render(name+"_body", d)
	return subject, body, err
}

func render(name string, d textData) (string, error) {
	var b strings.Builder
	if err := templates.ExecuteTemplate(&b, name, d); err != nil {
		return "", err
	}
	return strings.TrimSpace(b.String()), nil
}
```

- [ ] **Step 8: SMTP** — `internal/notify/smtp.go`:

<!-- file: internal/notify/smtp.go -->
```go
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
```

- [ ] **Step 9: Тест проходит**

```bash
go mod tidy
go test ./internal/notify/
```
Expected: `ok` за пару секунд (SMTP — на поддельном сервере из `smtp_test.go`; настоящая отправка через Mailpit — в e2e части 3).

- [ ] **Step 10: Контрольная точка** — выдать владельцу команды:

```bash
git add go.mod go.sum internal/notify
git commit -m "feat(notify): senders, test recorder and message templates"
```

---

### Task 11: Пакет `config`

Читает env (вложенные структуры `SMTP` и `Rate` разбираются автоматически), проверяет значения и их границы (опечатка не должна тихо ослаблять защиту: например, `REFRESH_GRACE=30m` или `TRUSTED_PROXIES=0.0.0.0/0`), в `prod` требует секреты, SMTP с TLS и реальный SMS-провайдер (поэтому prod сейчас сознательно не стартует — см. риски спецификации). `Config` печатается без секретов — и через `fmt`, и через `slog`. Ключи refresh-токенов и кодов выводятся из `APP_SECRET` через HKDF; в dev без `APP_SECRET` используется публичное dev-значение.

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Написать тест**

<!-- file: internal/config/config_test.go -->
```go
package config

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"
)

func devVars() map[string]string {
	return map[string]string{"DATABASE_URL": "postgres://localhost/test"}
}

// prodVars is a complete prod configuration apart from SMS: until a real provider is added,
// SMS_PROVIDER=log is the only error it produces.
func prodVars() map[string]string {
	v := devVars()
	maps.Copy(v, map[string]string{
		"APP_ENV":              "prod",
		"APP_SECRET":           "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		"JWT_PRIVATE_KEY_FILE": "/run/secrets/jwt.pem",
		"SMTP_HOST":            "smtp.example.com",
		"SMTP_FROM":            "noreply@example.com",
	})
	return v
}

func TestDevDefaults(t *testing.T) {
	c, err := LoadFrom(devVars())
	if err != nil {
		t.Fatal(err)
	}
	if c.Env != EnvDev || c.HTTPAddr != ":8080" || c.AccessTokenTTL != 15*time.Minute ||
		c.RefreshTokenTTL != 60*24*time.Hour || c.RefreshGrace != 30*time.Second {
		t.Fatalf("unexpected defaults: %v %v %v %v", c.HTTPAddr, c.AccessTokenTTL, c.RefreshTokenTTL, c.RefreshGrace)
	}
	if c.SMTP.Port != 587 || c.SMTP.TLS != "starttls" || c.Rate.LoginFailuresPerPhone != 5 || c.Rate.CodeCooldown != time.Minute {
		t.Fatalf("nested defaults not applied: %+v %+v", c.SMTP, c.Rate)
	}
	if !c.UsingDevSecret() || len(c.Keys.Refresh) != 32 || len(c.Keys.OTP) != 32 {
		t.Fatal("dev secret and derived keys expected")
	}
	if bytes.Equal(c.Keys.Refresh, c.Keys.OTP) {
		t.Fatal("derived keys must differ")
	}
	if c.Level != slog.LevelInfo {
		t.Fatalf("level = %v", c.Level)
	}
}

// Known answers (checked against an independent RFC 5869 implementation). A change here
// would log every user out and invalidate every pending code.
func TestKeyDerivationIsStable(t *testing.T) {
	c, err := LoadFrom(devVars())
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(c.Keys.Refresh); got != "47eceb0e3ab7a8d2866b4a593b9f702ed59bc8e2e227a4f53f43ec56fe86c316" {
		t.Errorf("refresh key %s", got)
	}
	if got := hex.EncodeToString(c.Keys.OTP); got != "2ea6e6a3a9b465eaa40c778587d7c12d6650520bf034e028ce8c0020c6d48a92" {
		t.Errorf("otp key %s", got)
	}
}

func TestDatabaseURLRequired(t *testing.T) {
	if _, err := LoadFrom(map[string]string{}); err == nil {
		t.Fatal("expected error without DATABASE_URL")
	}
	if _, err := LoadFrom(map[string]string{"DATABASE_URL": ""}); err == nil {
		t.Fatal("empty DATABASE_URL accepted")
	}
}

func TestLoadFromNilIgnoresProcessEnvironment(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/real")
	if _, err := LoadFrom(nil); err == nil {
		t.Fatal("LoadFrom(nil) read the process environment")
	}
}

func TestTrustedProxies(t *testing.T) {
	v := devVars()
	v["TRUSTED_PROXIES"] = "172.18.0.0/16, 10.0.0.5,10.1.2.3/8,::ffff:192.0.2.1,"
	c, err := LoadFrom(v)
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("172.18.0.0/16"),
		netip.MustParsePrefix("10.0.0.5/32"),
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("192.0.2.1/32"),
	}
	if !slices.Equal(c.ProxyPrefixes, want) {
		t.Fatalf("got %v, want %v", c.ProxyPrefixes, want)
	}
	for _, bad := range []string{"nonsense", "0.0.0.0/0", "::/0"} {
		v["TRUSTED_PROXIES"] = bad
		if _, err := LoadFrom(v); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXIES") {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

// Each bad value must be refused with a message that names the variable.
func TestInvalidValuesNameTheirVariable(t *testing.T) {
	for _, c := range []struct {
		vars map[string]string
		want string
	}{
		{map[string]string{"APP_ENV": "PROD"}, "APP_ENV"},
		{map[string]string{"APP_NAME": "Качалка Фитнес Клуб Москва"}, "APP_NAME"},
		{map[string]string{"APP_NAME": "Кач\nалка"}, "APP_NAME"},
		{map[string]string{"APP_NAME": "Кач" + string(rune(0x2028)) + "алка"}, "APP_NAME"}, // Unicode line separator
		{map[string]string{"SMS_PROVIDER": "twilio"}, "SMS_PROVIDER"},
		{map[string]string{"SMTP_TLS": "ssl"}, "SMTP_TLS"},
		{map[string]string{"SMTP_HOST": "smtp.example.com", "SMTP_FROM": "noreply@example.com", "SMTP_PORT": "70000"}, "SMTP_PORT"},
		{map[string]string{"SMTP_HOST": "smtp.example.com", "SMTP_FROM": "noreply@example.com", "SMTP_PORT": "0"}, "SMTP_PORT"},
		{map[string]string{"SMS_APP_HASH": "FA+9qCX9VS"}, "SMS_APP_HASH"},
		{map[string]string{"SMS_APP_HASH": "FA+9qCX9VSu\n"}, "SMS_APP_HASH"},
		{map[string]string{"SMTP_HOST": "smtp.example.com", "SMTP_FROM": "not an address"}, "SMTP_FROM"},
		{map[string]string{"ACCESS_TOKEN_TTL": "0s"}, "ACCESS_TOKEN_TTL"},
		{map[string]string{"ACCESS_TOKEN_TTL": "2h"}, "ACCESS_TOKEN_TTL"},
		{map[string]string{"REFRESH_TOKEN_TTL": "0s"}, "REFRESH_TOKEN_TTL"},
		{map[string]string{"REFRESH_TOKEN_TTL": "10m"}, "REFRESH_TOKEN_TTL"},
		{map[string]string{"REFRESH_GRACE": "-1s"}, "REFRESH_GRACE"},
		{map[string]string{"REFRESH_GRACE": "30m"}, "REFRESH_GRACE"},
		{map[string]string{"PASSWORD_HASH_CONCURRENCY": "0"}, "PASSWORD_HASH_CONCURRENCY"},
		{map[string]string{"PASSWORD_HASH_CONCURRENCY": "65"}, "PASSWORD_HASH_CONCURRENCY"},
		{map[string]string{"LOG_LEVEL": "loud"}, "LOG_LEVEL"},
		{map[string]string{"RATE_LOGIN_PER_IP_PER_MINUTE": "0"}, "RATE_LOGIN_PER_IP_PER_MINUTE"},
		{map[string]string{"RATE_LOGIN_FAILURES_PER_PHONE": "0"}, "RATE_LOGIN_FAILURES_PER_PHONE"},
		{map[string]string{"RATE_CODES_PER_TARGET_PER_HOUR": "0"}, "RATE_CODES_PER_TARGET_PER_HOUR"},
		{map[string]string{"RATE_CODES_PER_IP_PER_HOUR": "0"}, "RATE_CODES_PER_IP_PER_HOUR"},
		{map[string]string{"RATE_REFRESH_PER_IP_PER_MINUTE": "0"}, "RATE_REFRESH_PER_IP_PER_MINUTE"},
		{map[string]string{"RATE_LOGIN_FAILURES_WINDOW": "0s"}, "RATE_LOGIN_FAILURES_WINDOW"},
		{map[string]string{"RATE_LOGIN_FAILURES_WINDOW": "30s"}, "RATE_LOGIN_FAILURES_WINDOW"},
		{map[string]string{"RATE_CODE_COOLDOWN": "-1s"}, "RATE_CODE_COOLDOWN"},
	} {
		v := devVars()
		maps.Copy(v, c.vars)
		if _, err := LoadFrom(v); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: err = %v", c.vars, err)
		}
	}
}

func TestEdgeValuesAccepted(t *testing.T) {
	v := devVars()
	maps.Copy(v, map[string]string{
		"APP_NAME":                   "Качалка Фитнес Клуб",
		"ACCESS_TOKEN_TTL":           "1h",
		"REFRESH_TOKEN_TTL":          "1h",
		"REFRESH_GRACE":              "5m",
		"SMS_APP_HASH":               "FA+9qCX9VSu",
		"PASSWORD_HASH_CONCURRENCY":  "64",
		"RATE_LOGIN_FAILURES_WINDOW": "1m",
		"RATE_CODE_COOLDOWN":         "0s",
		"SMTP_HOST":                  "smtp.example.com",
		"SMTP_FROM":                  "Качалка <noreply@example.com>",
		"SMTP_PORT":                  "465",
	})
	if _, err := LoadFrom(v); err != nil {
		t.Fatal(err)
	}
}

func TestAppSecretFormat(t *testing.T) {
	for secret, ok := range map[string]bool{
		"+/+/+/+/+/+/+/+/+/+/+/+/+/+/+/+/+/+/+/+/+/8=": true,  // standard alphabet, 32 bytes
		"-_-_-_-_-_-_-_-_-_-_-_-_-_-_-_-_-_-_-_-_-_8=": false, // URL-safe alphabet
		"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=": true,  // 32 bytes
		"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8":  false, // padding missing
		"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHg==": false, // 31 bytes
	} {
		v := devVars()
		v["APP_SECRET"] = secret
		_, err := LoadFrom(v)
		if (err == nil) != ok {
			t.Errorf("%s: err = %v, want ok=%v", secret, err, ok)
		}
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Errorf("error reveals the secret: %v", err)
		}
	}
}

func TestDevSecretIsRecognisedWhenSetExplicitly(t *testing.T) {
	for _, secret := range []string{devAppSecret, devAppSecret + "\n", devAppSecret[:20] + "\r\n" + devAppSecret[20:]} {
		v := devVars()
		v["APP_SECRET"] = secret
		c, err := LoadFrom(v)
		if err != nil || !c.UsingDevSecret() {
			t.Fatalf("dev, %q: err = %v, UsingDevSecret = %v", secret, err, c.UsingDevSecret())
		}
		p := prodVars()
		p["APP_SECRET"] = secret
		if _, err := LoadFrom(p); err == nil || !strings.Contains(err.Error(), "public dev value") {
			t.Fatalf("prod, %q: err = %v", secret, err)
		}
	}
}

// validate runs first and already refuses this; derive must not fall back to the dev secret on its own.
func TestDeriveRefusesEmptySecretInProd(t *testing.T) {
	c := Config{Env: EnvProd}
	if err := c.derive(); err == nil {
		t.Fatal("derive fell back to the dev secret in prod")
	}
}

func TestProdRequiresHardening(t *testing.T) {
	v := devVars()
	v["APP_ENV"] = "prod"
	v["SMTP_TLS"] = "none"
	v["RATE_CODE_COOLDOWN"] = "0s"
	_, err := LoadFrom(v)
	if err == nil {
		t.Fatal("prod with defaults must fail")
	}
	for _, want := range []string{"APP_SECRET", "JWT_PRIVATE_KEY_FILE", "SMS_PROVIDER=log", "SMTP_HOST", "SMTP_TLS=none", "RATE_CODE_COOLDOWN=0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}

func TestProdErrorOnlyAboutSMSWhenOtherwiseComplete(t *testing.T) {
	_, err := LoadFrom(prodVars())
	if err == nil || err.Error() != "SMS_PROVIDER=log is forbidden in prod: codes would end up in logs; add a real provider (see README)" {
		t.Fatalf("want exactly the SMS_PROVIDER error, got %v", err)
	}
}

func TestConfigHidesSecrets(t *testing.T) {
	v := devVars()
	v["DATABASE_URL"] = "postgres://app:db-password@localhost/test"
	v["APP_SECRET"] = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	v["SMTP_USERNAME"] = "smtp-api-key"
	v["SMTP_PASSWORD"] = "smtp-password"
	c, err := LoadFrom(v)
	if err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("start", "config", c, "smtp", c.SMTP, "keys", c.Keys)
	secrets := []string{
		"db-password", "smtp-api-key", "smtp-password", v["APP_SECRET"],
		strings.Trim(fmt.Sprintf("%d", c.Keys.Refresh), "[]"), // as fmt prints []byte
		base64.StdEncoding.EncodeToString(c.Keys.Refresh),     // as JSON encodes []byte
	}
	for _, out := range []string{
		fmt.Sprintf("%v %+v %s %#v", c, c, c, c),
		fmt.Sprintf("%v %+v %#v %v %+v %#v", c.SMTP, c.SMTP, c.SMTP, c.Keys, c.Keys, c.Keys),
		logged.String(),
	} {
		for _, secret := range secrets {
			if strings.Contains(out, secret) {
				t.Fatalf("%q leaks into %s", secret, out)
			}
		}
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/config/`
Expected: FAIL — ошибки компиляции, например `undefined: LoadFrom`, `undefined: EnvDev`.

- [ ] **Step 3: Зависимость**

```bash
go get github.com/caarlos0/env/v11@v11.4.1
```

- [ ] **Step 4: Реализация**

<!-- file: internal/config/config.go -->
```go
// Package config loads and validates settings from environment variables.
package config

import (
	"bytes"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"net/netip"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/caarlos0/env/v11"
)

const (
	EnvDev  = "dev"
	EnvProd = "prod"

	minSecretLen = 32
	// devAppSecret is base64("dev-insecure-app-secret-change-me!"); used only in dev when APP_SECRET is empty.
	devAppSecret = "ZGV2LWluc2VjdXJlLWFwcC1zZWNyZXQtY2hhbmdlLW1lIQ==" //nolint:gosec // G101: public dev default, refused in prod

	// Bounds that keep a typo from quietly weakening protection.
	maxAppNameLen    = 20              // the SMS with the app hash still fits one Cyrillic segment (70)
	maxAccessTTL     = time.Hour       // access tokens can't be revoked; logout takes effect within this time
	maxRefreshGrace  = 5 * time.Minute // a stolen previous refresh token is honoured this long
	maxHashWorkers   = 64              // each password hash holds ~19 MiB
	minFailureWindow = time.Minute     // shorter windows let password guessing run at full speed
)

// smsAppHash is the 11-character hash of the Android SMS Retriever API.
var smsAppHash = regexp.MustCompile(`^[A-Za-z0-9+/]{11}$`)

type Config struct {
	Env                     string        `env:"APP_ENV" envDefault:"dev"`
	AppName                 string        `env:"APP_NAME" envDefault:"Качалка"`
	HTTPAddr                string        `env:"HTTP_ADDR" envDefault:":8080"`
	DatabaseURL             string        `env:"DATABASE_URL,required,notEmpty"`
	AppSecret               string        `env:"APP_SECRET"`
	JWTPrivateKeyFile       string        `env:"JWT_PRIVATE_KEY_FILE"`
	JWTIssuer               string        `env:"JWT_ISSUER" envDefault:"go-kachalka"`
	AccessTokenTTL          time.Duration `env:"ACCESS_TOKEN_TTL" envDefault:"15m"`
	RefreshTokenTTL         time.Duration `env:"REFRESH_TOKEN_TTL" envDefault:"1440h"`
	RefreshGrace            time.Duration `env:"REFRESH_GRACE" envDefault:"30s"`
	SMSProvider             string        `env:"SMS_PROVIDER" envDefault:"log"`
	SMSAppHash              string        `env:"SMS_APP_HASH"`
	TrustedProxies          []string      `env:"TRUSTED_PROXIES" envSeparator:","`
	PasswordHashConcurrency int           `env:"PASSWORD_HASH_CONCURRENCY" envDefault:"4"`
	LogLevel                string        `env:"LOG_LEVEL" envDefault:"info"`
	SMTP                    SMTP
	Rate                    Rate

	// Derived in Load; not read from the environment.
	Keys          Keys           `env:"-"`
	ProxyPrefixes []netip.Prefix `env:"-"`
	Level         slog.Level     `env:"-"`
}

type SMTP struct {
	Host     string `env:"SMTP_HOST"`
	Port     int    `env:"SMTP_PORT" envDefault:"587"`
	Username string `env:"SMTP_USERNAME"`
	Password string `env:"SMTP_PASSWORD"`
	From     string `env:"SMTP_FROM"`
	TLS      string `env:"SMTP_TLS" envDefault:"starttls"`
}

type Rate struct {
	LoginPerIPPerMinute   int           `env:"RATE_LOGIN_PER_IP_PER_MINUTE" envDefault:"20"`
	LoginFailuresPerPhone int           `env:"RATE_LOGIN_FAILURES_PER_PHONE" envDefault:"5"`
	LoginFailuresWindow   time.Duration `env:"RATE_LOGIN_FAILURES_WINDOW" envDefault:"15m"`
	CodeCooldown          time.Duration `env:"RATE_CODE_COOLDOWN" envDefault:"60s"`
	CodesPerTargetPerHour int           `env:"RATE_CODES_PER_TARGET_PER_HOUR" envDefault:"5"`
	CodesPerIPPerHour     int           `env:"RATE_CODES_PER_IP_PER_HOUR" envDefault:"20"`
	RefreshPerIPPerMinute int           `env:"RATE_REFRESH_PER_IP_PER_MINUTE" envDefault:"60"`
}

// Keys are HKDF-derived from APP_SECRET so one secret serves several independent purposes.
type Keys struct {
	Refresh []byte
	OTP     []byte
}

func (c Config) IsProd() bool { return c.Env == EnvProd }

// UsingDevSecret reports that the insecure, publicly known dev secret is in use.
func (c Config) UsingDevSecret() bool { return c.AppSecret == "" || isDevSecret(c.AppSecret) }

// isDevSecret also catches other spellings of the dev value (line breaks, padding bits):
// they decode to the same bytes and so to the same keys.
func isDevSecret(s string) bool {
	got, err := base64.StdEncoding.DecodeString(s)
	dev, _ := base64.StdEncoding.DecodeString(devAppSecret)
	return err == nil && bytes.Equal(got, dev)
}

// String, GoString and LogValue keep secrets (APP_SECRET, the derived keys, passwords in
// DATABASE_URL and SMTP_PASSWORD, SMTP_USERNAME which some providers use as an API key) out of logs and error messages, whatever prints a Config.
// SMTP and Keys do the same when printed on their own.
func (c Config) String() string {
	return fmt.Sprintf("config{env=%s addr=%s %v sms=%s}", c.Env, c.HTTPAddr, c.SMTP, c.SMSProvider)
}

func (c Config) GoString() string { return c.String() }

func (c Config) LogValue() slog.Value { return slog.StringValue(c.String()) }

func (s SMTP) String() string {
	return fmt.Sprintf("smtp{host=%s port=%d from=%s tls=%s}", s.Host, s.Port, s.From, s.TLS)
}

func (s SMTP) GoString() string { return s.String() }

func (s SMTP) LogValue() slog.Value { return slog.StringValue(s.String()) }

func (Keys) String() string { return "keys{redacted}" }

func (k Keys) GoString() string { return k.String() }

func (k Keys) LogValue() slog.Value { return slog.StringValue(k.String()) }

// Load reads the process environment.
func Load() (Config, error) { return load(env.Options{}) }

// LoadFrom reads only the given variables, never the process environment (for tests).
func LoadFrom(vars map[string]string) (Config, error) {
	if vars == nil {
		vars = map[string]string{} // env treats a nil map as "use the process environment"
	}
	return load(env.Options{Environment: vars})
}

func load(opts env.Options) (Config, error) {
	c, err := env.ParseAsWithOptions[Config](opts)
	if err != nil {
		return Config{}, err
	}
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	if err := c.derive(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c *Config) validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	switch c.Env {
	case EnvDev, EnvProd:
	default:
		add("APP_ENV must be %q or %q, got %q", EnvDev, EnvProd, c.Env)
	}
	if n := utf8.RuneCountInString(c.AppName); n > maxAppNameLen || strings.IndexFunc(c.AppName, notPrintable) >= 0 {
		add("APP_NAME must be at most %d characters on one line, got %q", maxAppNameLen, c.AppName)
	}
	if c.SMSProvider != "log" {
		add("SMS_PROVIDER %q is not supported (available: log)", c.SMSProvider)
	}
	if c.SMSAppHash != "" && !smsAppHash.MatchString(c.SMSAppHash) {
		add("SMS_APP_HASH must be the 11-character SMS Retriever hash, got %q", c.SMSAppHash)
	}
	switch c.SMTP.TLS {
	case "starttls", "tls", "none":
	default:
		add("SMTP_TLS must be starttls, tls or none, got %q", c.SMTP.TLS)
	}
	if c.SMTP.Host != "" {
		if c.SMTP.Port < 1 || c.SMTP.Port > 65535 {
			add("SMTP_PORT must be 1-65535, got %d", c.SMTP.Port)
		}
		if _, err := mail.ParseAddress(c.SMTP.From); err != nil {
			add("SMTP_FROM must be an email address, got %q", c.SMTP.From)
		}
	}
	if c.AccessTokenTTL <= 0 || c.AccessTokenTTL > maxAccessTTL {
		add("ACCESS_TOKEN_TTL must be in (0, %v], got %v", maxAccessTTL, c.AccessTokenTTL)
	}
	if c.RefreshTokenTTL <= 0 || c.RefreshTokenTTL < c.AccessTokenTTL {
		add("REFRESH_TOKEN_TTL must be positive and not shorter than ACCESS_TOKEN_TTL, got %v", c.RefreshTokenTTL)
	}
	if c.RefreshGrace < 0 || c.RefreshGrace > maxRefreshGrace {
		add("REFRESH_GRACE must be in [0, %v], got %v", maxRefreshGrace, c.RefreshGrace)
	}
	if c.PasswordHashConcurrency < 1 || c.PasswordHashConcurrency > maxHashWorkers {
		add("PASSWORD_HASH_CONCURRENCY must be 1-%d, got %d", maxHashWorkers, c.PasswordHashConcurrency)
	}
	r := c.Rate
	for _, lim := range []struct {
		name string
		v    int
	}{
		{"RATE_LOGIN_PER_IP_PER_MINUTE", r.LoginPerIPPerMinute},
		{"RATE_LOGIN_FAILURES_PER_PHONE", r.LoginFailuresPerPhone},
		{"RATE_CODES_PER_TARGET_PER_HOUR", r.CodesPerTargetPerHour},
		{"RATE_CODES_PER_IP_PER_HOUR", r.CodesPerIPPerHour},
		{"RATE_REFRESH_PER_IP_PER_MINUTE", r.RefreshPerIPPerMinute},
	} {
		if lim.v < 1 {
			add("%s must be at least 1, got %d", lim.name, lim.v)
		}
	}
	if r.LoginFailuresWindow < minFailureWindow {
		add("RATE_LOGIN_FAILURES_WINDOW must be at least %v, got %v", minFailureWindow, r.LoginFailuresWindow)
	}
	if r.CodeCooldown < 0 {
		add("RATE_CODE_COOLDOWN must not be negative, got %v", r.CodeCooldown)
	}
	if err := c.Level.UnmarshalText([]byte(c.LogLevel)); err != nil {
		add("LOG_LEVEL: %v", err)
	}
	for _, p := range c.TrustedProxies {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		prefix, err := parsePrefix(p)
		switch {
		case err != nil:
			add("TRUSTED_PROXIES: %q is not an IP or CIDR", p)
		case prefix.Bits() == 0:
			add("TRUSTED_PROXIES: %q would let every client set its own IP", p)
		default:
			c.ProxyPrefixes = append(c.ProxyPrefixes, prefix)
		}
	}
	if c.IsProd() {
		switch {
		case c.AppSecret == "":
			add("APP_SECRET is required in prod")
		case isDevSecret(c.AppSecret):
			add("APP_SECRET is the public dev value; generate one with keygen")
		}
		if c.JWTPrivateKeyFile == "" {
			add("JWT_PRIVATE_KEY_FILE is required in prod")
		}
		if c.SMSProvider == "log" {
			add("SMS_PROVIDER=log is forbidden in prod: codes would end up in logs; add a real provider (see README)")
		}
		if c.SMTP.Host == "" || c.SMTP.From == "" {
			add("SMTP_HOST and SMTP_FROM are required in prod")
		}
		if c.SMTP.TLS == "none" {
			add("SMTP_TLS=none is forbidden in prod")
		}
		if r.CodeCooldown == 0 {
			add("RATE_CODE_COOLDOWN=0 is forbidden in prod")
		}
	}
	return errors.Join(errs...)
}

func (c *Config) derive() error {
	raw := c.AppSecret
	if raw == "" {
		if c.IsProd() { // validate already refuses this; checked again so a reorder can't leak the dev secret
			return errors.New("APP_SECRET is required in prod")
		}
		raw = devAppSecret
	}
	secret, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return fmt.Errorf("APP_SECRET must be standard base64 as printed by keygen: %w", err)
	}
	if len(secret) < minSecretLen {
		return fmt.Errorf("APP_SECRET must decode to at least %d bytes, got %d", minSecretLen, len(secret))
	}
	if c.Keys.Refresh, err = hkdf.Key(sha256.New, secret, nil, "go-kachalka refresh v1", 32); err != nil {
		return err
	}
	if c.Keys.OTP, err = hkdf.Key(sha256.New, secret, nil, "go-kachalka otp v1", 32); err != nil {
		return err
	}
	return nil
}

func parsePrefix(s string) (netip.Prefix, error) {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	a = a.Unmap() // client addresses are compared unmapped
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// notPrintable flags line breaks (including U+2028), control and invisible format characters.
func notPrintable(r rune) bool { return !unicode.IsPrint(r) }
```

- [ ] **Step 5: Тест проходит**

```bash
go mod tidy
go test ./internal/config/
```
Expected: `ok`

- [ ] **Step 6: Контрольная точка** — выдать владельцу команды:

```bash
git add go.mod go.sum internal/config
git commit -m "feat(config): environment configuration with prod safeguards"
```

---

### Task 12: Итоговая проверка части 1

- [ ] **Step 1: Форматирование, vet, тесты**

```bash
gofmt -l ./internal
go vet ./...
go test ./...
go test -race ./...
go mod tidy -diff
```
Expected: `gofmt` и `go mod tidy -diff` ничего не выводят; `go vet` без ошибок; все пакеты `ok` (оба прогона тестов): apperr, clock, config, contact, notify, otp, password, ratelimit, token.

- [ ] **Step 2: Сообщить владельцу**, что часть 1 готова, и перейти к `docs/superpowers/plans/2026-10-23-part2-api.md`.
