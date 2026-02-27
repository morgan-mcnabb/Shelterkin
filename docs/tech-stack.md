# Caregiver Coordinator Hub — Tech Stack & Architecture Reference

*This document is the canonical technical reference for the Caregiver Coordinator Hub. It should be provided to any AI assistant or developer working on the project to ensure consistency across all code contributions.*

---

## Project Overview

The Caregiver Coordinator Hub is an open source, self-hosted web application for families and care teams coordinating care for elderly, disabled, or chronically ill loved ones. It is written in Go and designed to compile to a single binary that serves the entire application — backend, frontend, real-time updates, and static assets — from one process with zero external runtime dependencies.

### Design Principles

- **Single binary deployment**: no separate frontend server, no Node runtime, no external services required
- **SQLite by default**: zero-config database, single file, easy to back up, runs on minimal hardware
- **Server-rendered with progressive enhancement**: fast initial loads, SPA-like interactivity via HTMX, no full-page reloads for common actions
- **Accessible and non-technical-user-friendly**: clean UI, high contrast options, large touch targets, intuitive navigation for users who may be elderly or non-technical
- **AI-friendly codebase**: simple patterns, type-safe templates, SQL-first data access — easy for AI tools to read, generate, and modify
- **Privacy-first**: all data stays on the user's hardware, no telemetry, no cloud dependencies

---

## Tech Stack Summary

| Layer | Technology | Version/Source | Purpose |
|---|---|---|---|
| Language | Go | 1.22+ | Backend, routing, business logic, server |
| Router | Go standard library `net/http` | 1.22+ (method-based routing) | HTTP routing, middleware |
| Templating | Templ | github.com/a-h/templ | Type-safe, compiled HTML templates |
| Interactivity | HTMX | htmx.org (served as static asset) | Dynamic UI without JavaScript frameworks |
| Styling | Tailwind CSS | Standalone CLI binary | Utility-first CSS framework |
| Component Library | DaisyUI | Plugin for Tailwind | Pre-built accessible UI components |
| Database (default) | SQLite | modernc.org/sqlite (pure Go) | Embedded database, no CGO required |
| Database (optional) | PostgreSQL | github.com/jackc/pgx/v5 | For larger or multi-server deployments |
| Query Layer | sqlc | github.com/sqlc-dev/sqlc | Generates type-safe Go from SQL |
| Migrations | goose | github.com/pressly/goose/v3 | SQL migrations, embeddable in binary |
| Auth (1.0) | gorilla/sessions + bcrypt | github.com/gorilla/sessions | Session-based auth with secure cookies |
| Auth (post-1.0) | go-oidc | github.com/coreos/go-oidc/v3 | OIDC/OAuth2 for SSO providers |
| Real-time | Server-Sent Events (SSE) | Native Go + HTMX SSE extension | Live updates without WebSockets |
| Build/Release | GoReleaser | goreleaser.com | Multi-arch binaries + Docker images |
| Container | Docker | Multi-stage build | Single container deployment option |

---

## Layer-by-Layer Details

### 1. Go Backend (net/http, Go 1.22+)

Go 1.22 introduced method-based routing in the standard library, eliminating the need for third-party routers for most use cases.

**Routing pattern:**
```go
mux := http.NewServeMux()

// Method-based routing (Go 1.22+)
mux.HandleFunc("GET /api/care-recipients", handleListCareRecipients)
mux.HandleFunc("POST /api/care-recipients", handleCreateCareRecipient)
mux.HandleFunc("GET /api/care-recipients/{id}", handleGetCareRecipient)
mux.HandleFunc("PUT /api/care-recipients/{id}", handleUpdateCareRecipient)
mux.HandleFunc("DELETE /api/care-recipients/{id}", handleDeleteCareRecipient)

// Path parameters via request
id := r.PathValue("id")
```

**Middleware approach:**
Use standard `func(http.Handler) http.Handler` middleware pattern. Common middleware includes:
- Authentication check (session validation)
- Household scoping (ensure user can only access their household's data)
- Request logging
- CSRF protection
- Rate limiting

If middleware composition becomes complex, Chi (github.com/go-chi/chi) can be introduced as a drop-in — it implements `net/http` interfaces and adds middleware chaining, route groups, and subrouters without replacing the standard library.

**Key conventions:**
- All handlers receive `http.ResponseWriter` and `*http.Request`
- Business logic lives in service packages, not handlers
- Handlers are thin: parse request → call service → render response
- Use `context.Context` for passing auth/household info through the request chain
- Errors are handled explicitly (no panic-based error handling)

---

### 2. Templ (Templating)

Templ is a Go templating language that compiles to Go code. Templates are type-safe, refactorable, and have full IDE support.

**Why Templ over html/template:**
- Compile-time type checking (no runtime template errors)
- Components are Go functions — composable, testable
- IDE support: autocomplete, go-to-definition, rename refactoring
- AI tools work extremely well with it because the syntax is predictable Go + HTML

**File convention:**
- Template files use `.templ` extension
- Each feature/domain has its own templates directory
- Layouts and shared components live in a `components` package

**Example component:**
```templ
// components/card.templ
package components

templ Card(title string) {
    <div class="card bg-base-100 shadow-md">
        <div class="card-body">
            <h2 class="card-title">{ title }</h2>
            { children... }
        </div>
    </div>
}

// care/dashboard.templ
package care

import "caregiver-hub/components"

templ Dashboard(recipient CareRecipient, tasks []Task) {
    @components.Card(recipient.Name) {
        <ul>
            for _, task := range tasks {
                <li class={ templ.KV("line-through", task.Completed) }>
                    { task.Description }
                </li>
            }
        </ul>
    }
}
```

**Code generation:**
```bash
# Run templ generate to compile .templ files to .go files
templ generate

# Watch mode during development
templ generate --watch
```

**Integration with handlers:**
```go
func handleDashboard(w http.ResponseWriter, r *http.Request) {
    recipient := getCareRecipient(r.Context())
    tasks := getTodaysTasks(r.Context(), recipient.ID)
    care.Dashboard(recipient, tasks).Render(r.Context(), w)
}
```

---

### 3. HTMX (Interactivity)

HTMX enables dynamic, SPA-like behavior by making HTML the medium of exchange rather than JSON. The server renders HTML fragments and HTMX swaps them into the DOM.

**How it works with this stack:**
1. User interacts with the page (clicks button, submits form, etc.)
2. HTMX sends an AJAX request to the Go backend
3. Backend handler renders a Templ component (HTML fragment)
4. HTMX swaps the fragment into the specified DOM target

**Common patterns used in this app:**

```html
<!-- Mark a task as complete without page reload -->
<button hx-post="/tasks/{id}/complete"
        hx-target="#task-{id}"
        hx-swap="outerHTML"
        class="btn btn-sm btn-success">
    Mark Done
</button>

<!-- Load shift handoff form inline -->
<button hx-get="/shifts/handoff/form"
        hx-target="#handoff-container"
        hx-swap="innerHTML"
        class="btn btn-primary">
    Start Handoff
</button>

<!-- Live search the contact directory -->
<input type="search"
       name="q"
       hx-get="/contacts/search"
       hx-target="#contact-results"
       hx-trigger="input changed delay:300ms"
       placeholder="Search contacts..."
       class="input input-bordered w-full" />

<!-- SSE for live hub updates -->
<div hx-ext="sse"
     sse-connect="/events/hub"
     sse-swap="notification"
     hx-target="#notification-badge">
</div>
```

**Server-side conventions for HTMX:**
- Check for `HX-Request` header to distinguish HTMX requests from full page loads
- For HTMX requests: return only the HTML fragment (rendered Templ component)
- For full page loads: return the full page with layout wrapper
- Use `HX-Trigger` response header to fire client-side events (e.g., close a modal after form submission)
- Use `HX-Redirect` for post-action navigation

```go
func handleCompleteTask(w http.ResponseWriter, r *http.Request) {
    taskID := r.PathValue("id")
    task, err := taskService.Complete(r.Context(), taskID)
    if err != nil {
        http.Error(w, "Failed to complete task", 500)
        return
    }

    // Return just the updated task row fragment
    components.TaskRow(task).Render(r.Context(), w)
}
```

**HTMX is served as a static asset:**
- Download htmx.min.js and include it in the embedded static files
- No npm, no build step for HTMX itself
- Include the SSE extension for real-time features

---

### 4. Tailwind CSS + DaisyUI (Styling)

**Tailwind CSS** is used via the standalone CLI binary (no Node.js required). It scans `.templ` files for class names and generates a minimal CSS file.

**DaisyUI** is a Tailwind plugin that provides pre-built, themed, accessible components: buttons, cards, modals, dropdowns, badges, alerts, navigation, forms, and more.

**Setup:**
```bash
# Download Tailwind standalone CLI
curl -sLO https://github.com/tailwindlabs/tailwindcss/releases/latest/download/tailwindcss-linux-x64
chmod +x tailwindcss-linux-x64

# tailwind.config.js
module.exports = {
  content: ["./**/*.templ"],
  plugins: [require("daisyui")],
  daisyui: {
    themes: ["light", "dark", "cupcake", "emerald"],
  },
}
```

```bash
# Build CSS (production)
./tailwindcss -i input.css -o static/css/styles.css --minify

# Watch mode (development)
./tailwindcss -i input.css -o static/css/styles.css --watch
```

**DaisyUI component usage in Templ:**
```templ
// A medication card with status badge
templ MedicationCard(med Medication) {
    <div class="card bg-base-100 shadow-sm border border-base-200">
        <div class="card-body p-4">
            <div class="flex items-center justify-between">
                <h3 class="font-semibold text-lg">{ med.Name }</h3>
                if med.IsDue() {
                    <span class="badge badge-warning">Due Now</span>
                } else if med.IsOverdue() {
                    <span class="badge badge-error">Overdue</span>
                } else {
                    <span class="badge badge-ghost">{ med.NextDueTime() }</span>
                }
            </div>
            <p class="text-sm text-base-content/70">{ med.Dosage } — { med.Instructions }</p>
        </div>
    </div>
}
```

**Theming and accessibility:**
- DaisyUI themes are swappable at runtime via a `data-theme` attribute on `<html>`
- Include a high-contrast theme for visually impaired users
- Dark mode for late-night caregiving
- User theme preference stored in their profile
- All interactive elements must be keyboard navigable and screen-reader friendly

**Design guidelines for this app:**
- Large touch targets (minimum 44x44px) — caregivers may be using the app one-handed or on a phone while multitasking
- Clear visual hierarchy — the most important information (overdue meds, active alerts) should be visually dominant
- Minimal cognitive load — avoid dense UIs, use progressive disclosure (show summary, expand for details)
- Consistent color coding: red/error = overdue/critical, yellow/warning = due soon/attention needed, green/success = completed/on track
- Typography: minimum 16px base font size, generous line height

---

### 5. SQLite (Default Database)

**Driver:** `modernc.org/sqlite` — a pure Go SQLite implementation. No CGO, no C compiler needed, compiles cleanly on all platforms.

**Why this driver over mattn/go-sqlite3:**
- Pure Go = true single binary with no system dependencies
- Cross-compiles cleanly for ARM64 (Raspberry Pi), AMD64, etc.
- Slightly slower than the CGO version but more than fast enough for this use case

**Connection setup:**
```go
import (
    "database/sql"
    _ "modernc.org/sqlite"
)

func openDB(path string) (*sql.DB, error) {
    db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON")
    if err != nil {
        return nil, err
    }
    db.SetMaxOpenConns(1) // SQLite write concurrency limitation
    return db, nil
}
```

**Critical SQLite pragmas:**
- `_journal_mode=WAL` — enables concurrent reads while writing, essential for a web app
- `_busy_timeout=5000` — wait up to 5 seconds for locks instead of immediately failing
- `_foreign_keys=ON` — enforce referential integrity (SQLite disables this by default)

**Backup strategy:**
- SQLite's `.backup` API or simple file copy (safe when using WAL mode with a checkpoint)
- Scheduled backups via built-in cron-like scheduler in the Go app
- Backup to local path or S3-compatible storage

---

### 6. sqlc (Query Layer)

sqlc generates type-safe Go code from SQL queries. You write SQL, sqlc generates the structs and functions.

**Why sqlc:**
- SQL is the source of truth — no ORM magic, no runtime query building
- Generated code is plain Go — easy to read, debug, and understand
- AI tools are excellent at writing SQL, making this extremely AI-friendly
- Supports both SQLite and PostgreSQL from the same query files (with minor dialect handling)

**Configuration (sqlc.yaml):**
```yaml
version: "2"
sql:
  - engine: "sqlite"
    queries: "db/queries/"
    schema: "db/migrations/"
    gen:
      go:
        package: "dbgen"
        out: "internal/db/dbgen"
        emit_json_tags: true
        emit_empty_slices: true
```

**Example query file (db/queries/tasks.sql):**
```sql
-- name: GetTasksByRecipientAndDate :many
SELECT id, care_recipient_id, title, description, category,
       assigned_to, completed, completed_at, completed_by,
       due_at, created_at
FROM tasks
WHERE care_recipient_id = ? AND DATE(due_at) = DATE(?)
ORDER BY due_at ASC;

-- name: CompleteTask :one
UPDATE tasks
SET completed = true, completed_at = ?, completed_by = ?
WHERE id = ?
RETURNING *;

-- name: CreateTask :one
INSERT INTO tasks (id, care_recipient_id, title, description, category, assigned_to, due_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;
```

**Generated Go code (used in service layer):**
```go
// This code is auto-generated by sqlc — do not edit manually
func (q *Queries) GetTasksByRecipientAndDate(ctx context.Context, careRecipientID string, date string) ([]Task, error) { ... }
func (q *Queries) CompleteTask(ctx context.Context, arg CompleteTaskParams) (Task, error) { ... }
```

**Workflow:**
1. Write or modify SQL in `db/queries/`
2. Run `sqlc generate`
3. Use generated functions in service layer
4. Compile and test

---

### 7. goose (Migrations)

goose manages database schema migrations. Migrations are written as SQL files and can be embedded into the binary so they run automatically on startup.

**Migration files (db/migrations/):**
```sql
-- 001_initial_schema.sql

-- +goose Up
CREATE TABLE households (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE users (
    id TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id),
    email TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    display_name TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'member',
    auth_provider TEXT NOT NULL DEFAULT 'local',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE care_recipients (
    id TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id),
    name TEXT NOT NULL,
    date_of_birth DATE,
    blood_type TEXT,
    photo_path TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE care_recipients;
DROP TABLE users;
DROP TABLE households;
```

**Embedding and auto-running on startup:**
```go
import (
    "embed"
    "github.com/pressly/goose/v3"
)

//go:embed db/migrations/*.sql
var migrations embed.FS

func runMigrations(db *sql.DB) error {
    goose.SetBaseFS(migrations)
    return goose.Up(db, "db/migrations")
}
```

This means migrations are baked into the binary. When a user updates to a new version and restarts, schema migrations run automatically. Zero manual intervention.

---

### 8. Authentication (1.0)

**Session-based auth with secure cookies.**

```go
import (
    "github.com/gorilla/sessions"
    "golang.org/x/crypto/bcrypt"
)

// Session store — use filesystem or cookie store
// For single-server SQLite deployments, cookie store is fine
var store = sessions.NewCookieStore([]byte(os.Getenv("SESSION_SECRET")))

func init() {
    store.Options = &sessions.Options{
        Path:     "/",
        MaxAge:   86400 * 30, // 30 days
        HttpOnly: true,
        Secure:   true, // requires HTTPS
        SameSite: http.SameSiteLaxMode,
    }
}
```

**User model design (OIDC-ready):**
The user table includes an `auth_provider` field from day one. For 1.0, this is always "local". Post-1.0, it can be "oidc", "authentik", etc. This avoids a painful migration later.

**Password hashing:**
```go
hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(password))
```

**CSRF protection:**
Use the `gorilla/csrf` package or a simple double-submit cookie pattern. HTMX requests include a CSRF token via `hx-headers` configured globally.

**Role-based access:**
Roles are checked in middleware. The role hierarchy for this app:
1. `admin` — household creator, full access, can manage users and settings
2. `member` — family member, can view/edit most things, can't manage users
3. `caregiver` — professional/hired caregiver, scoped to assigned care recipients only
4. `readonly` — view-only access (for distant relatives who want to stay informed)

---

### 9. Encryption & Data Security

This application stores sensitive medical and personal data. Encryption is not optional — it's a core architectural requirement. Users need confidence that even if someone gains access to the server's filesystem or database file, the sensitive data is unreadable without the encryption key.

**Approach: Application-Level Encryption (ALE)**

SQLCipher (full-database encryption for SQLite) requires CGO, which breaks the pure-Go single-binary deployment story. Instead, this app uses application-level encryption: sensitive fields are encrypted in Go before being written to the database, and decrypted after being read.

This means non-sensitive data (IDs, timestamps, foreign keys, task completion booleans) remains queryable in plain text, while sensitive content (medical details, care log narratives, personal notes) is encrypted at the field level.

**Algorithm: AES-256-GCM**

```go
import (
    "crypto/aes"
    "crypto/cipher"
    "crypto/rand"
    "encoding/base64"
    "io"
)

type Encryptor struct {
    gcm cipher.AEAD
}

func NewEncryptor(key []byte) (*Encryptor, error) {
    block, err := aes.NewCipher(key) // key must be 32 bytes for AES-256
    if err != nil {
        return nil, err
    }
    gcm, err := cipher.NewGCM(block)
    if err != nil {
        return nil, err
    }
    return &Encryptor{gcm: gcm}, nil
}

func (e *Encryptor) Encrypt(plaintext string) (string, error) {
    nonce := make([]byte, e.gcm.NonceSize())
    if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
        return "", err
    }
    ciphertext := e.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
    return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func (e *Encryptor) Decrypt(encoded string) (string, error) {
    ciphertext, err := base64.StdEncoding.DecodeString(encoded)
    if err != nil {
        return "", err
    }
    nonceSize := e.gcm.NonceSize()
    if len(ciphertext) < nonceSize {
        return "", fmt.Errorf("ciphertext too short")
    }
    nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
    plaintext, err := e.gcm.Open(nil, nonce, ciphertext, nil)
    if err != nil {
        return "", err
    }
    return string(plaintext), nil
}
```

**Key Derivation & Management**

The user provides a master secret via environment variable or config file. The app derives the actual encryption key using Argon2id (via `golang.org/x/crypto/argon2`), which is resistant to brute-force attacks.

```go
import "golang.org/x/crypto/argon2"

func deriveKey(masterSecret string, salt []byte) []byte {
    // Argon2id: 1 iteration, 64MB memory, 4 threads, 32-byte output (AES-256)
    return argon2.IDKey([]byte(masterSecret), salt, 1, 64*1024, 4, 32)
}
```

- The master secret is provided by the user: `ENCRYPTION_SECRET=your-long-random-string-here`
- A random salt is generated on first run and stored in the database (in a `config` table) — the salt is not sensitive
- The derived key is held in memory only, never written to disk
- If the user loses their encryption secret, encrypted data is unrecoverable — the setup wizard and documentation must make this extremely clear
- The app should refuse to start if `ENCRYPTION_SECRET` is not set

**What Gets Encrypted (Field-Level)**

| Data | Encrypted | Rationale |
|---|---|---|
| Care recipient name | Yes | PII |
| Medical conditions, allergies, blood type | Yes | PHI |
| Medication names, dosages, instructions | Yes | PHI |
| Care log narrative/journal entries | Yes | PHI, sensitive observations |
| Doctor/provider names and contact details | Yes | PHI |
| Insurance information | Yes | PII/financial |
| Incident report details | Yes | PHI |
| Daily routine preferences | Yes | Personal, potentially sensitive |
| Emergency medical summary | Yes | PHI |
| User email addresses | Yes | PII |
| User display names | Yes | PII |
| Task titles and descriptions | Yes | May contain PHI |
| Message/announcement content | Yes | May contain PHI |
| Handoff note content | Yes | Contains PHI |
| Task IDs, foreign keys, timestamps | No | Needed for queries and indexing |
| Completion booleans, status flags | No | Non-sensitive operational data |
| Role assignments, permissions | No | Non-sensitive |
| Scheduling data (shift times, slots) | No | Non-sensitive structure |

**Implementation Pattern in Service Layer**

Encryption and decryption happen in the service layer, not in handlers or the database layer. sqlc-generated code works with the encrypted strings — it doesn't know or care about encryption.

```go
// Service layer encrypts before calling sqlc-generated DB functions
func (s *CareRecipientService) Create(ctx context.Context, input CreateInput) (*CareRecipient, error) {
    encName, err := s.enc.Encrypt(input.Name)
    if err != nil {
        return nil, fmt.Errorf("encrypting name: %w", err)
    }
    encConditions, err := s.enc.Encrypt(input.MedicalConditions)
    if err != nil {
        return nil, fmt.Errorf("encrypting conditions: %w", err)
    }

    row, err := s.db.CreateCareRecipient(ctx, dbgen.CreateCareRecipientParams{
        ID:                uuid.NewString(),
        HouseholdID:       getHouseholdID(ctx),
        Name:              encName,       // stored as base64 ciphertext
        MedicalConditions: encConditions, // stored as base64 ciphertext
        // ...
    })
    if err != nil {
        return nil, err
    }

    // Return decrypted data to the handler
    return s.rowToRecipient(row) // decrypts fields internally
}
```

**File Encryption (Uploaded Documents)**

Medical documents, photos (wound tracking, receipts), and other uploaded files are encrypted on disk using the same AES-256-GCM approach. Files are encrypted as whole blobs before writing to the filesystem and decrypted when served.

```go
func (s *FileService) SaveEncrypted(file io.Reader, filename string) (storedPath string, err error) {
    plaintext, err := io.ReadAll(file)
    if err != nil {
        return "", err
    }

    nonce := make([]byte, s.gcm.NonceSize())
    if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
        return "", err
    }

    ciphertext := s.gcm.Seal(nonce, nonce, plaintext, nil)

    storedPath = filepath.Join(s.uploadDir, uuid.NewString()+".enc")
    if err := os.WriteFile(storedPath, ciphertext, 0600); err != nil {
        return "", err
    }

    return storedPath, nil
}
```

- Original filenames are stored encrypted in the database, not on the filesystem
- Files on disk have UUID names with `.enc` extension — no metadata leakage from filenames
- File permissions are set to `0600` (owner read/write only)

**Backup Encryption**

Database backups and full data exports must also be encrypted since the SQLite file contains encrypted field values but also unencrypted metadata (table structure, timestamps, IDs).

```go
// Full backup: copy SQLite file + uploads dir, then encrypt the archive
func (s *BackupService) CreateBackup() (string, error) {
    // 1. Checkpoint WAL to ensure consistency
    s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")

    // 2. Create tar.gz of database file + uploads directory
    archivePath := createArchive(s.dbPath, s.uploadDir)

    // 3. Encrypt the archive using the same AES-256-GCM
    encryptedPath, err := s.enc.EncryptFile(archivePath)
    if err != nil {
        return "", err
    }

    // 4. Remove unencrypted archive
    os.Remove(archivePath)

    return encryptedPath, nil
}
```

- Backups are encrypted with the same master key
- Backup files include a version header so the restore process knows which format/schema to expect
- Automated scheduled backups should be supported (daily/weekly, configurable)

**Emergency Access URL Security**

The emergency mode QR code / shareable URL bypasses authentication but must still protect data:

- Emergency URLs use a cryptographically random token (minimum 32 bytes, URL-safe base64)
- Tokens are time-limited (configurable, default 24 hours, regenerable)
- Tokens are revocable at any time by any household admin
- The emergency view shows only the specific medical summary fields needed by paramedics — not the full care recipient profile
- Emergency tokens are hashed in the database (like passwords) so a database leak doesn't expose valid tokens
- Rate limiting on emergency URL access to prevent brute-force

```go
// Emergency token generation
token := make([]byte, 32)
rand.Read(token)
urlToken := base64.URLEncoding.EncodeToString(token)
hashedToken := sha256.Sum256(token)

// Store hash in DB, give URL-safe token to user
// GET /emergency/{token} — validates against stored hash
```

**Security Headers & Transport**

The app should set appropriate security headers on all responses:

```go
func securityHeaders(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("X-Frame-Options", "DENY")
        w.Header().Set("X-XSS-Protection", "0") // disabled, rely on CSP
        w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
        w.Header().Set("Content-Security-Policy",
            "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'")
        w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
        next.ServeHTTP(w, r)
    })
}
```

- HTTPS is expected in production (via reverse proxy or built-in Let's Encrypt)
- The app should warn (not block) if running over plain HTTP
- All cookies are `Secure`, `HttpOnly`, `SameSite=Lax`

**Key Rotation**

Support key rotation for users who want to change their encryption secret:

1. User provides old secret + new secret
2. App derives both keys
3. App reads all encrypted fields, decrypts with old key, re-encrypts with new key
4. Re-encrypts all uploaded files
5. Updates the salt in the config table
6. This is a potentially long-running operation — show progress and prevent concurrent access during rotation

**Documentation Requirements**

The encryption implementation must be clearly documented for users:

- What is encrypted and what is not
- The importance of the `ENCRYPTION_SECRET` and that losing it means permanent data loss
- How to back up and restore (including the secret)
- Key rotation process
- A clear statement that this is not HIPAA-certified software, but is designed with healthcare data privacy best practices
- Recommendation to run behind HTTPS in production

---

### 10. Server-Sent Events (Real-time)

SSE provides one-way server-to-client streaming over standard HTTP. Perfect for live dashboard updates, notification badges, and shift change alerts.

**Server-side (Go):**
```go
func handleSSE(w http.ResponseWriter, r *http.Request) {
    flusher, ok := w.(http.Flusher)
    if !ok {
        http.Error(w, "SSE not supported", http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.Header().Set("Connection", "keep-alive")

    // Subscribe this client to their household's event stream
    householdID := getHouseholdID(r.Context())
    ch := eventBroker.Subscribe(householdID)
    defer eventBroker.Unsubscribe(householdID, ch)

    for {
        select {
        case event := <-ch:
            fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, event.Data)
            flusher.Flush()
        case <-r.Context().Done():
            return
        }
    }
}
```

**Client-side (HTMX SSE extension):**
```html
<div hx-ext="sse" sse-connect="/events/hub">
    <!-- Update notification badge when "notification" event fires -->
    <span sse-swap="notification" hx-target="#notification-count"></span>

    <!-- Update active shift display -->
    <div sse-swap="shift-change" hx-target="#active-shift"></div>

    <!-- Update task list when someone completes a task -->
    <div sse-swap="task-update" hx-target="#task-list"></div>
</div>
```

**Event types used in this app:**
- `notification` — new notification badge count
- `shift-change` — someone started/ended a shift
- `task-update` — task completed/created/modified
- `medication-alert` — medication due or overdue
- `emergency` — panic button activated (highest priority)
- `message` — new message in the care recipient feed
- `handoff` — shift handoff submitted

---

### 11. Build & Release (GoReleaser + Docker)

**GoReleaser config (.goreleaser.yaml):**
```yaml
builds:
  - env:
      - CGO_ENABLED=0
    goos:
      - linux
      - darwin
      - windows
    goarch:
      - amd64
      - arm64
    ldflags:
      - -s -w -X main.version={{.Version}}

dockers:
  - image_templates:
      - "ghcr.io/yourorg/caregiver-hub:{{ .Tag }}"
      - "ghcr.io/yourorg/caregiver-hub:latest"
    goarch: amd64
    dockerfile: Dockerfile
  - image_templates:
      - "ghcr.io/yourorg/caregiver-hub:{{ .Tag }}-arm64"
    goarch: arm64
    dockerfile: Dockerfile
```

**Dockerfile (multi-stage):**
```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o caregiver-hub ./cmd/server

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /app/caregiver-hub .
EXPOSE 8080
VOLUME /app/data
CMD ["./caregiver-hub"]
```

**Docker Compose for end users (docker-compose.yml):**
```yaml
version: "3.8"
services:
  caregiver-hub:
    image: ghcr.io/yourorg/caregiver-hub:latest
    ports:
      - "8080:8080"
    volumes:
      - ./data:/app/data
    environment:
      - SESSION_SECRET=change-me-to-a-random-string
      - DB_PATH=/app/data/caregiver.db
    restart: unless-stopped
```

---

### 12. Static Asset Embedding

All static assets (CSS, JS, images, fonts) are embedded into the Go binary using `embed.FS`. No external file serving required.

```go
import "embed"

//go:embed static/*
var staticFiles embed.FS

func main() {
    mux := http.NewServeMux()
    mux.Handle("GET /static/", http.FileServer(http.FS(staticFiles)))
    // ... other routes
}
```

**Static asset inventory:**
- `static/css/styles.css` — generated by Tailwind CLI
- `static/js/htmx.min.js` — HTMX library
- `static/js/htmx-sse.js` — HTMX SSE extension
- `static/img/` — app icons, default avatars, PWA icons
- `static/manifest.json` — PWA manifest

---

## Project Structure

```
caregiver-hub/
├── cmd/
│   └── server/
│       └── main.go                 # Entry point: starts server, runs migrations
├── internal/
│   ├── auth/                       # Authentication, sessions, middleware
│   ├── hub/                        # Hub/dashboard handlers and templates
│   ├── care/                       # Care recipient management
│   ├── scheduling/                 # Calendar, shifts, availability
│   ├── tasks/                      # Task management, checklists
│   ├── medications/                # Medication tracking, administration logs
│   ├── carelog/                    # Daily journal, care logging
│   ├── handoff/                    # Shift handoff flow
│   ├── communication/              # Messages, announcements
│   ├── notifications/              # Notification center, delivery channels
│   ├── emergency/                  # Emergency mode, panic button
│   ├── contacts/                   # Provider/contact directory
│   ├── routine/                    # Daily routine & preferences reference
│   ├── users/                      # User management, roles, invites
│   ├── household/                  # Household/multi-tenancy logic
│   ├── events/                     # SSE event broker
│   └── db/
│       ├── dbgen/                  # sqlc generated code (do not edit)
│       └── queries/                # SQL query files for sqlc
├── components/                     # Shared Templ components (layouts, cards, nav, etc.)
├── db/
│   └── migrations/                 # goose SQL migration files
├── static/                         # CSS, JS, images (embedded into binary)
├── sqlc.yaml                       # sqlc configuration
├── tailwind.config.js              # Tailwind + DaisyUI configuration
├── input.css                       # Tailwind input file
├── .goreleaser.yaml                # Release configuration
├── Dockerfile
├── docker-compose.yml
└── Makefile                        # Dev commands: generate, build, run, test
```

**Each feature package follows the same internal pattern:**
```
internal/tasks/
├── handler.go          # HTTP handlers (thin: parse → service → render)
├── service.go          # Business logic
├── templates.templ     # Templ templates for this feature
├── templates_templ.go  # Generated by templ (do not edit)
└── models.go           # Domain types (if not covered by sqlc generated types)
```

---

## Development Workflow

### Prerequisites
- Go 1.22+
- Templ CLI (`go install github.com/a-h/templ/cmd/templ@latest`)
- sqlc CLI (`go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest`)
- Tailwind CSS standalone CLI (download from GitHub releases)
- goose CLI (`go install github.com/pressly/goose/v3/cmd/goose@latest`) — optional, for manual migration management

### Daily Development Loop
```bash
# Terminal 1: Watch and regenerate templ files
templ generate --watch

# Terminal 2: Watch and regenerate CSS
./tailwindcss -i input.css -o static/css/styles.css --watch

# Terminal 3: Run the app (use air for live reload)
# go install github.com/air-verse/air@latest
air
```

### After Modifying SQL
```bash
# Regenerate Go code from SQL queries
sqlc generate
```

### Makefile Targets
```makefile
.PHONY: generate build run test

generate:           ## Regenerate all generated code
	templ generate
	sqlc generate
	./tailwindcss -i input.css -o static/css/styles.css --minify

build: generate     ## Build the binary
	go build -o bin/caregiver-hub ./cmd/server

run:                ## Run in development mode
	go run ./cmd/server

test:               ## Run all tests
	go test ./...

lint:               ## Run linters
	go vet ./...
	staticcheck ./...
```

---

## PostgreSQL Compatibility Notes

The app defaults to SQLite but should support PostgreSQL for users who want multi-server deployments or already run Postgres.

**Strategy:**
- sqlc supports both SQLite and PostgreSQL
- Use a build tag or runtime config flag to switch drivers
- Abstract the `*sql.DB` connection behind an interface if needed
- SQL dialect differences to watch: `?` (SQLite) vs `$1` (Postgres) for parameters, `RETURNING` syntax, `TEXT` vs `VARCHAR`, date/time functions
- Test against both databases in CI

**For 1.0:** SQLite only. PostgreSQL support can be added post-1.0 by creating parallel query files with Postgres-specific SQL.

---

## Key Dependencies (go.mod)

```
module caregiver-hub

go 1.22

require (
    github.com/a-h/templ            // Templating
    modernc.org/sqlite               // SQLite driver (pure Go)
    github.com/gorilla/sessions      // Session management
    github.com/gorilla/csrf          // CSRF protection
    golang.org/x/crypto              // bcrypt password hashing, argon2 key derivation
    github.com/pressly/goose/v3      // Database migrations
    github.com/google/uuid           // UUID generation for IDs
)
```

Keep dependencies minimal. Every dependency is a maintenance burden for a long-lived open source project.

---

## AI Development Guidelines

When working with an AI assistant on this codebase:

1. **Templates**: Ask the AI to write Templ components. The syntax is Go + HTML and AI tools handle it well. Always specify DaisyUI component classes for consistent styling.

2. **Database changes**: Ask the AI to write the SQL migration first, then the sqlc query, then run `sqlc generate`. The AI should write SQL directly — not Go code that builds SQL strings.

3. **New features**: Follow the package pattern — create a new directory under `internal/` with handler.go, service.go, templates.templ, and models.go.

4. **HTMX interactions**: Describe the interaction you want (e.g., "clicking this button should mark the task complete and update the row without a page reload") and the AI should produce the HTMX attributes on the template and the handler that returns the HTML fragment.

5. **Styling**: Reference DaisyUI component names (card, badge, modal, drawer, etc.) and Tailwind utility classes. The AI should not write custom CSS.

6. **Testing**: Ask for table-driven tests (Go convention). Service layer tests should use an in-memory SQLite database.

7. **Never generate code in `dbgen/`** — that directory is sqlc-generated. Modify the SQL queries in `db/queries/` and regenerate.
