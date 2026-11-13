# go_kachalka backend — часть 3: комплект поставки. План реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Всё, что нужно для сдачи: Docker-образ, локальный стек одной командой, сквозной тест, prod-комплект для своего сервера (HTTPS, бэкапы), линтер и CI, README.

**Architecture:** Многоступенчатый Dockerfile → distroless-образ с `api` и `keygen`. Dev: `docker-compose.yml` (Postgres, Mailpit, API). Prod: `deploy/compose.yaml` (Caddy с автоматическим HTTPS → API → Postgres, плюс ежедневный `pg_dump`). CI на GitHub Actions: актуальность сгенерированного кода, vet, тесты с `-race`, golangci-lint, govulncheck, e2e против compose.

**Tech Stack:** Docker / Compose v2, `golang:1.27.3`, `gcr.io/distroless/static-debian13:nonroot`, `postgres:17-alpine`, `axllent/mailpit:v1.32`, `caddy:2.11`, golangci-lint v2.14.0, govulncheck v1.9.0.

**Спецификация:** `docs/superpowers/specs/2026-10-22-auth-backend-design.md`
**Части плана:** 1 и 2 должны быть выполнены → 3 — этот файл.

---

## Правила выполнения

- **Коммиты и push делает только владелец.** На шагах «Контрольная точка» исполнитель выдаёт владельцу команды из шага и ждёт подтверждения.
- Все файлы и команды проверены на Windows с Docker Desktop: образ собирается, dev-стек и e2e-тест проходят, prod-стек проверен пробным запуском (HTTPS через Caddy, отказ старта со SMS-заглушкой в prod, бэкап и восстановление).
- Docker Hub ограничивает анонимные скачивания. Если `docker pull` отвечает `429 Too Many Requests`, выполните `docker login` или подождите.
- Порты 8080, 5432, 8025, 1025 (dev) и 80, 443 (пробный запуск prod) должны быть свободны.
- Команды с `curl` и перенаправлением `<` выполняйте в Git Bash: в Windows PowerShell 5.1 `curl` — псевдоним `Invoke-WebRequest` (там используйте `curl.exe`), а `<` не поддерживается.

## Файлы части 3

| Файл | Ответственность |
|---|---|
| `Dockerfile`, `.dockerignore` | образ с бинарниками `api` и `keygen`, healthcheck |
| `docker-compose.yml` | локальный стек: Postgres, Mailpit, API с ослабленными лимитами |
| `e2e/e2e_test.go` | сквозной тест против запущенного стека (тег `e2e`) |
| `deploy/compose.yaml`, `deploy/Caddyfile`, `deploy/backup.sh`, `deploy/.env.example` | prod-комплект |
| `.golangci.yml`, `.github/workflows/ci.yml` | линтер и CI |
| `README.md` | запуск, разработка, конфигурация, развёртывание, подключение SMS |

---

### Task 1: Docker-образ

Сборка на `golang:1.27.3` (совпадает с toolchain в `go.mod`), итоговый образ — distroless без shell, uid 65532. `HEALTHCHECK` вызывает `api healthcheck`. `CMD` (а не `ENTRYPOINT`) позволяет запускать в том же образе `keygen`.

**Files:**
- Create: `Dockerfile`, `.dockerignore`

- [ ] **Step 1: `Dockerfile`**

<!-- file: Dockerfile -->
```dockerfile
# syntax=docker/dockerfile:1

FROM golang:1.27.3 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/keygen ./cmd/keygen

# distroless: no shell, no package manager; runs as uid 65532 (nonroot).
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/api /out/keygen /usr/local/bin/
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=3 CMD ["api", "healthcheck"]
CMD ["api"]
```

- [ ] **Step 2: `.dockerignore`**

<!-- file: .dockerignore -->
```text
.git
.github
docs
deploy
e2e
**/*_test.go
*.md
.env
secrets
backups
```

- [ ] **Step 3: Собрать и проверить образ**

```bash
docker build -t go-kachalka-api:dev .
docker run --rm go-kachalka-api:dev api healthcheck
docker image ls go-kachalka-api:dev
```
Expected: сборка успешна; `healthcheck` завершается ошибкой `dial tcp 127.0.0.1:8080: connect: connection refused` (сервер не запущен — бинарник на месте и работает); размер образа около 40 МБ.

- [ ] **Step 4: Контрольная точка** — выдать владельцу команды:

```bash
git add Dockerfile .dockerignore
git commit -m "build: distroless Docker image"
```

---

### Task 2: Локальный стек

Healthcheck Postgres идёт по TCP (`-h 127.0.0.1`): через unix-сокет база отвечает ещё во время initdb, и API без этого падал бы на старте. Лимиты на коды ослаблены, чтобы мобильное приложение можно было проверять многократно.

**Files:**
- Create: `docker-compose.yml`

- [ ] **Step 1: `docker-compose.yml`**

<!-- file: docker-compose.yml -->
```yaml
# Local development stack: `docker compose up --build`.
# API: http://localhost:8080, emails: http://localhost:8025, SMS codes: `docker compose logs api`.
name: go-kachalka-dev

services:
  postgres:
    image: postgres:17-alpine
    environment:
      POSTGRES_DB: kachalka
      POSTGRES_USER: kachalka
      POSTGRES_PASSWORD: kachalka
    ports:
      - "5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      # -h 127.0.0.1: check TCP; the unix socket answers during initdb, before the real start.
      test: ["CMD-SHELL", "pg_isready -h 127.0.0.1 -U kachalka -d kachalka"]
      interval: 2s
      timeout: 3s
      retries: 30

  mailpit:
    image: axllent/mailpit:v1.32
    ports:
      - "8025:8025" # web UI and REST API
      - "1025:1025" # SMTP

  api:
    build: .
    environment:
      APP_ENV: dev
      DATABASE_URL: postgres://kachalka:kachalka@postgres:5432/kachalka?sslmode=disable
      SMTP_HOST: mailpit
      SMTP_PORT: "1025"
      SMTP_TLS: none
      SMTP_FROM: noreply@kachalka.local
      LOG_LEVEL: debug
      # Relaxed limits so the mobile app can be tested repeatedly from one machine.
      RATE_CODE_COOLDOWN: 5s
      RATE_CODES_PER_TARGET_PER_HOUR: "100"
      RATE_CODES_PER_IP_PER_HOUR: "1000"
      RATE_LOGIN_PER_IP_PER_MINUTE: "1000"
    ports:
      - "8080:8080"
    depends_on:
      postgres:
        condition: service_healthy
      mailpit:
        condition: service_started

volumes:
  pgdata:
```

- [ ] **Step 2: Поднять стек**

```bash
docker compose up --build -d --wait
docker compose ps
curl -s http://localhost:8080/readyz
```
Expected: `api`, `postgres`, `mailpit` — `healthy`; ответ `{"status":"ready"}`.

- [ ] **Step 3: Ручная проверка кода из SMS**

```bash
curl -s -X POST http://localhost:8080/v1/auth/register/start -H "Content-Type: application/json" -d "{\"phone\":\"+79991234567\"}"
docker compose logs api
```
Expected: ответ `202` с `verification_id`, `expires_in: 600`, `resend_after: 5`; в логе строка `sms not sent (log provider)` с текстом `Качалка: код NNNNNN. Никому его не сообщайте.`. Веб-интерфейс Mailpit открывается на http://localhost:8025.

- [ ] **Step 4: Контрольная точка** — выдать владельцу команды (стек оставить запущенным для Task 3):

```bash
git add docker-compose.yml
git commit -m "build: local docker compose stack"
```

---

### Task 3: Сквозной тест

Тест регистрирует случайный номер и email, берёт SMS-код из `docker compose logs api` (JSON-строки лога), код из письма — из REST API Mailpit, и проходит подтверждение email, вход, refresh и выход.

**Files:**
- Create: `e2e/e2e_test.go`

- [ ] **Step 1: Написать тест**

<!-- file: e2e/e2e_test.go -->
```go
//go:build e2e

// Package e2e is a smoke test against the running dev stack (docker compose up).
// Run from the repository root: go test -tags e2e ./e2e/
package e2e

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

var (
	baseURL    = env("BASE_URL", "http://localhost:8080")
	mailpitURL = env("MAILPIT_URL", "http://localhost:8025")
	codeRe     = regexp.MustCompile(`\b\d{6}\b`)
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func TestSmoke(t *testing.T) {
	n, err := rand.Int(rand.Reader, big.NewInt(10_000_000))
	if err != nil {
		t.Fatal(err)
	}
	phone := fmt.Sprintf("+7999%07d", n.Int64())
	email := fmt.Sprintf("e2e-%07d@example.com", n.Int64())
	const pass = "e2e password 123"

	st, body := call(t, "POST", "/v1/auth/register/start", "", map[string]any{"phone": phone})
	want(t, http.StatusAccepted, st, body)
	st, body = call(t, "POST", "/v1/auth/register/verify", "", map[string]any{
		"verification_id": body["verification_id"], "code": smsCode(t, phone),
	})
	want(t, http.StatusOK, st, body)
	st, body = call(t, "POST", "/v1/auth/register", "", map[string]any{
		"registration_token": body["registration_token"], "password": pass, "email": email, "device_name": "e2e",
	})
	want(t, http.StatusCreated, st, body)
	access := body["tokens"].(map[string]any)["access_token"].(string)
	refresh := body["tokens"].(map[string]any)["refresh_token"].(string)

	st, body = call(t, "POST", "/v1/me/email/verify", access, map[string]any{"code": emailCode(t, email)})
	want(t, http.StatusOK, st, body)
	if body["email_verified"] != true {
		t.Fatalf("email not verified: %v", body)
	}

	st, body = call(t, "POST", "/v1/auth/login", "", map[string]any{"phone": phone, "password": pass})
	want(t, http.StatusOK, st, body)
	st, body = call(t, "POST", "/v1/auth/refresh", "", map[string]any{"refresh_token": refresh})
	want(t, http.StatusOK, st, body)
	st, body = call(t, "POST", "/v1/auth/logout", access, map[string]any{"all_devices": true})
	want(t, http.StatusNoContent, st, body)

	st, body = call(t, "POST", "/v1/auth/login", "", map[string]any{"phone": phone, "password": "wrong password"})
	if st != http.StatusUnauthorized || body["code"] != "invalid_credentials" {
		t.Fatalf("wrong password: %d %v", st, body)
	}
}

func call(t *testing.T, method, path, bearer string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequestWithContext(context.Background(), method, baseURL+path, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v (is `docker compose up` running?)", method, path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func want(t *testing.T, status, got int, body map[string]any) {
	t.Helper()
	if got != status {
		t.Fatalf("status %d, want %d: %v", got, status, body)
	}
}

// smsCode reads the code the log SMS provider printed for phone.
func smsCode(t *testing.T, phone string) string {
	t.Helper()
	return eventually(t, func() string {
		out, err := exec.Command("docker", "compose", "logs", "--no-color", "--no-log-prefix", "api").Output()
		if err != nil {
			t.Fatalf("docker compose logs: %v", err)
		}
		code := ""
		sc := bufio.NewScanner(bytes.NewReader(out))
		for sc.Scan() {
			var line struct {
				Phone string `json:"phone"`
				Text  string `json:"text"`
			}
			if json.Unmarshal(sc.Bytes(), &line) == nil && line.Phone == phone {
				code = codeRe.FindString(line.Text)
			}
		}
		return code
	})
}

// emailCode reads the latest code sent to addr from Mailpit's API.
func emailCode(t *testing.T, addr string) string {
	t.Helper()
	return eventually(t, func() string {
		var list struct {
			Messages []struct {
				ID string `json:"ID"`
			} `json:"messages"`
		}
		getJSON(t, mailpitURL+"/api/v1/search?query="+url.QueryEscape("to:"+addr), &list)
		if len(list.Messages) == 0 {
			return ""
		}
		var msg struct {
			Text string `json:"Text"`
		}
		getJSON(t, mailpitURL+"/api/v1/message/"+list.Messages[0].ID, &msg)
		return codeRe.FindString(msg.Text)
	})
}

func getJSON(t *testing.T, u string, v any) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
}

func eventually(t *testing.T, f func() string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if s := strings.TrimSpace(f()); s != "" {
			return s
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("value did not appear in time")
	return ""
}
```

- [ ] **Step 2: Без стека тест падает**

```bash
docker compose stop api
go test -tags e2e -count=1 ./e2e/
```
Expected: FAIL — `... connect: connection refused (is docker compose up running?)`.

- [ ] **Step 3: Со стеком тест проходит**

```bash
docker compose up -d --wait
go vet -tags e2e ./e2e/
go test -tags e2e -count=1 -v ./e2e/
```
Expected: `--- PASS: TestSmoke`.

- [ ] **Step 4: Остановить стек**

```bash
docker compose down -v
```

- [ ] **Step 5: Контрольная точка** — выдать владельцу команды:

```bash
git add e2e
git commit -m "test: end-to-end smoke test against docker compose"
```

---

### Task 4: Prod-комплект

Caddy получает фиксированный IP в сети `backend`, и API доверяет `X-Forwarded-For` только ему; сам Caddy заменяет присланный клиентом `X-Forwarded-For` реальным адресом. API и Postgres наружу не публикуются. У всех контейнеров ротация логов. `backup` раз в сутки делает `pg_dump`.

**Files:**
- Create: `deploy/compose.yaml`, `deploy/Caddyfile`, `deploy/backup.sh`, `deploy/.env.example`

- [ ] **Step 1: `deploy/compose.yaml`**

<!-- file: deploy/compose.yaml -->
```yaml
# Production stack. Run from this directory: docker compose up -d --build
# Before the first start follow README → «Развёртывание на сервере».
name: go-kachalka

x-logging: &logging
  driver: json-file
  options:
    max-size: "10m"
    max-file: "5"

services:
  caddy:
    image: caddy:2.11
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
      - "443:443/udp"
    environment:
      DOMAIN: ${DOMAIN:?set DOMAIN in deploy/.env}
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
      - caddy_config:/config
    networks:
      backend:
        ipv4_address: 172.30.10.10 # fixed so the API trusts X-Forwarded-For only from Caddy
    depends_on:
      api:
        condition: service_healthy
    logging: *logging

  api:
    build: ..
    image: go-kachalka-api:latest
    restart: unless-stopped
    env_file: .env
    environment:
      APP_ENV: ${APP_ENV:-prod}
      DATABASE_URL: postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB}?sslmode=disable
      JWT_PRIVATE_KEY_FILE: /secrets/jwt.pem
      TRUSTED_PROXIES: 172.30.10.10 # Caddy
    volumes:
      - ./secrets:/secrets:ro
    networks: [backend]
    depends_on:
      postgres:
        condition: service_healthy
    logging: *logging

  postgres:
    image: postgres:17-alpine
    restart: unless-stopped
    environment:
      POSTGRES_DB: ${POSTGRES_DB:?set POSTGRES_DB in deploy/.env}
      POSTGRES_USER: ${POSTGRES_USER:?set POSTGRES_USER in deploy/.env}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD in deploy/.env}
    volumes:
      - pgdata:/var/lib/postgresql/data
    networks: [backend]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -h 127.0.0.1 -U $${POSTGRES_USER} -d $${POSTGRES_DB}"]
      interval: 5s
      timeout: 3s
      retries: 30
    logging: *logging

  backup:
    image: postgres:17-alpine
    restart: unless-stopped
    entrypoint: ["/bin/sh", "/backup.sh"]
    environment:
      PGHOST: postgres
      PGDATABASE: ${POSTGRES_DB}
      PGUSER: ${POSTGRES_USER}
      PGPASSWORD: ${POSTGRES_PASSWORD}
      BACKUP_KEEP_DAYS: ${BACKUP_KEEP_DAYS:-14}
    volumes:
      - ./backup.sh:/backup.sh:ro
      - ./backups:/backups
    networks: [backend]
    depends_on:
      postgres:
        condition: service_healthy
    logging: *logging

networks:
  backend:
    ipam:
      config:
        - subnet: 172.30.10.0/24

volumes:
  pgdata:
  caddy_data:
  caddy_config:
```

- [ ] **Step 2: `deploy/Caddyfile`**

<!-- file: deploy/Caddyfile -->
```text
# HTTPS for {$DOMAIN} with an automatically issued certificate; everything goes to the API.
# Caddy replaces any client-supplied X-Forwarded-For with the real client address.
{$DOMAIN} {
	encode zstd gzip
	header {
		Strict-Transport-Security "max-age=31536000"
		-Server
	}
	reverse_proxy api:8080
}
```

- [ ] **Step 3: `deploy/backup.sh`** (переводы строк LF обеспечивает `.gitattributes`)

<!-- file: deploy/backup.sh -->
```bash
#!/bin/sh
# Daily pg_dump into /backups (custom format, restore with pg_restore).
# Keeps BACKUP_KEEP_DAYS days. Connection settings come from PG* environment variables.
set -eu

keep="${BACKUP_KEEP_DAYS:-14}"

while true; do
  file="/backups/kachalka-$(date -u +%Y%m%d-%H%M%S).dump"
  if pg_dump --format=custom --file="$file.tmp"; then
    mv "$file.tmp" "$file"
    echo "backup written: $file"
  else
    rm -f "$file.tmp"
    echo "backup FAILED" >&2
  fi
  find /backups -name 'kachalka-*.dump' -mtime +"$keep" -delete
  sleep 86400
done
```

- [ ] **Step 4: `deploy/.env.example`**

<!-- file: deploy/.env.example -->
```bash
# Скопируйте в deploy/.env и заполните. Файл .env не коммитится.

# Домен API. A-запись должна указывать на сервер: Caddy сам выпустит HTTPS-сертификат.
DOMAIN=api.example.com

# Postgres. Пароль — только латинские буквы и цифры (он подставляется в URL),
# например вывод `openssl rand -hex 24`.
POSTGRES_DB=kachalka
POSTGRES_USER=kachalka
POSTGRES_PASSWORD=

# Строка APP_SECRET=... из вывода keygen (см. README). Смена секрета разлогинит всех.
APP_SECRET=

# Название приложения в SMS и письмах.
APP_NAME=Качалка
JWT_ISSUER=go-kachalka

# Почта: SMTP-сервер вашего провайдера (Яндекс 360, Unisender, Mailgun и т. п.).
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USERNAME=
SMTP_PASSWORD=
SMTP_FROM=noreply@example.com
# starttls (порт 587) или tls (порт 465).
SMTP_TLS=starttls

# SMS. Значение log в prod запрещено: сервер не стартует, пока не подключён
# реальный провайдер (README → «Подключение SMS-провайдера»).
SMS_PROVIDER=log
# Необязательно: хэш приложения для автозаполнения кода на Android (SMS Retriever API).
SMS_APP_HASH=

LOG_LEVEL=info

# Сколько дней хранить ежедневные бэкапы в deploy/backups.
BACKUP_KEEP_DAYS=14

# Остальные настройки (время жизни токенов, лимиты) описаны в README;
# значения по умолчанию подходят для запуска.
```

- [ ] **Step 5: Проверить синтаксис compose-файлов**

```bash
docker compose config --quiet
cd deploy
cp .env.example .env
```
В `deploy/.env` задать `DOMAIN=localhost` и любой `POSTGRES_PASSWORD` из латиницы и цифр (пустой пароль compose не примет: переменная объявлена как обязательная), затем:

```bash
docker compose config --quiet
```
Expected: обе проверки `config` без вывода и ошибок.

- [ ] **Step 6: Пробный запуск в prod-режиме — сервер обязан отказаться стартовать.** Из каталога `deploy`:

PowerShell:
```powershell
New-Item -ItemType Directory -Force secrets, backups | Out-Null
docker compose build api
docker compose run --rm --no-deps -v "${PWD}/secrets:/out" api keygen -jwt-key /out/jwt.pem
```
Git Bash:
```bash
mkdir -p secrets backups
docker compose build api
MSYS_NO_PATHCONV=1 docker compose run --rm --no-deps -v "$(pwd -W)/secrets:/out" api keygen -jwt-key /out/jwt.pem
```
Вставить напечатанную строку `APP_SECRET=...` в `deploy/.env`, затем:

```bash
docker compose up -d postgres api
docker compose logs api
```
Expected: `api` перезапускается с сообщением `config: SMS_PROVIDER=log is forbidden in prod: codes would end up in logs; add a real provider (see README)`.

- [ ] **Step 7: Пробный запуск всего стека в dev-режиме.** В `deploy/.env` добавить строку `APP_ENV=dev` и сделать `SMTP_HOST=` пустым, затем:

```bash
docker compose up -d
docker compose ps
curl -sk https://localhost/readyz
curl -sk -X POST https://localhost/v1/auth/register/start -H "Content-Type: application/json" -H "X-Forwarded-For: 6.6.6.6" -d "{\"phone\":\"+79990001122\"}"
curl -skI https://localhost/healthz
docker compose logs api
```
Expected: все четыре сервиса запущены, `api` — `healthy`; `{"status":"ready"}`; на регистрацию — `202`; в заголовках есть `Strict-Transport-Security` и `Cache-Control: no-store`, нет `Server`; в логе запроса `"ip"` — адрес шлюза Docker (например `172.30.10.1`), а **не** `6.6.6.6`; в `deploy/backups/` появился файл `kachalka-*.dump`.

- [ ] **Step 8: Бэкап и восстановление на настоящих данных**

```bash
docker compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc "select count(*) from verification_codes"'
docker compose exec -T backup sh -c 'pg_dump --format=custom --file=/backups/manual-test.dump'
curl -sk -X POST https://localhost/v1/auth/register/start -H "Content-Type: application/json" -d "{\"phone\":\"+79990003344\"}"
docker compose stop api
docker compose exec -T postgres sh -c 'pg_restore --clean --if-exists -U "$POSTGRES_USER" -d "$POSTGRES_DB"' < backups/manual-test.dump
docker compose start api
docker compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc "select count(*) from verification_codes"'
```
Expected: число кодов после восстановления равно числу до бэкапа (новый код, созданный после бэкапа, исчез). В PowerShell перенаправление `<` не работает — выполните этот шаг в Git Bash.

- [ ] **Step 9: Убрать следы пробного запуска**

```bash
docker compose down -v
```
Удалить `deploy/.env`, `deploy/secrets/`, `deploy/backups/` (они и так в `.gitignore`), вернуться в корень репозитория.

- [ ] **Step 10: Контрольная точка** — выдать владельцу команды:

```bash
git add deploy/compose.yaml deploy/Caddyfile deploy/backup.sh deploy/.env.example
git commit -m "deploy: production compose with Caddy HTTPS and daily backups"
```

---

### Task 5: Линтер и CI

**Files:**
- Create: `.golangci.yml`, `.github/workflows/ci.yml`

- [ ] **Step 1: `.golangci.yml`** — стандартный набор плюс bodyclose, errorlint, gosec, misspell, noctx, unconvert; импорты модуля группируются отдельно:

<!-- file: .golangci.yml -->
```yaml
version: "2"

linters:
  default: standard # errcheck, govet, ineffassign, staticcheck, unused
  enable:
    - bodyclose
    - errorlint
    - gosec
    - misspell
    - noctx
    - unconvert
  settings:
    gosec:
      excludes:
        - G115 # integer conversions: lengths and generations are bounded by construction
        - G304 # file paths come from operator configuration and CLI flags, not from requests
  exclusions:
    generated: lax
    rules:
      - path: _test\.go
        linters: [gosec, noctx, errcheck]

formatters:
  enable:
    - gofmt
    - goimports
  settings:
    goimports:
      local-prefixes:
        - github.com/AntiqueViolet/go_kachalka_backend
```

- [ ] **Step 2: Установить и запустить линтер**

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
golangci-lint run --build-tags e2e ./...
```
Expected: `0 issues.`

- [ ] **Step 3: Проверить уязвимости**

```bash
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```
Expected: код возврата 0 — уязвимостей, которые вызывает наш код, нет (сообщение об уязвимости в зависимости, которую код не вызывает, допустимо).

- [ ] **Step 4: `.github/workflows/ci.yml`**

<!-- file: .github/workflows/ci.yml -->
```yaml
name: ci

on:
  push:
    branches: [main]
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod

      - name: Generated code is up to date
        run: |
          go generate ./...
          docker run --rm -v "$PWD:/src" -w /src sqlc/sqlc:1.31.1 generate
          git diff --exit-code

      - name: Vet
        run: go vet ./... && go vet -tags e2e ./e2e/

      - name: Test (unit + Postgres via testcontainers)
        run: go test -race ./...

      - name: Lint
        uses: golangci/golangci-lint-action@v8
        with:
          version: v2.14.0

      - name: Vulnerabilities
        run: go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

  e2e:
    runs-on: ubuntu-latest
    needs: test
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
      - name: Start dev stack
        run: docker compose up --build -d --wait
      - name: Smoke test
        run: go test -tags e2e -count=1 ./e2e/
      - name: Logs on failure
        if: failure()
        run: docker compose logs
      - name: Stop
        if: always()
        run: docker compose down -v
```

- [ ] **Step 5: Проверить, что сгенерированный код актуален** (то же, что делает CI)

PowerShell:
```powershell
go generate ./...
docker run --rm -v "${PWD}:/src" -w /src sqlc/sqlc:1.31.1 generate
git status --short
```
Expected: `git status` не показывает изменённых файлов в `internal/store` и `internal/httpapi/oapi`.

- [ ] **Step 6: Контрольная точка** — выдать владельцу команды:

```bash
git add .golangci.yml .github
git commit -m "ci: lint, tests, govulncheck and e2e on GitHub Actions"
```

---

### Task 6: README

**Files:**
- Create: `README.md`

- [ ] **Step 1: `README.md`**

<!-- file: README.md -->
````markdown
# go_kachalka backend

Сервер авторизации для мобильного приложения-трекера тренировок:

- регистрация по номеру телефона с подтверждением SMS-кодом;
- вход по телефону и паролю;
- подтверждение email и сброс пароля кодом из письма;
- access-токены (JWT, 15 минут) и refresh-токены с ротацией (60 дней);
- профиль: смена email и пароля, выход на одном или всех устройствах, удаление аккаунта.

Приложение работает офлайн с локальной БД; сервер — единственный источник истины об учётных данных.
Контракт API — [`api/openapi.yaml`](api/openapi.yaml), дизайн — [`docs/superpowers/specs/`](docs/superpowers/specs/).

## Быстрый старт (локально)

Нужен только Docker.

```bash
docker compose up --build
```

| Что | Где |
|---|---|
| API | http://localhost:8080 |
| Письма (подтверждение email, сброс пароля) | http://localhost:8025 (Mailpit) |
| SMS-коды | `docker compose logs api` — строки `sms not sent (log provider)` |
| Postgres | `localhost:5432`, база/пользователь/пароль `kachalka` |

В dev-режиме лимиты на отправку кодов ослаблены (см. `docker-compose.yml`), ключ подписи JWT
временный — после перезапуска контейнера нужно войти заново.

Пример регистрации:

```bash
curl -s -X POST localhost:8080/v1/auth/register/start -H 'Content-Type: application/json' -d '{"phone":"+79991234567"}'
# → {"verification_id":"…","expires_in":600,"resend_after":5}; код смотрим в docker compose logs api
curl -s -X POST localhost:8080/v1/auth/register/verify -H 'Content-Type: application/json' -d '{"verification_id":"…","code":"123456"}'
curl -s -X POST localhost:8080/v1/auth/register -H 'Content-Type: application/json' -d '{"registration_token":"…","password":"пароль от 8 символов","email":"me@example.com"}'
```

## Для мобильного разработчика

- Контракт: [`api/openapi.yaml`](api/openapi.yaml). Клиент можно сгенерировать (OpenAPI Generator, Swift OpenAPI Generator, `openapi-generator-cli generate -g kotlin` и т. п.).
- Ошибки — RFC 9457 (`application/problem+json`). Ориентируйтесь на поле `code`, не на `title`. На `429` есть заголовок `Retry-After` (секунды).
- Refresh-токен храните только в Keychain / Android Keystore, не в локальной БД.
- Access-токен обновляйте по `access_expires_in` или по ответу `401 unauthorized`. При `invalid_refresh_token` и `refresh_token_reused` — на экран входа.
- `403 wrong_password` — неверный пароль в операции профиля; токены не трогать.
- Не шлите несколько refresh-запросов параллельно. Повтор того же refresh-токена в течение 30 секунд вернёт тот же новый токен (на случай потерянного ответа).
- Локальным записям выдавайте UUIDv7 на устройстве — при будущей синхронизации они загрузятся без конфликтов ID.
- Офлайн «вход» — это локальная разблокировка (PIN/биометрия) уже открытой сессии; первый вход требует сети.

## Разработка

Требования: Go 1.27 (нужная патч-версия скачается сама по `toolchain` в `go.mod`), Docker.

```bash
go test ./...                 # unit + интеграционные (Postgres поднимается через testcontainers)
go test -short ./...          # только тесты без Docker
docker compose up --build -d --wait
go test -tags e2e ./e2e/      # сквозной тест против запущенного стека
```

Линтер — [golangci-lint](https://golangci-lint.run) v2, конфигурация в `.golangci.yml`:

```bash
golangci-lint run --build-tags e2e ./...
```

Сгенерированный код лежит в репозитории; после правки `api/openapi.yaml` или SQL-запросов перегенерируйте его:

```bash
go generate ./...                                                    # internal/httpapi/oapi из api/openapi.yaml
docker run --rm -v "${PWD}:/src" -w /src sqlc/sqlc:1.31.1 generate   # internal/store из internal/store/queries
```

Новая миграция — файл `migrations/0000N_описание.sql` с секциями `-- +goose Up` / `-- +goose Down`;
миграции применяются автоматически при старте сервера.

### Структура

```
cmd/api          сервер (и `api healthcheck` для Docker)
cmd/keygen       генерация ключа JWT и APP_SECRET
internal/app     сборка зависимостей
internal/httpapi HTTP: обработчики, middleware, ошибки; oapi/ — сгенерированный код
internal/auth    регистрация, вход, refresh, выход, сброс пароля
internal/account профиль: /me, email, пароль, удаление
internal/codes   выдача и доставка кодов
internal/otp     коды подтверждения (генерация, хранение, проверка, лимиты)
internal/token   JWT и refresh-токены
internal/store   Postgres (sqlc), миграции, storetest — БД для тестов
internal/...     config, password, contact, notify, ratelimit, clock, apperr
migrations/      SQL-миграции
deploy/          комплект для сервера
e2e/             сквозной тест
```

## Конфигурация

Все настройки — переменные окружения. При ошибке в конфигурации сервер не стартует и пишет причину.

| Переменная | По умолчанию | Описание |
|---|---|---|
| `APP_ENV` | `dev` | `dev` или `prod`. В `prod` обязательны `APP_SECRET`, `JWT_PRIVATE_KEY_FILE`, SMTP с TLS и реальный SMS-провайдер |
| `APP_NAME` | `Качалка` | Название в SMS и письмах, до 20 символов |
| `HTTP_ADDR` | `:8080` | Адрес сервера |
| `DATABASE_URL` | — | Строка подключения Postgres (обязательна) |
| `APP_SECRET` | dev-значение | ≥ 32 байт в base64 (вывод `keygen`); из него выводятся ключи refresh-токенов и кодов. dev-значение в `prod` запрещено |
| `JWT_PRIVATE_KEY_FILE` | временный ключ (dev) | PEM-файл Ed25519 (создаёт `keygen`) |
| `JWT_ISSUER` | `go-kachalka` | `iss` в токенах |
| `ACCESS_TOKEN_TTL` | `15m` | Время жизни access-токена, не больше `1h` |
| `REFRESH_TOKEN_TTL` | `1440h` | 60 дней; продлевается при каждом обновлении |
| `REFRESH_GRACE` | `30s` | Окно повтора refresh-токена, от `0` до `5m` |
| `SMS_PROVIDER` | `log` | `log` пишет SMS в лог; в `prod` запрещён |
| `SMS_APP_HASH` | — | Хэш приложения для Android SMS Retriever (11 символов) |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM` | порт `587` | Почта. Пустой `SMTP_HOST` в dev — письма пишутся в лог |
| `SMTP_TLS` | `starttls` | `starttls`, `tls` (порт 465) или `none` (в `prod` запрещён) |
| `TRUSTED_PROXIES` | — | IP/CIDR прокси через запятую, которым верим в `X-Forwarded-For` (`/0` запрещён) |
| `PASSWORD_HASH_CONCURRENCY` | `4` | Сколько паролей хэшируется одновременно (каждый ~19 МБ памяти), от 1 до 64 |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `RATE_LOGIN_PER_IP_PER_MINUTE` | `20` | Попыток входа с одного IP в минуту |
| `RATE_LOGIN_FAILURES_PER_PHONE` / `RATE_LOGIN_FAILURES_WINDOW` | `5` / `15m` | Неудачных входов на номер за окно (окно не короче `1m`) |
| `RATE_CODE_COOLDOWN` | `60s` | Пауза между кодами на один адрес (`0` в `prod` запрещён) |
| `RATE_CODES_PER_TARGET_PER_HOUR` | `5` | Кодов на один номер/email в час |
| `RATE_CODES_PER_IP_PER_HOUR` | `20` | Запросов кодов с одного IP в час |
| `RATE_REFRESH_PER_IP_PER_MINUTE` | `60` | Обновлений токенов с одного IP в минуту |

## Развёртывание на сервере

Комплект — каталог `deploy/`: Caddy (HTTPS с автоматическим сертификатом), API, Postgres и ежедневный бэкап.

**Важно:** в `prod` сервер не стартует с `SMS_PROVIDER=log` (иначе коды попадали бы в логи).
Сначала подключите SMS-провайдера — см. следующий раздел.

### Требования

- Linux-сервер с Docker Engine и плагином `docker compose`, от 1 ГБ RAM;
- домен, A-запись которого указывает на сервер;
- открытые порты 80 и 443;
- SMTP-доступ у почтового провайдера.

### Первый запуск

```bash
git clone https://github.com/AntiqueViolet/go_kachalka_backend.git
cd go_kachalka_backend/deploy
cp .env.example .env
nano .env                              # DOMAIN, POSTGRES_PASSWORD, SMTP_*, SMS-провайдер
mkdir -p secrets backups
sudo chown 65532:65532 secrets         # контейнер API работает от uid 65532
docker compose build api
docker compose run --rm --no-deps -v "$PWD/secrets:/out" api keygen -jwt-key /out/jwt.pem
```

`keygen` создаст `secrets/jwt.pem` и напечатает строку `APP_SECRET=…` — вставьте её в `.env`. Затем:

```bash
docker compose up -d
docker compose ps                      # все сервисы running/healthy
curl https://ВАШ_ДОМЕН/readyz          # {"status":"ready"}
docker compose logs -f api
```

Ключ `secrets/jwt.pem` и `APP_SECRET` храните в надёжном месте: их потеря или замена разлогинит всех пользователей.

### Обновление

```bash
cd go_kachalka_backend && git pull
cd deploy && docker compose up -d --build
```

Миграции БД применятся при старте. При обновлении версии Go меняйте `toolchain` в `go.mod` и тег образа в `Dockerfile`.

### Бэкапы

Сервис `backup` раз в сутки делает `pg_dump` в `deploy/backups/` и хранит `BACKUP_KEEP_DAYS` дней (по умолчанию 14).
Копируйте бэкапы за пределы сервера. Внеочередной бэкап:

```bash
docker compose exec backup sh -c 'pg_dump --format=custom --file=/backups/manual-$(date -u +%Y%m%d-%H%M%S).dump'
```

Восстановление:

```bash
docker compose stop api
docker compose exec -T postgres sh -c 'pg_restore --clean --if-exists -U "$POSTGRES_USER" -d "$POSTGRES_DB"' < backups/ИМЯ_ФАЙЛА.dump
docker compose start api
```

### Пробный запуск комплекта на своей машине

Можно проверить весь prod-стек локально, не подключая SMS и почту: в `deploy/.env` укажите `DOMAIN=localhost`,
любой `POSTGRES_PASSWORD`, добавьте строку `APP_ENV=dev` и оставьте `SMTP_HOST` пустым. Caddy выпустит
локальный сертификат: `curl -k https://localhost/readyz`. Для настоящего сервера `APP_ENV=dev` не используйте.

## Подключение SMS-провайдера

1. Реализуйте интерфейс `notify.SMSSender` в новом файле `internal/notify/<провайдер>.go`:

   ```go
   type SMSSender interface {
       SendSMS(ctx context.Context, phone, text string) error // phone в формате E.164: +79991234567
   }
   ```

   Пример для провайдера с HTTP API (сверьте адрес и параметры с документацией вашего провайдера):

   ```go
   type SMSRu struct {
       APIID  string
       Client *http.Client
   }

   func (s *SMSRu) SendSMS(ctx context.Context, phone, text string) error {
       form := url.Values{"api_id": {s.APIID}, "to": {strings.TrimPrefix(phone, "+")}, "msg": {text}, "json": {"1"}}
       req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://sms.ru/sms/send", strings.NewReader(form.Encode()))
       if err != nil {
           return err
       }
       req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
       resp, err := s.Client.Do(req)
       if err != nil {
           return err
       }
       defer resp.Body.Close()
       if resp.StatusCode != http.StatusOK {
           return fmt.Errorf("sms.ru: status %d", resp.StatusCode)
       }
       return nil // разберите тело ответа: провайдеры часто отвечают 200 и сообщают ошибку в JSON
   }
   ```

2. В `internal/config/config.go` добавьте настройки провайдера (например, `SMSRU_API_ID`) и разрешите новое
   значение `SMS_PROVIDER` в `validate()`.
3. В `internal/app/app.go` (функция `senders`) создавайте ваш sender, когда `SMS_PROVIDER` равен его имени.
4. Добавьте переменные в `deploy/.env.example` и `SMS_PROVIDER=<имя>` в `deploy/.env`.

Ошибка отправки превращается в `503 service_unavailable`, пользователь может повторить запрос.
Лимиты на отправку (на номер и на IP) уже защищают от «SMS-накрутки», но включите и лимиты в кабинете провайдера.

## Безопасность (кратко)

- Пароли — argon2id (параметры OWASP), число одновременных хэширований ограничено.
- Коды — 6 цифр, в БД только HMAC, 5 попыток, 10 минут, лимиты на отправку.
- Refresh-токены не хранятся вовсе: сервер вычисляет их из сессии и секретного ключа; повторное использование старого токена отзывает сессию.
- Одинаковый ответ и время ответа для неизвестного номера и неверного пароля; `password/forgot` не раскрывает, есть ли email.
- `X-Forwarded-For` учитывается только от Caddy; тело запроса ≤ 64 КиБ; таймауты сервера; `Cache-Control: no-store`.
- Пароли, коды (кроме dev-лога SMS), токены и секреты не пишутся в логи.

## Частые проблемы

- **Сервер в prod не стартует: `SMS_PROVIDER=log is forbidden in prod`** — подключите SMS-провайдера (раздел выше).
- **`port is already allocated`** — порт 8080/5432/8025 занят другим процессом; остановите его или поменяйте порт в `docker-compose.yml`.
- **`429 Too Many Requests` при скачивании образов** — лимит Docker Hub для анонимных пользователей; выполните `docker login` или подождите.
- **Подсеть `172.30.10.0/24` уже занята на сервере** — поменяйте её в `deploy/compose.yaml` (вместе с адресом Caddy и `TRUSTED_PROXIES`).
````

- [ ] **Step 2: Сверить README с фактом** — пройти по разделам «Быстрый старт» и «Разработка»: каждая команда из них выполнялась в Tasks 2, 3 и 5 этой части.

- [ ] **Step 3: Контрольная точка** — выдать владельцу команды:

```bash
git add README.md
git commit -m "docs: README with local run, deploy and SMS provider guide"
```

---

### Task 7: Финальная проверка и передача

- [ ] **Step 1: Полный прогон**

```bash
gofmt -l ./cmd ./internal ./migrations ./e2e
go vet ./...
go test -count=1 ./...
golangci-lint run --build-tags e2e ./...
docker compose up --build -d --wait
go test -tags e2e -count=1 ./e2e/
docker compose down -v
```
Expected: `gofmt` пусто; все пакеты `ok`; `0 issues.`; `PASS` для e2e.

- [ ] **Step 2: Отчёт владельцу** — что сделано, где что лежит, и команды для публикации:

```bash
git status
git push -u origin main
```
После push проверить в GitHub, что workflow `ci` прошёл (обе задачи: `test` и `e2e`).

- [ ] **Step 3: Напомнить о рисках из спецификации:** без реального SMS-провайдера prod не стартует (README → «Подключение SMS-провайдера»); `deploy/secrets/jwt.pem` и `APP_SECRET` нужно хранить отдельно от сервера.
