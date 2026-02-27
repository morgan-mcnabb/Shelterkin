# Caregiver Coordinator Hub — Database Schema Design

*This document defines the canonical database schema for the Caregiver Coordinator Hub. It should be provided alongside the Tech Stack document to any AI assistant or developer working on the project.*

---

## Pre-Schema Analysis: Critical Design Decisions

Before defining tables, these architectural decisions need to be locked in. Each one has cascading effects throughout the schema and getting them wrong would require painful migrations later.

### Decision 1: Multi-Tenancy Model — Household Scoping

**Problem:** Multiple families use the same instance (or a single family needs data isolation). Every query must be scoped so users only see their household's data.

**Decision: Household-scoped with foreign keys, enforced at the service layer.**

Every data table includes a `household_id` foreign key. The service layer injects the authenticated user's household ID into every query. This is simpler than row-level security (which SQLite doesn't support anyway) and works identically across SQLite and PostgreSQL.

**Why not user-scoped?** Because the whole point of this app is *shared* access. A task belongs to the household, not to the individual who created it. Multiple users need to read and write the same care recipient's data.

**Why not schema-per-tenant?** Overkill for this use case, doesn't work with SQLite, and makes migrations painful when you have hundreds of households.

**Implication:** Every `SELECT` query in sqlc must include `WHERE household_id = ?`. This is repetitive but explicit, and the service layer handles it automatically. A forgotten `household_id` filter is a data leak — the test suite should verify this.

---

### Decision 2: Primary Key Strategy — ULIDs

**Problem:** Need globally unique IDs that work across SQLite and PostgreSQL, are safe to expose in URLs, and don't leak information about record count or creation order (which auto-increment integers do).

**Decision: ULIDs (Universally Unique Lexicographically Sortable Identifiers) stored as TEXT.**

ULIDs are 26-character strings that are:
- Globally unique (like UUIDs)
- Lexicographically sortable by creation time (unlike UUIDv4)
- URL-safe (no special characters)
- Case-insensitive

The time-sortable property matters because it means `ORDER BY id` gives you chronological order for free, and B-tree indexes on TEXT ULIDs have good locality (unlike random UUIDs which fragment indexes).

```
Example ULID: 01HXYZ1234ABCDEF5678GHIJK
              |------||--------------|
              timestamp   randomness
```

**Go library:** `github.com/oklog/ulid/v2`

**Why not UUIDv4?** Random UUIDs fragment B-tree indexes and aren't sortable. UUIDv7 would also work (it's time-sorted) but has less Go ecosystem support currently.

**Why not auto-increment integers?** They leak information (user #47 knows there are at least 46 others), cause conflicts in distributed scenarios, and are predictable in URLs.

**Implication:** All `id` columns are `TEXT NOT NULL PRIMARY KEY`. All foreign keys referencing them are `TEXT NOT NULL`. This is slightly less space-efficient than integers in SQLite but the tradeoff is worth it.

---

### Decision 3: Encrypted Field Querying

**Problem:** Many sensitive fields (care recipient names, medication names, task descriptions) are encrypted at the application level per the tech stack document. You cannot use SQL `LIKE`, `WHERE name = ?`, or `ORDER BY name` on encrypted columns because the database only sees opaque ciphertext.

**Decision: Encrypted fields get companion search/lookup columns where querying is essential.**

For fields that must be searchable or filterable, store a companion column with either:
- A keyed HMAC hash (for exact-match lookups, like email)
- A lowercase prefix or normalized token set (for search, carefully scoped)

For fields that don't need SQL-level querying (care log narrative, medical notes), only store the encrypted value.

**Which fields need search capability:**

| Field | Search needed? | Strategy |
|---|---|---|
| User email | Yes (login lookup) | HMAC hash in `email_hash` column, encrypted email in `email_enc` |
| Care recipient name | Yes (display, sorting) | Encrypted only — decrypt in service layer and sort in Go. Small dataset per household (rarely >5 recipients) |
| Medication name | Marginal | Encrypted only — filter in Go after decryption. Small dataset per recipient |
| Task title/description | Yes (search) | Store encrypted. Full-text search is post-1.0. For 1.0, filter in Go after decryption. Task lists are per-recipient-per-day, so datasets are small |
| Contact/provider name | Yes (directory search) | Encrypted only — decrypt and filter in Go. Directory is per-household, manageable size |
| Care log entries | Marginal | Encrypted only — chronological access is the primary pattern, not search |
| Message content | Marginal | Encrypted only — chronological feed, not searched |

**Why this works for 1.0:** The datasets that need searching are small per-household. A household might have 3-5 care recipients, 20-50 medications total, 100-200 contacts. Decrypting these into memory and filtering/sorting in Go is fast and avoids the complexity of searchable encryption schemes. If full-text search becomes critical post-1.0, consider adding an encrypted search index using something like blind indexing.

**Implication:** Column naming convention — encrypted fields use an `_enc` suffix. HMAC lookup columns use a `_hash` suffix. This makes it immediately obvious in the schema which columns contain ciphertext.

```sql
-- Example: users table
email_enc    TEXT NOT NULL,  -- AES-256-GCM encrypted email
email_hash   TEXT NOT NULL,  -- HMAC-SHA256 of normalized email (for login lookup)
```

---

### Decision 4: Temporal Data — Timestamps and Time Zones

**Problem:** Caregivers may be in different time zones. Medication schedules, shift times, and appointments need to be unambiguous.

**Decision: Store all timestamps as UTC in ISO 8601 format. Store user time zone preference in their profile. Convert to local time in the service/template layer.**

SQLite stores timestamps as TEXT. Using ISO 8601 with explicit UTC (`2025-01-15T14:30:00Z`) ensures:
- No ambiguity about when something happened
- Correct chronological sorting (ISO 8601 sorts lexicographically)
- Clean migration path to PostgreSQL (which has native `TIMESTAMPTZ`)

**Time-of-day fields** (medication schedules, routine times) are stored as `TIME` strings (`"08:00"`, `"14:30"`) without a date component, interpreted in the care recipient's local time zone.

**Implication:** The `users` table has a `timezone` column (e.g., `"America/New_York"`). The `care_recipients` table also has a `timezone` column (since the recipient may be in a different timezone than some caregivers). All display logic converts UTC timestamps to the viewing user's timezone.

---

### Decision 5: Soft Deletes vs Hard Deletes

**Problem:** When a user deletes a care recipient, medication, or task, should the data actually be removed from the database?

**Decision: Soft deletes for core entities, hard deletes for transient data.**

Soft-deleted records have a `deleted_at` timestamp set instead of being removed. This is important because:
- Audit trail: you need to know what medications a care recipient *was* on, even if they've been discontinued
- Accidental deletion recovery
- Referential integrity: a care log entry that references a deleted medication shouldn't break
- Legal/medical record keeping

**Soft delete entities:**
- Care recipients
- Users / household members
- Medications
- Contacts / providers
- Care log entries

**Hard delete entities:**
- Notification records (transient, high volume)
- Session data
- Expired emergency tokens
- Draft messages (if unsent)

**Implication:** Every soft-deletable table has a `deleted_at TIMESTAMP` column (NULL = active). Every query on these tables must include `WHERE deleted_at IS NULL` unless explicitly requesting archived records. sqlc queries should have both variants:

```sql
-- name: GetActiveMedications :many
SELECT * FROM medications WHERE care_recipient_id = ? AND deleted_at IS NULL;

-- name: GetAllMedicationsIncludingDeleted :many
SELECT * FROM medications WHERE care_recipient_id = ? ORDER BY deleted_at NULLS FIRST;
```

---

### Decision 6: Recurring Events Architecture

**Problem:** Tasks, medications, and appointments can be recurring (daily, weekly, specific days, etc.). This is notoriously tricky to model correctly.

**Decision: Template + instance pattern.**

A recurring item has two representations:
1. **Template** (the definition): "Give Mom 10mg Lisinopril every day at 8am"
2. **Instance** (each occurrence): "Give Mom 10mg Lisinopril on 2025-03-15 at 8am — completed by Sarah at 8:12am"

Templates define the recurrence rule. Instances are generated from templates, either eagerly (generate the next N days on a schedule) or lazily (generate today's instances on first access each day). Instances can be modified individually without affecting the template (e.g., "skip Tuesday's dose per doctor's orders").

**Why not just store the recurrence rule and calculate on the fly?** Because instances need to track completion state, who completed them, notes, and modifications. You need actual rows to attach that data to.

**Recurrence rule storage:** Store as a simple structured JSON string in the template. Don't over-engineer with full RFC 5545 (iCal) recurrence rules for 1.0.

```json
{
  "frequency": "daily",
  "times": ["08:00", "20:00"],
  "days_of_week": null
}

{
  "frequency": "weekly",
  "times": ["09:00"],
  "days_of_week": ["mon", "wed", "fri"]
}

{
  "frequency": "monthly",
  "times": ["10:00"],
  "day_of_month": 15
}
```

**Instance generation:** A background goroutine runs daily (or on-demand) to generate instances for the upcoming period (default: 7 days ahead). This keeps queries simple — "show me today's tasks" is just a `SELECT` with a date filter on concrete rows.

**Implication:** Tables like `tasks` and `medication_administrations` have a nullable `template_id` foreign key. If set, the record was generated from a template. If null, it's a one-off.

---

### Decision 7: Notification Storage Architecture

**Problem:** Notifications are high-volume, cross-cutting (every feature generates them), and need multiple delivery states (in-app read/unread, email sent/pending, push delivered/failed).

**Decision: Single notification table with delivery tracking.**

Each notification has:
- A type (enum-like string) that determines rendering and routing
- A reference to the source entity (polymorphic: task, medication, message, etc.)
- Delivery state per channel (in-app, email, push)
- Priority level that determines which channels are used

**Polymorphic reference approach:** Use `source_type` + `source_id` columns rather than separate foreign keys for each entity type. This avoids adding a column to the notifications table every time you add a new feature.

```sql
source_type TEXT NOT NULL,  -- 'task', 'medication', 'message', 'incident', 'shift', etc.
source_id   TEXT NOT NULL,  -- ULID of the source entity
```

**Implication:** Notifications can't have a foreign key constraint on the source entity (since it could be any table). The service layer is responsible for ensuring referential integrity. This is an acceptable tradeoff for the flexibility it provides.

---

## Complete Schema

### Foundational Tables

```sql
-- ============================================================================
-- HOUSEHOLDS
-- The top-level organizational unit. All data is scoped to a household.
-- ============================================================================

CREATE TABLE households (
    id              TEXT NOT NULL PRIMARY KEY,  -- ULID
    name_enc        TEXT NOT NULL,              -- Encrypted household name
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    encryption_salt TEXT NOT NULL,              -- Salt used for key derivation (not sensitive)
    onboarding_progress TEXT NOT NULL DEFAULT '{}',  -- JSON tracking which setup steps are completed:
                                                      -- {"profile_created":true,"first_recipient":true,
                                                      --  "members_invited":false,"schedule_setup":false,
                                                      --  "medications_added":false,"completed":false}
    settings        TEXT NOT NULL DEFAULT '{}'  -- JSON: default timezone, notification prefs, etc.
);

-- ============================================================================
-- USERS
-- People who log into the app. Belong to exactly one household.
-- ============================================================================

CREATE TABLE users (
    id              TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id    TEXT NOT NULL REFERENCES households(id),
    email_enc       TEXT NOT NULL,              -- Encrypted email
    email_hash      TEXT NOT NULL UNIQUE,       -- HMAC hash for login lookup
    password_hash   TEXT,                       -- bcrypt hash (NULL for OIDC-only users)
    display_name_enc TEXT NOT NULL,             -- Encrypted display name
    role            TEXT NOT NULL DEFAULT 'member',  -- 'admin', 'member', 'caregiver', 'readonly'
    auth_provider   TEXT NOT NULL DEFAULT 'local',   -- 'local', 'oidc'
    timezone        TEXT NOT NULL DEFAULT 'America/New_York',
    notification_prefs TEXT NOT NULL DEFAULT '{}',   -- JSON: channels, quiet hours, etc.
    totp_secret_enc TEXT,                       -- Encrypted TOTP secret (NULL if 2FA not enabled)
    last_login_at   TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    deleted_at      TEXT                        -- Soft delete
);

CREATE INDEX idx_users_household ON users(household_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_users_email_hash ON users(email_hash);

-- ============================================================================
-- SESSIONS
-- Server-side session storage (alternative to cookie-only sessions for
-- session revocation and management).
-- ============================================================================

CREATE TABLE sessions (
    id              TEXT NOT NULL PRIMARY KEY,  -- Session token (random, not ULID)
    user_id         TEXT NOT NULL REFERENCES users(id),
    household_id    TEXT NOT NULL REFERENCES households(id),
    ip_address      TEXT,
    user_agent      TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    expires_at      TEXT NOT NULL,
    last_active_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);

-- ============================================================================
-- LOGIN ATTEMPTS
-- Tracks failed login attempts for rate limiting and brute-force protection.
-- ============================================================================

CREATE TABLE login_attempts (
    id              TEXT NOT NULL PRIMARY KEY,  -- ULID
    email_hash      TEXT NOT NULL,              -- HMAC hash of attempted email (matches users.email_hash)
    ip_address      TEXT NOT NULL,
    succeeded        INTEGER NOT NULL DEFAULT 0,
    attempted_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- Index for checking recent failures by email (account lockout)
CREATE INDEX idx_login_attempts_email ON login_attempts(email_hash, attempted_at);
-- Index for checking recent failures by IP (IP-based rate limiting)
CREATE INDEX idx_login_attempts_ip ON login_attempts(ip_address, attempted_at);

-- ============================================================================
-- INVITES
-- Pending invitations to join a household.
-- ============================================================================

CREATE TABLE invites (
    id              TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id    TEXT NOT NULL REFERENCES households(id),
    invited_by      TEXT NOT NULL REFERENCES users(id),
    email_enc       TEXT,                       -- Encrypted email (NULL for link-only invites)
    email_hash      TEXT,                       -- HMAC hash for dedup
    token_hash      TEXT NOT NULL UNIQUE,       -- SHA-256 hash of invite token
    role            TEXT NOT NULL DEFAULT 'member',
    expires_at      TEXT NOT NULL,
    accepted_at     TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_invites_token ON invites(token_hash);
CREATE INDEX idx_invites_household ON invites(household_id);
```

### Care Recipient Tables

```sql
-- ============================================================================
-- CARE RECIPIENTS
-- The people being cared for. A household can have multiple.
-- ============================================================================

CREATE TABLE care_recipients (
    id                      TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id            TEXT NOT NULL REFERENCES households(id),
    name_enc                TEXT NOT NULL,              -- Encrypted name
    date_of_birth_enc       TEXT,                       -- Encrypted DOB
    blood_type_enc          TEXT,                       -- Encrypted blood type
    photo_path              TEXT,                       -- Path to encrypted photo file
    timezone                TEXT NOT NULL DEFAULT 'America/New_York',
    created_at              TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at              TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    deleted_at              TEXT
);

CREATE INDEX idx_care_recipients_household ON care_recipients(household_id) WHERE deleted_at IS NULL;

-- ============================================================================
-- CARE RECIPIENT MEDICAL INFO
-- Separated from the main profile because it changes independently and
-- has different access control implications.
-- ============================================================================

CREATE TABLE care_recipient_medical (
    id                      TEXT NOT NULL PRIMARY KEY,  -- ULID
    care_recipient_id       TEXT NOT NULL UNIQUE REFERENCES care_recipients(id),
    household_id            TEXT NOT NULL REFERENCES households(id),
    conditions_enc          TEXT NOT NULL DEFAULT '',   -- Encrypted JSON array of conditions
    allergies_enc           TEXT NOT NULL DEFAULT '',   -- Encrypted JSON array of allergies
    dietary_restrictions_enc TEXT NOT NULL DEFAULT '',  -- Encrypted text
    insurance_info_enc      TEXT NOT NULL DEFAULT '',   -- Encrypted JSON (provider, policy #, group #, phone)
    notes_enc               TEXT NOT NULL DEFAULT '',   -- Encrypted free-form medical notes
    updated_at              TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_by              TEXT REFERENCES users(id)
);

-- ============================================================================
-- CARE RECIPIENT DAILY ROUTINE & PREFERENCES
-- The "how Mom likes things" reference document.
-- ============================================================================

CREATE TABLE care_recipient_routines (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    care_recipient_id   TEXT NOT NULL REFERENCES care_recipients(id),
    household_id        TEXT NOT NULL REFERENCES households(id),
    content_enc         TEXT NOT NULL,              -- Encrypted markdown/structured text
    version             INTEGER NOT NULL DEFAULT 1, -- Increment on each edit
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_by          TEXT REFERENCES users(id)
);

CREATE INDEX idx_routines_recipient ON care_recipient_routines(care_recipient_id);

-- ============================================================================
-- EMERGENCY PROFILES
-- Pre-compiled emergency info for paramedic-facing view.
-- Denormalized intentionally for fast, auth-free access.
-- ============================================================================

CREATE TABLE emergency_profiles (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    care_recipient_id   TEXT NOT NULL UNIQUE REFERENCES care_recipients(id),
    household_id        TEXT NOT NULL REFERENCES households(id),
    summary_enc         TEXT NOT NULL,              -- Encrypted: name, DOB, blood type, conditions,
                                                    -- allergies, current medications, emergency contacts
                                                    -- All in one encrypted blob for fast single-read access
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- ============================================================================
-- EMERGENCY ACCESS TOKENS
-- Time-limited, revocable tokens for the shareable emergency URL.
-- ============================================================================

CREATE TABLE emergency_tokens (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    care_recipient_id   TEXT NOT NULL REFERENCES care_recipients(id),
    household_id        TEXT NOT NULL REFERENCES households(id),
    token_hash          TEXT NOT NULL UNIQUE,       -- SHA-256 hash of the URL token
    created_by          TEXT NOT NULL REFERENCES users(id),
    expires_at          TEXT NOT NULL,
    revoked_at          TEXT,
    last_accessed_at    TEXT,
    access_count        INTEGER NOT NULL DEFAULT 0,
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_emergency_tokens_hash ON emergency_tokens(token_hash) WHERE revoked_at IS NULL;

-- ============================================================================
-- EMERGENCY EVENTS
-- Records each panic button activation as a first-class entity.
-- Other records (notifications, care logs, debrief) reference this.
-- ============================================================================

CREATE TABLE emergency_events (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id        TEXT NOT NULL REFERENCES households(id),
    care_recipient_id   TEXT NOT NULL REFERENCES care_recipients(id),
    activated_by        TEXT NOT NULL REFERENCES users(id),
    activated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    deactivated_by      TEXT REFERENCES users(id),
    deactivated_at      TEXT,
    reason_enc          TEXT,                       -- Encrypted: why was emergency activated (fall, chest pain, etc.)
    resolution_enc      TEXT,                       -- Encrypted: what happened / how was it resolved
    debrief_notes_enc   TEXT,                       -- Encrypted: post-emergency debrief
    severity            TEXT NOT NULL DEFAULT 'high',  -- 'high', 'critical'
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_emergency_events_recipient ON emergency_events(care_recipient_id, activated_at);
```

### Caregiver Assignment & Scheduling Tables

```sql
-- ============================================================================
-- CAREGIVER ASSIGNMENTS
-- Links caregivers (especially professional/hired) to specific care recipients.
-- Members and admins implicitly have access to all recipients in the household.
-- ============================================================================

CREATE TABLE caregiver_assignments (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id        TEXT NOT NULL REFERENCES households(id),
    user_id             TEXT NOT NULL REFERENCES users(id),
    care_recipient_id   TEXT NOT NULL REFERENCES care_recipients(id),
    assigned_by         TEXT NOT NULL REFERENCES users(id),
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    revoked_at          TEXT,
    UNIQUE(user_id, care_recipient_id)
);

CREATE INDEX idx_assignments_user ON caregiver_assignments(user_id) WHERE revoked_at IS NULL;
CREATE INDEX idx_assignments_recipient ON caregiver_assignments(care_recipient_id) WHERE revoked_at IS NULL;

-- ============================================================================
-- SHIFTS
-- Scheduled caregiving time blocks.
-- ============================================================================

CREATE TABLE shifts (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id        TEXT NOT NULL REFERENCES households(id),
    care_recipient_id   TEXT NOT NULL REFERENCES care_recipients(id),
    assigned_to         TEXT REFERENCES users(id),  -- NULL = unassigned
    starts_at           TEXT NOT NULL,              -- UTC timestamp
    ends_at             TEXT NOT NULL,              -- UTC timestamp
    actual_start_at     TEXT,                       -- When caregiver actually clocked in
    actual_end_at       TEXT,                       -- When caregiver actually clocked out
    status              TEXT NOT NULL DEFAULT 'scheduled',  -- 'scheduled', 'active', 'completed', 'missed', 'swapped'
    template_id         TEXT REFERENCES shift_templates(id),
    notes_enc           TEXT,                       -- Encrypted shift notes
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_shifts_recipient_date ON shifts(care_recipient_id, starts_at);
CREATE INDEX idx_shifts_assigned ON shifts(assigned_to, starts_at);
CREATE INDEX idx_shifts_household_date ON shifts(household_id, starts_at);

-- ============================================================================
-- SHIFT TEMPLATES
-- Recurrence definitions for repeating shifts.
-- ============================================================================

CREATE TABLE shift_templates (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id        TEXT NOT NULL REFERENCES households(id),
    care_recipient_id   TEXT NOT NULL REFERENCES care_recipients(id),
    assigned_to         TEXT REFERENCES users(id),
    name_enc            TEXT NOT NULL,              -- Encrypted template name ("Mom's weekday mornings")
    recurrence_rule     TEXT NOT NULL,              -- JSON recurrence definition
    shift_start_time    TEXT NOT NULL,              -- Time of day ("08:00")
    shift_end_time      TEXT NOT NULL,              -- Time of day ("16:00")
    is_active           INTEGER NOT NULL DEFAULT 1,
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- ============================================================================
-- SHIFT SWAP REQUESTS
-- ============================================================================

CREATE TABLE shift_swap_requests (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id        TEXT NOT NULL REFERENCES households(id),
    shift_id            TEXT NOT NULL REFERENCES shifts(id),
    requested_by        TEXT NOT NULL REFERENCES users(id),
    requested_to        TEXT REFERENCES users(id),  -- NULL = open request to anyone
    status              TEXT NOT NULL DEFAULT 'pending',  -- 'pending', 'accepted', 'declined', 'cancelled'
    message_enc         TEXT,                       -- Encrypted message
    responded_at        TEXT,
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- ============================================================================
-- AVAILABILITY
-- Caregivers mark when they're available or unavailable.
-- ============================================================================

CREATE TABLE availability (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id        TEXT NOT NULL REFERENCES households(id),
    user_id             TEXT NOT NULL REFERENCES users(id),
    available_date      TEXT NOT NULL,              -- Date (YYYY-MM-DD)
    start_time          TEXT,                       -- NULL = all day
    end_time            TEXT,                       -- NULL = all day
    is_available        INTEGER NOT NULL DEFAULT 1, -- 1 = available, 0 = blocked off
    note_enc            TEXT,                       -- Encrypted reason
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_availability_user_date ON availability(user_id, available_date);
```

### Task Management Tables

```sql
-- ============================================================================
-- TASK TEMPLATES
-- Reusable definitions for recurring tasks.
-- ============================================================================

CREATE TABLE task_templates (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id        TEXT NOT NULL REFERENCES households(id),
    care_recipient_id   TEXT NOT NULL REFERENCES care_recipients(id),
    title_enc           TEXT NOT NULL,              -- Encrypted task title
    description_enc     TEXT,                       -- Encrypted description
    category            TEXT NOT NULL DEFAULT 'general',  -- 'medical', 'household', 'hygiene',
                                                          -- 'nutrition', 'social', 'exercise', 'errand', 'general'
    priority            TEXT NOT NULL DEFAULT 'normal',   -- 'low', 'normal', 'high', 'critical'
    recurrence_rule     TEXT NOT NULL,              -- JSON recurrence definition
    default_assigned_to TEXT REFERENCES users(id),  -- Default assignee
    is_active           INTEGER NOT NULL DEFAULT 1,
    created_by          TEXT NOT NULL REFERENCES users(id),
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_task_templates_recipient ON task_templates(care_recipient_id) WHERE is_active = 1;

-- ============================================================================
-- TASKS
-- Individual task instances (generated from templates or created ad-hoc).
-- ============================================================================

CREATE TABLE tasks (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id        TEXT NOT NULL REFERENCES households(id),
    care_recipient_id   TEXT NOT NULL REFERENCES care_recipients(id),
    template_id         TEXT REFERENCES task_templates(id),  -- NULL = ad-hoc task
    title_enc           TEXT NOT NULL,              -- Encrypted title
    description_enc     TEXT,                       -- Encrypted description
    category            TEXT NOT NULL DEFAULT 'general',
    priority            TEXT NOT NULL DEFAULT 'normal',
    assigned_to         TEXT REFERENCES users(id),
    due_at              TEXT NOT NULL,              -- UTC timestamp
    completed           INTEGER NOT NULL DEFAULT 0,
    completed_at        TEXT,
    completed_by        TEXT REFERENCES users(id),
    skipped             INTEGER NOT NULL DEFAULT 0, -- Task was intentionally skipped
    skipped_reason_enc  TEXT,                       -- Encrypted reason for skipping
    notes_enc           TEXT,                       -- Encrypted completion notes
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    deleted_at          TEXT
);

CREATE INDEX idx_tasks_recipient_due ON tasks(care_recipient_id, due_at) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_assigned_due ON tasks(assigned_to, due_at) WHERE deleted_at IS NULL AND completed = 0;
CREATE INDEX idx_tasks_household_due ON tasks(household_id, due_at) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_template ON tasks(template_id);
```

### Medication Management Tables

```sql
-- ============================================================================
-- MEDICATIONS
-- Master list of medications for a care recipient.
-- ============================================================================

CREATE TABLE medications (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id        TEXT NOT NULL REFERENCES households(id),
    care_recipient_id   TEXT NOT NULL REFERENCES care_recipients(id),
    name_enc            TEXT NOT NULL,              -- Encrypted medication name
    dosage_enc          TEXT NOT NULL,              -- Encrypted dosage (e.g., "10mg")
    form_enc            TEXT,                       -- Encrypted form (tablet, liquid, injection, etc.)
    instructions_enc    TEXT,                       -- Encrypted instructions (e.g., "take with food")
    prescribing_doctor_enc TEXT,                    -- Encrypted doctor name
    pharmacy_enc        TEXT,                       -- Encrypted pharmacy info
    is_prn              INTEGER NOT NULL DEFAULT 0, -- 1 = as-needed medication
    prn_reason_enc      TEXT,                       -- Encrypted: what symptoms trigger PRN use
    interaction_notes_enc TEXT,                     -- Encrypted interaction warnings
    refill_quantity     INTEGER,                    -- Number of doses per refill
    current_supply      INTEGER,                    -- Current supply count (updated on administration)
    low_supply_threshold INTEGER DEFAULT 7,         -- Alert when supply drops below this
    started_at          TEXT,                       -- When this medication was started
    discontinued_at     TEXT,                       -- When this medication was stopped
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    deleted_at          TEXT
);

CREATE INDEX idx_medications_recipient ON medications(care_recipient_id) WHERE deleted_at IS NULL AND discontinued_at IS NULL;

-- ============================================================================
-- MEDICATION SCHEDULES
-- When each medication should be administered.
-- A medication can have multiple schedule entries (e.g., morning and evening).
-- ============================================================================

CREATE TABLE medication_schedules (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    medication_id       TEXT NOT NULL REFERENCES medications(id),
    household_id        TEXT NOT NULL REFERENCES households(id),
    time_of_day         TEXT NOT NULL,              -- "08:00", "20:00" (local to care recipient timezone)
    days_of_week        TEXT,                       -- JSON array: ["mon","tue","wed","thu","fri","sat","sun"] or NULL for daily
    is_active           INTEGER NOT NULL DEFAULT 1,
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_med_schedules_medication ON medication_schedules(medication_id) WHERE is_active = 1;

-- ============================================================================
-- MEDICATION ADMINISTRATIONS
-- Log of each time a medication was given (or missed).
-- This is the source of truth for medication adherence.
-- ============================================================================

CREATE TABLE medication_administrations (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id        TEXT NOT NULL REFERENCES households(id),
    care_recipient_id   TEXT NOT NULL REFERENCES care_recipients(id),
    medication_id       TEXT NOT NULL REFERENCES medications(id),
    schedule_id         TEXT REFERENCES medication_schedules(id),  -- NULL for PRN administrations
    scheduled_at        TEXT,                       -- When it was supposed to be given (NULL for PRN)
    administered_at     TEXT,                       -- When it was actually given (NULL if missed)
    administered_by     TEXT REFERENCES users(id),
    status              TEXT NOT NULL DEFAULT 'pending',  -- 'pending', 'given', 'missed', 'skipped', 'refused'
    notes_enc           TEXT,                       -- Encrypted notes (e.g., "patient complained of nausea")
    prn_symptom_enc     TEXT,                       -- Encrypted: what symptom triggered PRN administration
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_med_admin_recipient_date ON medication_administrations(care_recipient_id, scheduled_at);
CREATE INDEX idx_med_admin_medication ON medication_administrations(medication_id, scheduled_at);
CREATE INDEX idx_med_admin_status ON medication_administrations(household_id, status, scheduled_at)
    WHERE status IN ('pending', 'missed');

-- ============================================================================
-- MEDICATION SUPPLY LOG
-- Tracks every change to a medication's supply count for auditability.
-- If the current_supply on the medications table ever gets out of sync,
-- this log is the source of truth for reconciliation.
-- ============================================================================

CREATE TABLE medication_supply_log (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id        TEXT NOT NULL REFERENCES households(id),
    medication_id       TEXT NOT NULL REFERENCES medications(id),
    change_type         TEXT NOT NULL,              -- 'administered', 'refilled', 'adjusted', 'wasted', 'correction'
    quantity_change     INTEGER NOT NULL,           -- Positive for additions (refill), negative for usage
    quantity_after      INTEGER NOT NULL,           -- Running total after this change
    administration_id   TEXT REFERENCES medication_administrations(id),  -- Link to administration if applicable
    notes_enc           TEXT,                       -- Encrypted notes (e.g., "Refilled at CVS", "Dropped and wasted 2 tablets")
    logged_by           TEXT NOT NULL REFERENCES users(id),
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_supply_log_medication ON medication_supply_log(medication_id, created_at);
```

### Care Logging Tables

```sql
-- ============================================================================
-- CARE LOG ENTRIES
-- Daily journal and structured observations.
-- ============================================================================

CREATE TABLE care_log_entries (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id        TEXT NOT NULL REFERENCES households(id),
    care_recipient_id   TEXT NOT NULL REFERENCES care_recipients(id),
    logged_by           TEXT NOT NULL REFERENCES users(id),
    entry_type          TEXT NOT NULL DEFAULT 'note',  -- 'note', 'vitals', 'meal', 'mood',
                                                       -- 'sleep', 'pain', 'bathroom', 'activity', 'incident'
    content_enc         TEXT NOT NULL,              -- Encrypted narrative/notes
    structured_data_enc TEXT,                       -- Encrypted JSON for type-specific structured data:
                                                    -- vitals: {"bp":"120/80","temp":"98.6","weight":"150","pulse":"72"}
                                                    -- meal: {"meal_type":"lunch","amount":"full","description":"..."}
                                                    -- mood: {"level":"good","notes":"..."}
                                                    -- sleep: {"hours":7,"quality":"restless","notes":"..."}
                                                    -- pain: {"level":3,"location":"lower back","notes":"..."}
    logged_at           TEXT NOT NULL,              -- When the observation was made (may differ from created_at)
    shift_id            TEXT REFERENCES shifts(id), -- Which shift this was logged during
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    deleted_at          TEXT
);

CREATE INDEX idx_care_log_recipient_date ON care_log_entries(care_recipient_id, logged_at) WHERE deleted_at IS NULL;
CREATE INDEX idx_care_log_type ON care_log_entries(care_recipient_id, entry_type, logged_at) WHERE deleted_at IS NULL;

-- ============================================================================
-- CARE LOG TAGS
-- Tagging system for filtering and pattern spotting.
-- ============================================================================

CREATE TABLE care_log_tags (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id        TEXT NOT NULL REFERENCES households(id),
    name                TEXT NOT NULL,              -- Tag name (not encrypted — tags are short, categorical)
    color               TEXT,                       -- Hex color for display
    UNIQUE(household_id, name)
);

CREATE TABLE care_log_entry_tags (
    care_log_entry_id   TEXT NOT NULL REFERENCES care_log_entries(id),
    tag_id              TEXT NOT NULL REFERENCES care_log_tags(id),
    PRIMARY KEY (care_log_entry_id, tag_id)
);

-- ============================================================================
-- CARE LOG ATTACHMENTS
-- Photos, documents attached to care log entries.
-- ============================================================================

CREATE TABLE care_log_attachments (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    care_log_entry_id   TEXT NOT NULL REFERENCES care_log_entries(id),
    household_id        TEXT NOT NULL REFERENCES households(id),
    original_filename_enc TEXT NOT NULL,            -- Encrypted original filename
    stored_path         TEXT NOT NULL,              -- Path to encrypted file on disk (UUID-named)
    mime_type           TEXT NOT NULL,
    size_bytes          INTEGER NOT NULL,
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_attachments_entry ON care_log_attachments(care_log_entry_id);
```

### Communication & Handoff Tables

```sql
-- ============================================================================
-- MESSAGES
-- In-app message feed per care recipient.
-- ============================================================================

CREATE TABLE messages (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id        TEXT NOT NULL REFERENCES households(id),
    care_recipient_id   TEXT NOT NULL REFERENCES care_recipients(id),
    author_id           TEXT NOT NULL REFERENCES users(id),
    content_enc         TEXT NOT NULL,              -- Encrypted message content
    is_pinned           INTEGER NOT NULL DEFAULT 0,
    is_announcement     INTEGER NOT NULL DEFAULT 0,
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    deleted_at          TEXT
);

CREATE INDEX idx_messages_recipient ON messages(care_recipient_id, created_at) WHERE deleted_at IS NULL;
CREATE INDEX idx_messages_pinned ON messages(care_recipient_id) WHERE is_pinned = 1 AND deleted_at IS NULL;

-- ============================================================================
-- MESSAGE MENTIONS
-- @mention tracking for targeted notifications.
-- ============================================================================

CREATE TABLE message_mentions (
    message_id          TEXT NOT NULL REFERENCES messages(id),
    mentioned_user_id   TEXT NOT NULL REFERENCES users(id),
    PRIMARY KEY (message_id, mentioned_user_id)
);

-- ============================================================================
-- MESSAGE READ RECEIPTS
-- Track who has read each message (critical messages only, to avoid bloat).
-- ============================================================================

CREATE TABLE message_read_receipts (
    message_id          TEXT NOT NULL REFERENCES messages(id),
    user_id             TEXT NOT NULL REFERENCES users(id),
    read_at             TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    PRIMARY KEY (message_id, user_id)
);

-- ============================================================================
-- SHIFT HANDOFFS
-- Structured handoff from outgoing to incoming caregiver.
-- ============================================================================

CREATE TABLE shift_handoffs (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id        TEXT NOT NULL REFERENCES households(id),
    care_recipient_id   TEXT NOT NULL REFERENCES care_recipients(id),
    shift_id            TEXT NOT NULL REFERENCES shifts(id),
    outgoing_user_id    TEXT NOT NULL REFERENCES users(id),
    incoming_user_id    TEXT REFERENCES users(id),  -- NULL if next caregiver hasn't started yet

    -- Structured handoff fields (all encrypted)
    mood_enc            TEXT,                       -- How was their mood/demeanor?
    meals_enc           TEXT,                       -- What did they eat?
    medications_enc     TEXT,                       -- Medication notes (beyond what's in the log)
    incidents_enc       TEXT,                       -- Anything unusual happen?
    pending_tasks_enc   TEXT,                       -- What still needs to be done?
    pain_level          INTEGER,                    -- 0-10 (not encrypted — needed for trending)
    sleep_quality_enc   TEXT,                       -- How did they sleep?
    bathroom_enc        TEXT,                       -- Bowel/bladder notes
    visitors_enc        TEXT,                       -- Who visited?
    freeform_notes_enc  TEXT,                       -- Anything else

    acknowledged_at     TEXT,                       -- When incoming caregiver confirmed receipt
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_handoffs_recipient ON shift_handoffs(care_recipient_id, created_at);
CREATE INDEX idx_handoffs_shift ON shift_handoffs(shift_id);
```

### Contact Directory Tables

```sql
-- ============================================================================
-- CONTACTS
-- Healthcare providers, pharmacies, insurance, personal contacts.
-- ============================================================================

CREATE TABLE contacts (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id        TEXT NOT NULL REFERENCES households(id),
    category            TEXT NOT NULL DEFAULT 'other',  -- 'doctor', 'specialist', 'pharmacy',
                                                        -- 'insurance', 'home_health', 'personal',
                                                        -- 'emergency', 'neighbor', 'other'
    name_enc            TEXT NOT NULL,              -- Encrypted name
    organization_enc    TEXT,                       -- Encrypted organization/practice name
    phone_enc           TEXT,                       -- Encrypted phone number
    email_enc           TEXT,                       -- Encrypted email
    address_enc         TEXT,                       -- Encrypted address
    notes_enc           TEXT,                       -- Encrypted notes ("ask for nurse Janet")
    is_favorite         INTEGER NOT NULL DEFAULT 0,
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    deleted_at          TEXT
);

CREATE INDEX idx_contacts_household ON contacts(household_id, category) WHERE deleted_at IS NULL;
CREATE INDEX idx_contacts_favorites ON contacts(household_id) WHERE is_favorite = 1 AND deleted_at IS NULL;

-- ============================================================================
-- DOCUMENTS
-- General-purpose document storage for care recipient profiles.
-- Medical records, legal documents (power of attorney, advance directives),
-- insurance cards, discharge summaries, etc.
-- Separate from care_log_attachments which are tied to specific log entries.
-- ============================================================================

CREATE TABLE documents (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id        TEXT NOT NULL REFERENCES households(id),
    care_recipient_id   TEXT NOT NULL REFERENCES care_recipients(id),
    uploaded_by         TEXT NOT NULL REFERENCES users(id),
    title_enc           TEXT NOT NULL,              -- Encrypted document title
    description_enc     TEXT,                       -- Encrypted description/notes
    category            TEXT NOT NULL DEFAULT 'other',  -- 'medical_record', 'legal', 'insurance',
                                                        -- 'discharge_summary', 'lab_results',
                                                        -- 'imaging', 'prescription', 'other'
    original_filename_enc TEXT NOT NULL,            -- Encrypted original filename
    stored_path         TEXT NOT NULL,              -- Path to encrypted file on disk (UUID-named)
    mime_type           TEXT NOT NULL,
    size_bytes          INTEGER NOT NULL,
    document_date       TEXT,                       -- Date of the document itself (e.g., lab result date)
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    deleted_at          TEXT
);

CREATE INDEX idx_documents_recipient ON documents(care_recipient_id, category) WHERE deleted_at IS NULL;

-- ============================================================================
-- CONTACT-CARE RECIPIENT LINKS
-- Which contacts are associated with which care recipients.
-- A doctor might be linked to multiple recipients in the same household.
-- ============================================================================

CREATE TABLE contact_care_recipient_links (
    contact_id          TEXT NOT NULL REFERENCES contacts(id),
    care_recipient_id   TEXT NOT NULL REFERENCES care_recipients(id),
    relationship_enc    TEXT,                       -- Encrypted: "cardiologist", "primary care", "neighbor", etc.
    PRIMARY KEY (contact_id, care_recipient_id)
);
```

### Notification Tables

```sql
-- ============================================================================
-- NOTIFICATIONS
-- Unified notification system across all features.
-- ============================================================================

CREATE TABLE notifications (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id        TEXT NOT NULL REFERENCES households(id),
    recipient_user_id   TEXT NOT NULL REFERENCES users(id),  -- Who receives this notification
    source_type         TEXT NOT NULL,              -- 'task', 'medication', 'message', 'shift',
                                                    -- 'handoff', 'emergency', 'system', 'invite'
    source_id           TEXT,                       -- ULID of source entity (NULL for system notifications)
    urgency             TEXT NOT NULL DEFAULT 'normal',  -- 'low', 'normal', 'high', 'critical'
    title_enc           TEXT NOT NULL,              -- Encrypted notification title
    body_enc            TEXT,                       -- Encrypted notification body

    -- Delivery state
    read_at             TEXT,                       -- When user read in-app (NULL = unread)
    dismissed_at        TEXT,                       -- When user dismissed
    snoozed_until       TEXT,                       -- Snooze expiry (NULL = not snoozed)
    deliver_after       TEXT,                       -- Deferred delivery (quiet hours). NULL = deliver immediately.
                                                    -- If set, notification channels won't fire until this timestamp.

    -- Channel delivery tracking
    email_sent_at       TEXT,                       -- NULL = not sent / not applicable
    push_sent_at        TEXT,
    email_failed        INTEGER NOT NULL DEFAULT 0,
    push_failed         INTEGER NOT NULL DEFAULT 0,

    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_notifications_user_unread ON notifications(recipient_user_id, created_at)
    WHERE read_at IS NULL AND dismissed_at IS NULL;
CREATE INDEX idx_notifications_user ON notifications(recipient_user_id, created_at);
CREATE INDEX idx_notifications_source ON notifications(source_type, source_id);

-- ============================================================================
-- NOTIFICATION CHANNELS
-- Per-user external notification endpoint configuration.
-- Stored separately from user profile because these contain sensitive
-- credentials (API keys, topic URLs) that need individual encryption.
-- ============================================================================

CREATE TABLE notification_channels (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    user_id             TEXT NOT NULL REFERENCES users(id),
    household_id        TEXT NOT NULL REFERENCES households(id),
    channel_type        TEXT NOT NULL,              -- 'email', 'web_push', 'ntfy', 'gotify', 'pushover'
    config_enc          TEXT NOT NULL,              -- Encrypted JSON with channel-specific config:
                                                    -- email: {"address": "..."}
                                                    -- ntfy: {"server_url": "...", "topic": "..."}
                                                    -- gotify: {"server_url": "...", "token": "..."}
                                                    -- pushover: {"user_key": "...", "api_token": "..."}
                                                    -- web_push: {"endpoint": "...", "keys": {...}}
    is_enabled          INTEGER NOT NULL DEFAULT 1,
    min_urgency         TEXT NOT NULL DEFAULT 'normal',  -- Minimum urgency to trigger this channel
    quiet_hours_start   TEXT,                       -- "22:00" (local to user timezone, NULL = no quiet hours)
    quiet_hours_end     TEXT,                       -- "07:00"
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_notification_channels_user ON notification_channels(user_id) WHERE is_enabled = 1;
```

### Audit Log Table

```sql
-- ============================================================================
-- AUDIT LOG
-- Who did what and when. Critical for accountability in a care setting.
-- Not encrypted — contains action metadata, not PHI content.
-- Sensitive details are referenced by ID, not stored inline.
-- ============================================================================

CREATE TABLE audit_log (
    id                  TEXT NOT NULL PRIMARY KEY,  -- ULID
    household_id        TEXT NOT NULL REFERENCES households(id),
    user_id             TEXT REFERENCES users(id),  -- NULL for system actions
    action              TEXT NOT NULL,              -- 'create', 'update', 'delete', 'login', 'logout',
                                                    -- 'shift_start', 'shift_end', 'emergency_activate',
                                                    -- 'emergency_deactivate', 'invite_sent', 'role_changed',
                                                    -- 'medication_given', 'task_completed', 'export_data'
    entity_type         TEXT NOT NULL,              -- 'care_recipient', 'task', 'medication', 'user', etc.
    entity_id           TEXT,                       -- ULID of affected entity
    metadata            TEXT,                       -- JSON with non-sensitive action details
                                                    -- e.g., {"field":"role","from":"member","to":"admin"}
    ip_address          TEXT,
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_audit_household ON audit_log(household_id, created_at);
CREATE INDEX idx_audit_user ON audit_log(user_id, created_at);
CREATE INDEX idx_audit_entity ON audit_log(entity_type, entity_id);
```

### Application Config Table

```sql
-- ============================================================================
-- CONFIG
-- Application-level configuration stored in the database.
-- ============================================================================

CREATE TABLE config (
    key                 TEXT NOT NULL PRIMARY KEY,
    value               TEXT NOT NULL,
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- Expected keys:
-- 'encryption_salt'       - Random salt generated on first run
-- 'schema_version'        - Tracks migration state (also managed by goose)
-- 'instance_id'           - Random ID for this installation (useful for support)
-- 'setup_completed'       - Whether onboarding wizard has been completed
-- 'backup_last_run'       - Timestamp of last backup
```

---

## Entity Relationship Summary

```
household
├── users (many)
│   ├── sessions (many)
│   ├── login_attempts (many, via email_hash)
│   ├── availability (many)
│   └── notification_channels (many)
├── invites (many)
├── care_recipients (many)
│   ├── care_recipient_medical (one)
│   ├── care_recipient_routines (many, versioned)
│   ├── emergency_profiles (one)
│   ├── emergency_tokens (many)
│   ├── emergency_events (many)
│   ├── caregiver_assignments (many)
│   ├── documents (many)
│   ├── shifts (many)
│   │   ├── shift_handoffs (one per shift)
│   │   └── care_log_entries (many, linked to shift)
│   ├── tasks (many)
│   │   └── task_templates (many)
│   ├── medications (many)
│   │   ├── medication_schedules (many per medication)
│   │   ├── medication_administrations (many)
│   │   └── medication_supply_log (many)
│   ├── care_log_entries (many)
│   │   ├── care_log_entry_tags (many-to-many)
│   │   └── care_log_attachments (many)
│   ├── messages (many)
│   │   ├── message_mentions (many)
│   │   └── message_read_receipts (many)
│   └── contacts (many-to-many via contact_care_recipient_links)
├── contacts (many)
├── notifications (many, per user)
├── notification_channels (many, per user)
└── audit_log (many)
```

---

## Index Strategy Notes

**General principles applied:**
- Every foreign key that's used in `WHERE` clauses gets an index
- Composite indexes are ordered for the most common query patterns (household + date range is the most frequent)
- Partial indexes (`WHERE deleted_at IS NULL`, `WHERE is_active = 1`) keep index size small and queries fast on active data
- The `household_id` appears in most indexes because virtually every query is household-scoped

**Indexes NOT created (intentionally):**
- No indexes on encrypted columns — they can't be meaningfully queried at the SQL level
- No full-text search indexes in 1.0 — search happens in Go after decryption
- No indexes on rarely-queried columns to keep write performance high

**SQLite-specific considerations:**
- SQLite uses B-tree indexes. ULID primary keys have good locality because they're time-sorted
- `WITHOUT ROWID` could be used on junction tables (like `care_log_entry_tags`) for slight performance gains, but isn't critical for 1.0
- WAL mode (set in connection pragmas) is essential for concurrent read performance

---

## Migration Ordering

The initial migration should create tables in dependency order:

```
001_initial_schema.sql:
  1.  config
  2.  households
  3.  users
  4.  sessions
  5.  login_attempts
  6.  invites
  7.  care_recipients
  8.  care_recipient_medical
  9.  care_recipient_routines
  10. emergency_profiles
  11. emergency_tokens
  12. emergency_events
  13. caregiver_assignments
  14. shift_templates
  15. shifts
  16. shift_swap_requests
  17. availability
  18. task_templates
  19. tasks
  20. medications
  21. medication_schedules
  22. medication_administrations
  23. medication_supply_log
  24. care_log_entries
  25. care_log_tags
  26. care_log_entry_tags
  27. care_log_attachments
  28. messages
  29. message_mentions
  30. message_read_receipts
  31. shift_handoffs
  32. contacts
  33. contact_care_recipient_links
  34. documents
  35. notifications
  36. notification_channels
  37. audit_log
```

This ordering ensures all foreign key references resolve correctly.

---

## Future Schema Considerations (Post-1.0)

These tables will be needed for post-1.0 features but are documented here so the core schema doesn't conflict with them:

**Appointments & Transportation** — `appointments` table linking to `care_recipients` and `contacts`, with `transportation_assignments` junction table.

**Incident Tracking** — `incidents` table with severity levels, linked to `care_log_entries` for the narrative and `tasks` for follow-up actions.

**Financial Tracking** — `expenses` table with category, amount, receipt attachment, and `expense_splits` for cost sharing across users.

**Supplies & Inventory** — `supply_items` table with par levels and `supply_log` for tracking consumption and restocking.

**Family Decision Log** — `decisions` table with `decision_acknowledgments` junction table for sign-offs.

**Caregiver Wellbeing** — `caregiver_checkins` table for self-assessments, with `caregiver_hours_summary` as a materialized view or summary table computed from shifts.

**Reporting** — likely uses materialized summary tables or views computed from existing data rather than new entity tables.

None of these will require changes to the core 1.0 tables — they'll add new tables with foreign keys to existing ones.
