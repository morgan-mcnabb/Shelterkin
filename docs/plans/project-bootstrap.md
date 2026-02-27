# Project Bootstrap Plan

Initial project setup to take the repository from design docs to a bootable server with all infrastructure in place.

## Phase 1: Go Module + Dependencies

- `go mod init github.com/shelterkin/shelterkin`
- Install core deps: `modernc.org/sqlite`, `goose/v3`, `templ`, `gorilla/sessions`, `golang.org/x/crypto`, `oklog/ulid/v2`

## Phase 2: Directory Structure

```
cmd/shelterkin/
internal/apperror/
internal/config/
internal/crypto/
internal/database/
internal/middleware/
internal/server/
internal/testutil/
components/
db/migrations/
db/queries/
internal/db/dbgen/
static/css/
static/js/
```

## Phase 3: Core Infrastructure Packages

Built in dependency order so each package compiles before the next.

### 3a. `internal/apperror/` (zero deps)

- `Error` struct with Type, Message, Field, RetryAfter, wrapped Err
- Type enum: Validation(400), NotFound(404), Unauthorized(401), Forbidden(403), Conflict(409), RateLimited(429), Unavailable(503), Internal(500)
- Constructor funcs: `Validation()`, `NotFound()`, `Unauthorized()`, `Internal()`, etc.
- `HTTPStatus()` mapper, `ValidationErrors` for multi-field validation
- SQLite constraint helpers (`IsUniqueConstraintViolation`)

### 3b. `internal/config/` (zero deps)

- `Config` struct: Port, DatabasePath, SessionSecret, EncryptionSecret, CSRFKey, DataDir, LogLevel, BaseURL
- `Load()` reads env vars with sensible defaults, validates required secrets

### 3c. `internal/crypto/` (depends on golang.org/x/crypto)

- `Encryptor`: AES-256-GCM encrypt/decrypt with random nonce, base64 encoding
- `DeriveKey()`: Argon2id key derivation from master secret + salt
- `GenerateSalt()`: 16 random bytes
- `HMACHasher`: HMAC-SHA256 for lookup fields (email_hash), hex-encoded output
- Tests: round-trip, different ciphertexts for same input, wrong-key failure, HMAC determinism

### 3d. `internal/database/` (depends on config, sqlite, goose)

- `Open(path)`: SQLite connection with WAL mode, busy timeout, foreign keys, max 1 conn
- `RunMigrations(db, fs, dir)`: runs goose Up with embedded FS

### 3e. `internal/middleware/` (depends on apperror)

- `RequestID`: generates random ID, stores in context + X-Request-ID header
- `SecurityHeaders`: all headers from tech-stack doc (CSP, X-Frame-Options, etc.)
- `Logging`: slog-based structured logging (method, path, status, duration, request ID)
- `Recover`: panic recovery with 500 response

### 3f. `internal/testutil/` (depends on crypto, database, db)

- `NewTestDB(t)`: in-memory SQLite with migrations, auto-cleanup
- `NewTestEncryptor(t)`, `NewTestHMAC(t)`: fixed-key instances for tests
- Stub `factories.go` for future CreateTestHousehold/CreateTestUser

## Phase 4: First Migration

**File: `db/migrations/001_foundational_tables.sql`**

7 tables from schema-design.md:

| Table | Purpose |
|-------|---------|
| `config` | key-value runtime settings |
| `households` | top-level org, name_enc, encryption_salt, onboarding_progress |
| `users` | household-scoped, email_enc/hash, password_hash, role, totp |
| `sessions` | server-side session tracking, expiry |
| `login_attempts` | brute-force protection |
| `invites` | token-based, expiring |
| `audit_log` | action tracking, entity refs |

Plus `db/embed.go` to expose `MigrationsFS` via `//go:embed`.

## Phase 5: sqlc Setup + Initial Queries

- `sqlc.yaml` targeting SQLite engine, `db/queries/` -> `internal/db/dbgen/`
- Query files: `config.sql`, `households.sql`, `users.sql`, `sessions.sql`, `login_attempts.sql`, `invites.sql`, `audit_log.sql`
- Run `sqlc generate` to produce type-safe Go code

## Phase 6: Server + Entry Point

### `internal/server/`

- `Server` struct holds config, db, encryptor, hmac, http.Server, mux
- `New()` wires middleware chain: Recover -> RequestID -> SecurityHeaders -> Logging -> mux
- Registers `GET /static/`, `GET /health`, `GET /` (placeholder)
- Timeouts: read 15s, write 30s, idle 60s

### `cmd/shelterkin/main.go`

- `run()` function: load config -> open db -> run migrations -> init encryption (get/create salt from config table) -> verify encryption key -> create server -> graceful shutdown on SIGINT/SIGTERM
- Embeds `static/*` via `embed.FS`

## Phase 7: Templates + Static Assets

- `components/layout.templ`: HTML shell with DaisyUI theme, HTMX script, alert container, HTMX response config
- `components/errors.templ`: `FormFieldError`, `AlertBanner`, OOB swap variants
- `components/helpers.go`: alertClass helper
- Download `htmx.min.js` and `htmx-sse.js` into `static/js/`

## Phase 8: Tailwind CSS + DaisyUI

- `package.json` with tailwindcss + daisyui devDependencies
- `tailwind.config.js`: scan `.templ` and `.go` files, DaisyUI plugin with themes
- `input.css`: Tailwind directives
- Generate `static/css/styles.css`

## Phase 9: Build + Dev Tooling

| File | Purpose |
|------|---------|
| `Makefile` | generate, build, run, test, test-race, lint, dev, check targets |
| `.air.toml` | live reload watching .go, .templ, .sql files |
| `Dockerfile` | multi-stage alpine build, non-root user, /app/data volume |
| `docker-compose.yml` | single service with env vars, volume mount |
| `.goreleaser.yaml` | multi-arch (amd64/arm64), linux/darwin/windows, Docker images |
| `.env.example` | documented template of all env vars |

## Phase 10: .gitignore Updates

Add: `*_templ.go`, `internal/db/dbgen/`, `static/css/styles.css`, `tmp/`, `bin/`, `node_modules/`, `data/`, `dist/`, `tailwindcss-*`

## Phase 11: Verification

```bash
go mod tidy
go vet ./...
go test ./...
go run ./cmd/shelterkin  # with required env vars set
```

Confirm: server starts, migrations run, `/health` returns 200, `/` returns HTML.

## Complete File List (~30 files)

| File | Purpose |
|------|---------|
| `go.mod` | module definition |
| `internal/apperror/errors.go` | error types + constructors |
| `internal/apperror/validation.go` | multi-field validation |
| `internal/apperror/sqlite.go` | SQLite constraint helpers |
| `internal/apperror/errors_test.go` | error tests |
| `internal/config/config.go` | env-based configuration |
| `internal/config/config_test.go` | config tests |
| `internal/crypto/encryptor.go` | AES-256-GCM encrypt/decrypt |
| `internal/crypto/kdf.go` | Argon2id key derivation |
| `internal/crypto/hmac.go` | HMAC-SHA256 for lookups |
| `internal/crypto/crypto_test.go` | encryption round-trip tests |
| `internal/database/database.go` | SQLite connection + migrations |
| `internal/middleware/requestid.go` | request ID middleware |
| `internal/middleware/security.go` | security headers |
| `internal/middleware/logging.go` | structured logging |
| `internal/middleware/recover.go` | panic recovery |
| `internal/middleware/middleware_test.go` | middleware tests |
| `internal/server/server.go` | HTTP server setup |
| `internal/testutil/db.go` | test database helper |
| `internal/testutil/crypto.go` | test encryption helpers |
| `internal/testutil/factories.go` | stub for factory functions |
| `db/embed.go` | embedded migrations FS |
| `db/migrations/001_foundational_tables.sql` | 7 core tables |
| `db/queries/*.sql` (7 files) | sqlc query definitions |
| `sqlc.yaml` | sqlc configuration |
| `cmd/shelterkin/main.go` | entry point |
| `components/layout.templ` | HTML layout |
| `components/errors.templ` | error display components |
| `components/helpers.go` | template helpers |
| `input.css` | Tailwind input |
| `tailwind.config.js` | Tailwind + DaisyUI config |
| `package.json` | Node deps for Tailwind |
| `Makefile` | dev commands |
| `.air.toml` | live reload config |
| `Dockerfile` | container build |
| `docker-compose.yml` | deployment config |
| `.goreleaser.yaml` | release config |
| `.env.example` | env var template |
