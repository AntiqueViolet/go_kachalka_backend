# go_kachalka backend — часть 3: комплект поставки. План реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Всё, что нужно для сдачи: Docker-образ, локальный стек одной командой, сквозной тест, prod-комплект для своего сервера (HTTPS, бэкапы), линтер и CI, README.

**Architecture:** Многоступенчатый Dockerfile → distroless-образ с `api` и `keygen`. Dev: `docker-compose.yml` (Postgres, Mailpit, API). Prod: `deploy/compose.yaml` (Caddy с автоматическим HTTPS → API → Postgres, плюс ежедневный `pg_dump`). CI на GitHub Actions: актуальность сгенерированного кода, vet, тесты с `-race`, golangci-lint, govulncheck, e2e против compose.

**Tech Stack:** Docker / Compose v2, `golang:1.27.4`, `gcr.io/distroless/static-debian13:nonroot`, `postgres:17-alpine`, `axllent/mailpit:v1.32`, `caddy:2.11`, golangci-lint v2.14.0, govulncheck v1.9.0.

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

Сборка на `golang:1.27.4` (совпадает с toolchain в `go.mod`), итоговый образ — distroless без shell, uid 65532. `HEALTHCHECK` вызывает `api healthcheck`. `CMD` (а не `ENTRYPOINT`) позволяет запускать в том же образе `keygen`. Кэш модулей и сборки — через cache mounts BuildKit; `.dockerignore` не пускает в контекст сборки секреты и данные (`.env`, ключи, дампы) ни на каком уровне вложенности.

**Files:**
- Create: `Dockerfile`, `.dockerignore`

- [ ] **Step 1: `Dockerfile`**

<!-- file: Dockerfile -->
```dockerfile
# syntax=docker/dockerfile:1

FROM golang:1.27.4 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
# Cache mounts keep module and build caches between builds; they never end up in the image.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
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
# Not needed to build the binaries.
.git
.github
docs
deploy
e2e
**/*_test.go
*.md
Dockerfile
docker-compose.yml
.golangci.yml
# Secrets and data must never reach the build context, wherever they lie.
**/.env
**/.env.*
**/*.pem
**/*.key
**/*.dump
**/secrets
**/backups
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

Healthcheck Postgres идёт по TCP (`-h 127.0.0.1`): через unix-сокет база отвечает ещё во время initdb, и API без этого падал бы на старте. Лимиты на коды ослаблены, чтобы мобильное приложение можно было проверять многократно. Postgres и Mailpit слушают только localhost (пароль БД публичный), API — все интерфейсы, чтобы приложение на телефоне в той же Wi-Fi-сети до него дотягивалось.

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
      - "127.0.0.1:5432:5432" # localhost only: the dev password is public
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
      - "127.0.0.1:8025:8025" # web UI and REST API (shows the codes)
      - "127.0.0.1:1025:1025" # SMTP

  api:
    build: .
    # The server drains for up to 20s on SIGTERM (shutdownTimeout); Docker's default 10s would SIGKILL it.
    stop_grace_period: 25s
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
      - "8080:8080" # all interfaces: a phone on the same Wi-Fi can reach the API
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

Тест регистрирует случайный номер и email, берёт SMS-код из `docker compose logs api` (JSON-строки лога), код из письма — из REST API Mailpit, и проходит подтверждение email, вход, refresh (токен ротируется), `/v1/me` и выход со всех устройств — после него refresh-токены не принимаются.

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
	client     = &http.Client{Timeout: 10 * time.Second}
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
	access, refresh := tokens(t, body)

	st, body = call(t, "POST", "/v1/me/email/verify", access, map[string]any{"code": emailCode(t, email)})
	want(t, http.StatusOK, st, body)
	if body["email_verified"] != true {
		t.Fatalf("email not verified: %v", body)
	}

	st, body = call(t, "POST", "/v1/auth/login", "", map[string]any{"phone": phone, "password": pass})
	want(t, http.StatusOK, st, body)
	_, loginRefresh := tokens(t, body)

	st, body = call(t, "POST", "/v1/auth/refresh", "", map[string]any{"refresh_token": refresh})
	want(t, http.StatusOK, st, body)
	access, rotated := tokens(t, body)
	if rotated == refresh {
		t.Fatal("refresh token was not rotated")
	}
	st, body = call(t, "GET", "/v1/me", access, nil)
	want(t, http.StatusOK, st, body)
	if body["phone"] != phone {
		t.Fatalf("me: %v", body)
	}

	st, body = call(t, "POST", "/v1/auth/logout", access, map[string]any{"all_devices": true})
	want(t, http.StatusNoContent, st, body)
	for _, rt := range []string{rotated, loginRefresh} {
		st, body = call(t, "POST", "/v1/auth/refresh", "", map[string]any{"refresh_token": rt})
		if st != http.StatusUnauthorized {
			t.Fatalf("refresh after logout from all devices: %d %v", st, body)
		}
	}

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
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v (is `docker compose up` running?)", method, path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// tokens extracts the token pair from an auth response.
func tokens(t *testing.T, body map[string]any) (access, refresh string) {
	t.Helper()
	pair, _ := body["tokens"].(map[string]any)
	access, _ = pair["access_token"].(string)
	refresh, _ = pair["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("no token pair in %v", body)
	}
	return access, refresh
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
	return eventually(t, "SMS code for "+phone, func() string {
		out, err := exec.Command("docker", "compose", "-f", "../docker-compose.yml", "logs", "--no-color", "--no-log-prefix", "api").Output()
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
	return eventually(t, "email code for "+addr, func() string {
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
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
}

func eventually(t *testing.T, what string, f func() string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if s := strings.TrimSpace(f()); s != "" {
			return s
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("%s did not appear within 15 s", what)
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

Caddy получает фиксированный IP в сети `backend`, и API доверяет `X-Forwarded-For` только ему; сам Caddy заменяет присланный клиентом `X-Forwarded-For` реальным адресом. API и Postgres наружу не публикуются; контейнер API только для чтения, без capabilities. Caddy режет тела больше 128 КБ и добавляет `HSTS` и `nosniff`. У всех контейнеров ротация логов. `backup` раз в сутки делает `pg_dump`; старые дампы удаляются только после удачного и не меньше трёх последних остаются всегда, а healthcheck `backup` становится `unhealthy`, если свежего дампа нет больше 26 часов. Postgres получает 60 секунд на штатную остановку.

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
    # The server drains for up to 20s on SIGTERM (shutdownTimeout); Docker's default 10s would SIGKILL it.
    stop_grace_period: 25s
    restart: unless-stopped
    # The server writes nothing to disk and needs no privileges.
    read_only: true
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
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
    stop_grace_period: 60s # a clean shutdown avoids crash recovery on the next start
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
    healthcheck:
      # Unhealthy (visible in `docker compose ps`) when the newest dump is older than 26 h.
      test: ["CMD-SHELL", "find /backups -name 'kachalka-*.dump' -mmin -1560 | grep -q ."]
      interval: 10m
      timeout: 10s
      start_period: 10m
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
	# The API accepts at most 64 KiB; larger bodies are cut off here already.
	request_body {
		max_size 128KB
	}
	header {
		Strict-Transport-Security "max-age=31536000"
		X-Content-Type-Options "nosniff"
		-Server
	}
	reverse_proxy api:8080
}
```

- [ ] **Step 3: `deploy/backup.sh`** (переводы строк LF обеспечивает `.gitattributes`)

<!-- file: deploy/backup.sh -->
```bash
#!/bin/sh
# Daily pg_dump into /backups (custom format, restore with pg_restore). Connection settings come
# from PG* environment variables. Old dumps are pruned only after a successful dump and the
# newest ones always stay, so failing backups never eat the good ones. A failure is logged and
# turns the container unhealthy (compose healthcheck: the newest dump is older than 26 h).
set -eu

keep_days="${BACKUP_KEEP_DAYS:-14}"
keep_min=3

while true; do
  file="/backups/kachalka-$(date -u +%Y%m%d-%H%M%S).dump"
  if pg_dump --format=custom --file="$file.tmp"; then
    mv "$file.tmp" "$file"
    echo "backup written: $file"
    # Names sort by time: skip the keep_min newest, delete the rest once older than keep_days.
    ls -1 /backups/kachalka-*.dump | sort -r | tail -n +$((keep_min + 1)) | while read -r old; do
      if [ -n "$(find "$old" -mtime +"$keep_days")" ]; then
        rm -f "$old" && echo "backup pruned: $old"
      fi
    done
  else
    rm -f "$file.tmp"
    echo "backup FAILED" >&2
  fi
  find /backups -name 'kachalka-*.dump.tmp' -mmin +60 -delete
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
Expected: все четыре сервиса запущены, `api` — `healthy`; `{"status":"ready"}`; на регистрацию — `202`; в заголовках есть `Strict-Transport-Security`, `X-Content-Type-Options: nosniff` и `Cache-Control: no-store`, нет `Server`; в логе запроса `"ip"` — адрес шлюза Docker (например `172.30.10.1`), а **не** `6.6.6.6`; в `deploy/backups/` появился файл `kachalka-*.dump`.

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

CI только читает репозиторий (`permissions: contents: read`), отменяет устаревшие прогоны PR и ограничен по времени. Проверка сгенерированного кода ловит и новые незакоммиченные файлы, и неаккуратный `go.mod`. Линтер проверяет и e2e-тест (`build-tags` в `.golangci.yml`).

**Files:**
- Create: `.golangci.yml`, `.github/workflows/ci.yml`

- [ ] **Step 1: `.golangci.yml`** — стандартный набор плюс bodyclose, errorlint, gosec, misspell, noctx, unconvert; импорты модуля группируются отдельно:

<!-- file: .golangci.yml -->
```yaml
version: "2"

run:
  build-tags: [e2e] # lint the end-to-end test too

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

# The jobs only read the repository.
permissions:
  contents: read

# A newer push to the same branch or PR supersedes the running checks.
concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: ${{ github.event_name == 'pull_request' }}

jobs:
  test:
    runs-on: ubuntu-latest
    timeout-minutes: 20
    steps:
      - uses: actions/checkout@v5
        with:
          persist-credentials: false
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod

      - name: Generated code and go.mod are up to date
        run: |
          go generate ./...
          docker run --rm -v "$PWD:/src" -w /src sqlc/sqlc:1.31.1 generate
          go mod tidy -diff
          # porcelain also lists new files that a generator created but nobody committed
          test -z "$(git status --porcelain)" || { git status --short; git diff; exit 1; }

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
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v5
        with:
          persist-credentials: false
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

> Репозиторий на GitHub приватный: доступ (collaborator, deploy key или токен) выдаёт владелец.

## Быстрый старт (локально)

Нужен только Docker. Команды `curl`/`docker` в этом README рассчитаны на bash (Linux, macOS, WSL, Git Bash).
В PowerShell `curl` — это псевдоним `Invoke-WebRequest`; используйте `Invoke-RestMethod` (пример ниже)
или `curl.exe`, либо Git Bash.

```bash
git clone https://github.com/AntiqueViolet/go_kachalka_backend.git   # нужен доступ к приватному репозиторию
cd go_kachalka_backend
docker compose up --build
```

| Что | Где |
|---|---|
| API | http://localhost:8080 (слушает на всех интерфейсах — с телефона по Wi-Fi: `http://<IP компьютера>:8080`) |
| Письма (подтверждение email, сброс пароля) | http://localhost:8025 (Mailpit) |
| SMS-коды | `docker compose logs api` — строки `sms not sent (log provider)` |
| Postgres | `localhost:5432`, база/пользователь/пароль `kachalka` |

В dev-режиме лимиты на отправку кодов ослаблены (см. `docker-compose.yml`), ключ подписи JWT временный:
после перезапуска контейнера выданные access-токены перестают приниматься (клиент обновит их через refresh),
а refresh-токены остаются рабочими. Мобильное приложение можно разрабатывать на этом стеке уже сейчас —
SMS-провайдер для этого не нужен: код приходит в `docker compose logs api`, письма — в Mailpit.

Пример регистрации (bash):

```bash
curl -s -X POST localhost:8080/v1/auth/register/start -H 'Content-Type: application/json' -d '{"phone":"+79991234567"}'
# → {"verification_id":"…","expires_in":600,"resend_after":5}; код смотрим в docker compose logs api
curl -s -X POST localhost:8080/v1/auth/register/verify -H 'Content-Type: application/json' -d '{"verification_id":"…","code":"123456"}'
curl -s -X POST localhost:8080/v1/auth/register -H 'Content-Type: application/json' -d '{"registration_token":"…","password":"пароль от 8 символов","email":"me@example.com"}'
```

То же в PowerShell (ошибки 4xx `Invoke-RestMethod` выбрасывает как исключение; тело ответа — в `$_.ErrorDetails.Message`):

```powershell
$body = @{ phone = '+79991234567' } | ConvertTo-Json
Invoke-RestMethod -Method Post -Uri http://localhost:8080/v1/auth/register/start -ContentType 'application/json' -Body $body
```

## Для мобильного разработчика

Контракт — [`api/openapi.yaml`](api/openapi.yaml); клиент можно сгенерировать (OpenAPI Generator, Swift OpenAPI Generator,
`openapi-generator-cli generate -g kotlin` и т. п.). Ниже — то, чего в контракте не видно.
Запрос на сервере ограничен 12 секундами — таймаут клиента ставьте 15–20 с.

### Сценарии

- **Регистрация.** `POST /v1/auth/register/start` (телефон) → SMS с кодом, в ответе `verification_id` →
  `POST /v1/auth/register/verify` (`verification_id` + `code`) → `registration_token` (живёт ~15 минут, `expires_in`) →
  `POST /v1/auth/register` (`registration_token`, `password`, `email`, `device_name`) → пользователь и токены.
  Email при регистрации обязателен, но после неё **не подтверждён**.
- **Вход.** `POST /v1/auth/login` (`phone`, `password`, `device_name`). Неизвестный номер и неверный пароль неразличимы:
  `401 invalid_credentials`.
- **Подтверждение email.** После регистрации на email уходит код: `POST /v1/me/email/verify` (`code`). Повторная отправка —
  `POST /v1/me/email/resend`; сменить адрес — `PUT /v1/me/email` (нужен пароль; новый адрес снова не подтверждён, на него уходит код).
  Вход и работа с профилем не зависят от подтверждения, но **сброс пароля работает только для подтверждённого email**.
- **Сброс пароля.** `POST /v1/auth/password/forgot` (`email`) — ответ `202` одинаков, есть такой email или нет →
  `POST /v1/auth/password/reset` (`email`, `code`, `new_password`) → `204`. Все сессии отзываются, нужно войти заново.
- **Профиль.** `GET /v1/me`, `PUT /v1/me/password` (остальные сессии отзываются, текущая остаётся), `DELETE /v1/me` (с паролем; данные удаляются полностью).

### Форматы

- **Телефон:** любой формат, цифры и разделители (пробел, `-`, `(`, `)`, `.`), до 32 символов. Без `+` номер считается российским
  (`8 (999) 123-45-67` и `9991234567` → `+79991234567`); номера других стран — только с `+` и кодом страны. Принимаются только номера,
  способные получить SMS (мобильные); городские российские номера отклоняются как `invalid_phone`. В ответах телефон всегда в E.164.
- **Пароль:** 8–128 символов (считаются символы Unicode, не байты). Email: только ASCII, до 254 символов; сервер приводит к нижнему регистру.
- **Код:** 6 цифр, живёт 10 минут, 5 попыток на код. Новый код отменяет предыдущий.
- **`device_name`:** до 100 символов, необязателен (по умолчанию `unknown`) — показывается в списке сессий.

### Токены

- Access — JWT, 15 минут (`access_expires_in`). Refresh — 60 дней, **скользящий**: каждое обновление продлевает срок.
- **Ротация:** каждый `POST /v1/auth/refresh` выдаёт новую пару, старый refresh-токен перестаёт работать. Новый refresh-токен
  сохраняйте **до** использования новой пары. Не шлите несколько refresh-запросов параллельно.
- Если ответ потерялся, повторите запрос с тем же (предыдущим) refresh-токеном в течение 30 секунд — вернётся та же новая пара.
  Позже повторное использование старого токена считается кражей: сессия отзывается, ответ `401 refresh_token_reused` → экран входа.
- Refresh-токен храните только в Keychain / Android Keystore, не в локальной БД. Обновляйте токены по `access_expires_in` или по `401 unauthorized`.
- Не больше 10 сессий (устройств) на пользователя: при 11-м входе отзывается сессия, которой дольше всех не пользовались (там будет `invalid_refresh_token`).
- **Выход:** `POST /v1/auth/logout` (`{"all_devices": true}` — на всех устройствах). Уже выданные access-токены живут до 15 минут,
  поэтому при выходе без сети достаточно удалить токены локально (и отозвать сессию позже, когда сеть появится).

### Ошибки

Формат — RFC 9457 (`application/problem+json`). Ориентируйтесь на `code`, не на `title`; неизвестный `code` обрабатывайте по HTTP-статусу
(список кодов может пополняться). Поля: `status`, `code`, `title`, а также `errors[]` (`field` + `code`: `invalid_phone`, `invalid_email`,
`too_short`, `too_long`, `required`) при `validation_failed` и `attempts_left` при `invalid_code`.

| `code` | HTTP | Что делать клиенту |
|---|---|---|
| `bad_request` | 400 | Некорректный JSON или тело — это баг клиента |
| `unauthorized` | 401 | Access-токен просрочен или недействителен → refresh, затем повторить запрос |
| `invalid_credentials` | 401 | Показать «неверный телефон или пароль» |
| `invalid_registration_token` | 401 | Регистрационный токен просрочен (~15 мин) → начать регистрацию заново |
| `invalid_refresh_token` | 401 | Сессия закончилась (просрочена, отозвана, вытеснена) → экран входа |
| `refresh_token_reused` | 401 | Токен использован повторно, сессия отозвана → экран входа |
| `wrong_password` | 403 | Неверный пароль в операции профиля; токены не трогать |
| `phone_taken` | 409 | Номер уже зарегистрирован → предложить вход |
| `email_taken` | 409 | Email уже подтверждён у другого аккаунта |
| `email_already_verified` | 409 | Email уже подтверждён — просто обновить профиль |
| `code_expired` | 410 | Код просрочен, исчерпаны попытки или не найден → запросить новый |
| `validation_failed` | 422 | Подсветить поля из `errors[]` |
| `invalid_code` | 422 | Неверный код; `attempts_left` — сколько попыток осталось. В сбросе пароля **любая** ошибка кода — это `invalid_code` без `attempts_left` |
| `rate_limited` | 429 | Подождать `Retry-After` секунд; не повторять запрос автоматически |
| `service_unavailable` | 503 | SMS/письмо не ушло или сервер перегружен — повторить позже, код при этом не потрачен |
| `internal_error` | 500 | Ошибка сервера — повторить позже |

Таймеры в UI берите из ответов: `resend_after` (когда можно запросить код ещё раз) и `expires_in` (сколько секунд живёт код / токен).

### Лимиты, которые встретят пользователи

- Вход: 5 неудачных попыток за 15 минут на номер (верный пароль не считается), плюс 20 попыток в минуту с одного IP.
- Коды: не чаще чем раз в 60 секунд на один номер/адрес, не больше 5 в час на адрес и 20 в час с одного IP.
- Неверный текущий пароль в `PUT /v1/me/password`, `PUT /v1/me/email`, `DELETE /v1/me` ограничен так же, как неудачные входы (5 за 15 минут).
- Refresh: 60 запросов в минуту с одного IP.

### Подключение в разработке

- Android-эмулятор: `http://10.0.2.2:8080`. Физическое устройство в той же Wi-Fi-сети: `http://<IP компьютера в LAN>:8080`
  (порт 8080 dev-стека открыт на все интерфейсы; в Windows брандмауэр может потребовать разрешить входящие).
- По `http` приложение работает с исключениями: Android — `android:usesCleartextTraffic="true"` или network security config для dev-хоста;
  iOS — `NSAllowsLocalNetworking` (для `localhost`/локальной сети) в `Info.plist`. Для `10.0.2.2` и LAN-IP на Android нужен именно cleartext.
- На сервере только HTTPS; запросы по `http` получают `308` на `https`. В релизной сборке cleartext-исключения не оставляйте.
- Локальным записям выдавайте UUIDv7 на устройстве — при будущей синхронизации они загрузятся без конфликтов ID.
- Офлайн «вход» — это локальная разблокировка (PIN/биометрия) уже открытой сессии; первый вход требует сети.

## Разработка

Требования: Go 1.27 (нужная патч-версия скачается сама по `toolchain` в `go.mod`), Docker.
Версия Go указана в двух местах — `toolchain` в `go.mod` и тег `golang:` в `Dockerfile`; при обновлении меняйте оба.

```bash
go test ./...                 # unit + интеграционные (Postgres поднимается через testcontainers)
go test -short ./...          # только тесты без Docker
docker compose up --build -d --wait
go test -tags e2e ./e2e/      # сквозной тест против запущенного dev-стека
```

Сквозной тест запускайте из корня репозитория: ему нужен docker CLI (он читает SMS-коды из `docker compose logs api`), а в dev-БД
он создаёт тестовых пользователей.

Линтер — [golangci-lint](https://golangci-lint.run) v2, конфигурация в `.golangci.yml`:

```bash
golangci-lint run --build-tags e2e ./...
```

Сгенерированный код лежит в репозитории; после правки `api/openapi.yaml` или SQL-запросов перегенерируйте его:

```bash
go generate ./...                                                    # internal/httpapi/oapi из api/openapi.yaml
docker run --rm -v "$PWD:/src" -w /src sqlc/sqlc:1.31.1 generate     # internal/store из internal/store/queries (Linux, macOS, WSL)
# Git Bash на Windows: без отключения конвертации путей docker получит «C:/Program Files/Git/src»
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src sqlc/sqlc:1.31.1 generate
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

Все настройки — переменные окружения. При ошибке в конфигурации сервер не стартует и пишет причину
(первые строки `docker compose logs api` называют проблемную переменную).

| Переменная | По умолчанию | Описание |
|---|---|---|
| `APP_ENV` | `dev` | `dev` или `prod`. В `prod` обязательны `APP_SECRET`, `JWT_PRIVATE_KEY_FILE`, `SMTP_HOST` и `SMTP_FROM`, TLS для SMTP и реальный SMS-провайдер |
| `APP_NAME` | `Качалка` | Название в SMS и письмах, до 20 символов |
| `HTTP_ADDR` | `:8080` | Адрес сервера |
| `DATABASE_URL` | — | Строка подключения Postgres (обязательна) |
| `APP_SECRET` | dev-значение | ≥ 32 байт в base64 (вывод `keygen`); из него выводятся ключи refresh-токенов и кодов. dev-значение в `prod` запрещено |
| `JWT_PRIVATE_KEY_FILE` | временный ключ (dev) | PEM-файл Ed25519 (создаёт `keygen`) |
| `JWT_ISSUER` | `go-kachalka` | `iss` в токенах |
| `ACCESS_TOKEN_TTL` | `15m` | Время жизни access-токена, не больше `1h` |
| `REFRESH_TOKEN_TTL` | `1440h` | 60 дней; продлевается при каждом обновлении; не меньше `ACCESS_TOKEN_TTL` |
| `REFRESH_GRACE` | `30s` | Окно повтора refresh-токена, от `0` до `5m` |
| `SMS_PROVIDER` | `log` | Сейчас принимается только `log` (пишет SMS в лог; в `prod` запрещён). Другие значения появятся после [подключения провайдера](#шаг-0-подключение-sms-провайдера) |
| `SMS_APP_HASH` | — | Хэш приложения для Android SMS Retriever (11 символов) |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM` | `SMTP_PORT` = `587`, остальные — пусто | Почта. Пустой `SMTP_HOST` в dev — письма пишутся в лог. Если `SMTP_HOST` задан, `SMTP_FROM` должен быть корректным адресом, а `SMTP_PORT` — 1–65535. В `prod` `SMTP_HOST` и `SMTP_FROM` обязательны |
| `SMTP_TLS` | `starttls` | `starttls`, `tls` (порт 465) или `none` (в `prod` запрещён) |
| `TRUSTED_PROXIES` | — | IP/CIDR прокси через запятую, которым верим в `X-Forwarded-For` (`/0` запрещён) |
| `PASSWORD_HASH_CONCURRENCY` | `4` | Сколько паролей хэшируется одновременно (каждый ~19 МБ памяти), от 1 до 64 |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `RATE_LOGIN_PER_IP_PER_MINUTE` | `20` | Попыток входа с одного IP в минуту (≥ 1) |
| `RATE_LOGIN_FAILURES_PER_PHONE` / `RATE_LOGIN_FAILURES_WINDOW` | `5` / `15m` | Неудачных входов на номер за окно (≥ 1; окно не короче `1m`) |
| `RATE_CODE_COOLDOWN` | `60s` | Пауза между кодами на один адрес (≥ 0; `0` в `prod` запрещён) |
| `RATE_CODES_PER_TARGET_PER_HOUR` | `5` | Кодов на один номер/email в час (≥ 1) |
| `RATE_CODES_PER_IP_PER_HOUR` | `20` | Запросов кодов с одного IP в час (≥ 1) |
| `RATE_REFRESH_PER_IP_PER_MINUTE` | `60` | Обновлений токенов с одного IP в минуту (≥ 1) |

### Файл `deploy/.env`

Сервер читает его через `env_file`, а `docker compose` ещё и подставляет в `compose.yaml`. Типичные ошибки:

- `APP_SECRET=` в шаблоне уже есть (пустой) — **замените** эту строку, не добавляйте вторую.
- `POSTGRES_PASSWORD` — только латинские буквы и цифры (пароль подставляется в URL), например `openssl rand -hex 24`.
- Знак `$` в значениях удваивайте (`$$`): иначе compose примет его за подстановку.
- Переменные, заданные в `compose.yaml` (`APP_ENV`, `DATABASE_URL`, `JWT_PRIVATE_KEY_FILE`, `TRUSTED_PROXIES`), перекрывают значения из `.env`.
  Исключение — `APP_ENV`: он берётся из `.env`, если задан там (`${APP_ENV:-prod}`).
- Ограничьте доступ: `chmod 600 .env`.
- После правки `.env` применяйте изменения командой `docker compose up -d` — `docker compose restart` файл заново не читает.

## Развёртывание на сервере

**В репозитории есть только SMS-провайдер `log` (пишет коды в лог), а в `prod` он запрещён — поэтому на настоящем сервере
сервер не стартует, пока не подключён реальный провайдер. Сделайте это первым: [шаг 0](#шаг-0-подключение-sms-провайдера).**

Комплект — каталог `deploy/`: Caddy (HTTPS с автоматическим сертификатом), API, Postgres и ежедневный бэкап.

### Требования

- Linux-сервер с Docker Engine и плагином `docker compose`; рекомендуется от 2 ГБ RAM (образ API собирается на самом сервере)
  или swap;
- домен, A-запись (и AAAA, если нужен IPv6 — см. [частые проблемы](#частые-проблемы)) которого указывает на сервер;
- открытые порты 80 и 443 (tcp), желательно и 443/udp (HTTP/3);
- SMTP-доступ у почтового провайдера с подтверждённым адресом отправителя;
- аккаунт и API-ключ SMS-провайдера;
- доступ к приватному репозиторию (collaborator, deploy key или токен — выдаёт владелец).

### Шаг 0. Подключение SMS-провайдера

Интерфейс `notify.SMSSender` реализуйте для вашего провайдера. Ниже — пример для SMS.ru; сверьте адрес и параметры с
документацией выбранного провайдера. Все фрагменты проверены сборкой (`go build ./... && go vet ./...`) и тестами.

1. Новый файл `internal/notify/smsru.go`. Таймаут HTTP-клиента — 8 с: запрос к серверу целиком ограничен 12 с,
   поэтому зависший провайдер даст чистый `503`, а не обрыв.

   ```go
   package notify

   import (
   	"context"
   	"encoding/json"
   	"fmt"
   	"io"
   	"net/http"
   	"net/url"
   	"strings"
   	"time"
   )

   // SMSRu sends SMS through the sms.ru HTTP API.
   type SMSRu struct {
   	apiID  string
   	client *http.Client
   }

   func NewSMSRu(apiID string) *SMSRu {
   	return &SMSRu{apiID: apiID, client: &http.Client{Timeout: 8 * time.Second}}
   }

   func (s *SMSRu) SendSMS(ctx context.Context, phone, text string) error {
   	form := url.Values{"api_id": {s.apiID}, "to": {strings.TrimPrefix(phone, "+")}, "msg": {text}, "json": {"1"}}
   	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://sms.ru/sms/send", strings.NewReader(form.Encode()))
   	if err != nil {
   		return err
   	}
   	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
   	resp, err := s.client.Do(req)
   	if err != nil {
   		return err
   	}
   	defer resp.Body.Close()
   	if resp.StatusCode != http.StatusOK {
   		return fmt.Errorf("sms.ru: HTTP status %d", resp.StatusCode)
   	}
   	var out struct {
   		Status     string `json:"status"`
   		StatusCode int    `json:"status_code"`
   	}
   	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
   		return fmt.Errorf("sms.ru: bad response: %w", err)
   	}
   	if out.Status != "OK" {
   		// Не кладите в ошибку номер и текст SMS (в нём код): ошибка попадает в лог.
   		return fmt.Errorf("sms.ru: status %q, code %d", out.Status, out.StatusCode)
   	}
   	return nil
   }
   ```

   Ошибка отправки превращается в `503 service_unavailable`, код при этом удаляется и лимит не тратится — пользователь повторяет запрос.

2. `internal/config/config.go`: поле в `Config` (рядом с `SMSAppHash`) и замена проверки в `validate()`.

   ```go
   SMSRuAPIID string `env:"SMSRU_API_ID"`
   ```

   Вместо `if c.SMSProvider != "log" { … }`:

   ```go
   switch c.SMSProvider {
   case "log":
   case "smsru":
   	if c.SMSRuAPIID == "" {
   		add("SMSRU_API_ID is required when SMS_PROVIDER=smsru")
   	}
   default:
   	add("SMS_PROVIDER %q is not supported (available: log, smsru)", c.SMSProvider)
   }
   ```

   Проверка «`log` запрещён в `prod`» ниже по функции остаётся как есть. Ключ провайдера в логи не попадает: `Config.String()` его не печатает.

3. `internal/app/app.go`, функция `senders` — вместо безусловного `notify.NewLogSMS`:

   ```go
   if sms == nil {
   	switch o.Config.SMSProvider {
   	case "smsru":
   		sms = notify.NewSMSRu(o.Config.SMSRuAPIID)
   	default: // config.validate пропускает только "log" и "smsru"
   		sms = notify.NewLogSMS(o.Log)
   	}
   }
   ```

4. Запустите `go test ./...` (при необходимости добавьте в `internal/config/config_test.go` случаи для `smsru` — тесты, ссылавшиеся на текст
   `available: log`, поправьте), добавьте `SMSRU_API_ID=` в `deploy/.env.example`, а в `deploy/.env` — `SMS_PROVIDER=smsru` и `SMSRU_API_ID=<ключ>`
   (файл целиком передаётся в контейнер через `env_file`, новые переменные подхватываются сами). Пересоберите образ: `docker compose up -d --build`.

Лимиты на отправку (на номер и на IP) уже защищают от «SMS-накрутки», но включите и лимиты/потолок расходов в кабинете провайдера.
Если вместо этого запущен сервер со старой сборкой и `SMS_PROVIDER=smsru`, он напишет `SMS_PROVIDER "smsru" is not supported (available: log)` —
значит, образ не пересобран.

### Первый запуск

```bash
git clone https://github.com/AntiqueViolet/go_kachalka_backend.git   # приватный репозиторий: доступ выдаёт владелец
cd go_kachalka_backend/deploy
cp .env.example .env
chmod 600 .env
nano .env                              # DOMAIN, POSTGRES_PASSWORD, SMTP_*, SMS-провайдер; про APP_SECRET — ниже
mkdir -p secrets backups
sudo chown 65532:65532 secrets         # контейнер API работает от uid 65532
docker compose build api
docker compose run --rm --no-deps -v "$PWD/secrets:/out" api keygen -jwt-key /out/jwt.pem
```

`keygen` создаст `secrets/jwt.pem` (владелец uid 65532, права `0600` — читать и копировать файл на хосте нужно через `sudo`) и напечатает
строку `APP_SECRET=…`. Впишите её в `.env`, **заменив** существующую пустую строку `APP_SECRET=`. Затем:

```bash
docker compose up -d
docker compose ps                      # все сервисы running/healthy
curl https://ВАШ_ДОМЕН/readyz          # {"status":"ready"}
docker compose logs --tail=50 api
```

После деплоя проверьте IPv6, если у домена есть AAAA-запись (см. [частые проблемы](#частые-проблемы)).

### Секреты

- **`APP_SECRET`** — из него выводятся refresh-токены и ключи кодов. Потеря или замена **разлогинит всех**: все refresh-токены и
  ожидающие коды станут недействительными, пользователям придётся войти заново.
- **`secrets/jwt.pem`** — ключ подписи access-токенов. Его замена (или потеря) отзывает только уже выданные access-токены (до 15 минут;
  клиенты тихо обновят их через refresh) и токены регистрации «в полёте»; refresh-токены и сессии не страдают.
- Бэкапы БД секретов **не содержат**. Храните `secrets/jwt.pem` и `APP_SECRET` отдельно от бэкапов — например, в менеджере паролей
  (`sudo cat secrets/jwt.pem`), иначе восстановление на новом сервере невозможно без выхода всех пользователей.

### Обновление

Перед обновлением сделайте внеочередной бэкап (миграции только вперёд: откат кода схему БД не откатывает):

```bash
cd go_kachalka_backend/deploy
docker compose exec backup sh -c 'pg_dump --format=custom --file=/backups/manual-$(date -u +%Y%m%d-%H%M%S).dump'
git pull
docker compose up -d --build
docker compose ps
docker compose logs --tail=50 api
```

Миграции БД применятся при старте. Чтобы получать исправления безопасности в базовых образах (Caddy, Postgres, Go, distroless),
раз в месяц-два выполняйте:

```bash
docker compose pull && docker compose build --pull && docker compose up -d
```

### Бэкапы и восстановление

Сервис `backup` раз в сутки делает `pg_dump` в `deploy/backups/` (файлы `kachalka-*.dump`, формат custom). Он хранит `BACKUP_KEEP_DAYS` дней
(по умолчанию 14), но удаляет старые дампы только после успешного нового и **всегда оставляет 3 самых свежих**. Файлы `manual-*.dump`
автоматически не удаляются. У сервиса есть healthcheck: он становится `unhealthy` (видно в `docker compose ps`), если самый свежий
`kachalka-*.dump` старше 26 часов, — то есть бэкапы перестали создаваться.

Все команды — из каталога `deploy/`. Внеочередной бэкап:

```bash
docker compose exec backup sh -c 'pg_dump --format=custom --file=/backups/manual-$(date -u +%Y%m%d-%H%M%S).dump'
```

Дампы содержат персональные данные (телефоны, email, хэши паролей). Файлы принадлежат root (читать — через `sudo`); копируйте их за
пределы сервера только по защищённому каналу, лучше в зашифрованное хранилище. Пример ежедневного задания на хосте
(`/etc/cron.d/kachalka-backup`, от root; путь к репозиторию подставьте свой; `restic` или `rclone` — аналогично):

```
30 4 * * * root rsync -a -e "ssh -i /root/.ssh/backup_key" /srv/go_kachalka_backend/deploy/backups/ backup@storage.example.com:kachalka/
```

Периодически проверяйте, что дамп действительно восстанавливается (учебное восстановление на отдельной машине).

**Восстановление на работающем сервере** (данные, созданные после бэкапа, будут потеряны; сессии, выданные позже бэкапа, получат
`invalid_refresh_token` — пользователи войдут заново):

```bash
docker compose stop api
sudo cat backups/ИМЯ_ФАЙЛА.dump | docker compose exec -T postgres sh -c 'pg_restore --clean --if-exists -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
docker compose start api
```

**Новый сервер или потерянный том БД:** клонируйте репозиторий, верните на место `deploy/.env` и `deploy/secrets/` (из менеджера паролей;
`jwt.pem` — владелец 65532, права `0600`), скопируйте нужный дамп в `deploy/backups/`, затем

```bash
docker compose up -d postgres
sudo cat backups/ИМЯ_ФАЙЛА.dump | docker compose exec -T postgres sh -c 'pg_restore --clean --if-exists -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
docker compose up -d
```

Никогда не выполняйте `docker compose down -v` на сервере: ключ `-v` удаляет тома `pgdata` (всю БД) и `caddy_data` (сертификаты).
Обычная остановка — `docker compose down` или `docker compose stop`.

### Пробный запуск комплекта на своей машине

Можно проверить весь стек (Caddy, API, Postgres, бэкап) локально, не подключая SMS и почту. В `deploy/.env` (из `.env.example`) укажите:
`DOMAIN=localhost`, любой `POSTGRES_PASSWORD` из букв и цифр, пустые `SMTP_HOST=` и `SMTP_FROM=`, и добавьте строку `APP_ENV=dev`
(она подхватывается через `${APP_ENV:-prod}` и разрешает SMS `log` и пустую почту). **Не используйте `APP_ENV=dev` на настоящем сервере.**

```bash
cd deploy
mkdir -p secrets backups
docker compose build api
docker compose run --rm --no-deps -v ./secrets:/out api keygen -jwt-key /out/jwt.pem   # напечатает APP_SECRET=… — вставьте в .env
docker compose up -d
curl -k https://localhost/readyz                                                         # {"status":"ready"}
```

Нужны свободные порты 80 и 443 и подсеть `172.30.10.0/24`. На Docker Desktop (Windows, macOS) `chown 65532` не нужен — права на смонтированные
каталоги проверены на Docker Desktop for Windows и не мешают. В Linux локально сначала выполните `sudo chown 65532:65532 secrets`.
Путь `./secrets:/out` (а не `"$PWD/secrets:/out"`) намеренно: в Git Bash на Windows `$PWD` превращается в `/c/Users/…`, и Docker
смонтирует не тот каталог — ключ окажется вне `deploy/secrets`, и API упадёт с `open /secrets/jwt.pem: no such file`.
Остановить и стереть пробный стенд вместе с данными: `docker compose down -v` (только для пробного запуска!).

## Безопасность (кратко)

- Пароли — argon2id (параметры OWASP), число одновременных хэширований ограничено.
- Коды — 6 цифр, в БД только HMAC, 5 попыток, 10 минут, лимиты на отправку (параллельные запросы их не обходят; недоставленный код лимит не тратит).
- Refresh-токены не хранятся вовсе: сервер вычисляет их из сессии и `APP_SECRET`; повторное использование старого токена отзывает сессию.
- Одинаковый ответ и время ответа для неизвестного номера и неверного пароля; `password/forgot` (письмо уходит в фоне) и `password/reset` (одна ошибка `invalid_code` на всё) не раскрывают, есть ли email.
- Неверный текущий пароль при смене пароля/email и удалении аккаунта ограничен так же, как неудачные входы.
- `X-Forwarded-For` учитывается только от Caddy; тело запроса ≤ 64 КиБ; таймаут запроса 12 с и таймауты сервера; `Cache-Control: no-store`; Caddy отдаёт `Strict-Transport-Security` (HSTS) и редиректит `http` на `https`.
- В prod БД и API наружу не публикуются: снаружи доступен только Caddy (80/443); контейнер API работает только на чтение, без привилегий (capabilities).
- Корневой `docker-compose.yml` — **только для разработки** (пароль Postgres `kachalka`, `APP_ENV=dev`, коды в логах). На сервере используйте только `deploy/compose.yaml`.
  В dev Postgres и Mailpit слушают только `localhost`, а API — всю локальную сеть (для проверки с устройства).
- Пароли, коды (кроме dev: лог SMS и писем при пустом `SMTP_HOST`), токены и секреты не пишутся в логи.

## Частые проблемы

- **Сервер в prod не стартует: `SMS_PROVIDER=log is forbidden in prod`** — подключите SMS-провайдера ([шаг 0](#шаг-0-подключение-sms-провайдера)).
- **`SMS_PROVIDER "x" is not supported (available: log)`** — такой провайдер ещё не добавлен в код (или образ не пересобран): шаг 0, затем `docker compose up -d --build`.
- **API перезапускается в цикле / `dependency failed to start … is unhealthy`** — смотрите `docker compose logs api`: первые строки называют проблемную переменную.
  `open /secrets/jwt.pem` — не выполнен `keygen`; `permission denied` — не выполнен `sudo chown 65532:65532 secrets`.
- **Caddy не получает сертификат** — проверьте A/AAAA-записи домена, открыты ли 80/443 (tcp) и 443/udp в файрволе хостинга и на сервере,
  читайте `docker compose logs caddy`. У Let's Encrypt есть лимиты на повторные попытки — не пересоздавайте тома `caddy_data` без нужды.
- **`password authentication failed`** — `POSTGRES_PASSWORD` применяется только при первом создании тома БД. Верните прежний пароль в `.env`
  (или, если данные не нужны, пересоздайте том: `docker compose down -v` — это стирает БД).
- **Письма попадают в спам** — используйте подтверждённый адрес отправителя у SMTP-провайдера, настройте SPF и DKIM для домена.
- **IPv6-клиенты делят один лимит.** После деплоя выполните `curl -6 https://ВАШ_ДОМЕН/v1/me` (ответ `401` — это нормально) и посмотрите поле `ip` в
  `docker compose logs api`. Если там адрес шлюза Docker-сети (`172.30.10.1`), все IPv6-пользователи попадают в одну корзину лимитов:
  удалите AAAA-запись домена или включите IPv6 для сети `backend`.
- **`port is already allocated`** — порт 8080/5432/8025 (dev) или 80/443 (сервер) занят другим процессом; остановите его или поменяйте порт в compose-файле.
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
