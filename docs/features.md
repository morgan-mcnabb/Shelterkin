# Caregiver Coordinator Hub — Full Feature List

*Open source, self-hosted · Written in Go*

---

## Release Planning

### 1.0 — Core (Ship This First)
The essential caregiving loop: set up, schedule, track, communicate, respond to emergencies.

- Onboarding / Setup Wizard
- The Hub — Main Dashboard
- Core: Care Recipient Profiles
- Contact & Provider Directory
- Scheduling & Calendar
- Task Management
- Medication Management
- Care Logging & Daily Journal
- Shift Handoff
- Communication & Messaging
- Notification Center
- Emergency Mode / Panic Button
- Care Recipient Daily Routine & Preferences
- Multi-Caregiver Access & Roles
- Authentication & Security
- Self-Hosting & Technical
- Data Export & Portability

### Post-1.0 — Valuable but Deferrable
These make the app better but aren't required for the core caregiving workflow to function.

- Appointments & Transportation *(could be tracked as tasks in 1.0)*
- Incident & Health Event Tracking *(care logs + emergency mode cover the basics in 1.0)*
- Reporting & Insights *(raw data is there, dashboards can come later)*
- Financial Tracking
- Supplies & Inventory Tracking
- Family Decision Log
- Caregiver Wellbeing & Load Balancing
- API & Extensibility *(build internal API-first, but public API docs and webhooks can wait)*
- Nice-to-Have / Future Roadmap items

---

## Onboarding / Setup Wizard

- Guided first-run flow: create household → add first care recipient → invite family members → set up basic schedule
- Step-by-step profile builder for care recipients (don't dump a blank form)
- Invite flow with role selection: "Are they a family member or a hired caregiver?"
- Quick-start templates: common daily routines, medication schedules, and task lists to start from rather than building from scratch
- Skip-and-come-back-later: let users complete setup incrementally without blocking access to the app
- Short contextual tooltips on first use of each major feature

---

## The Hub — Main Dashboard / Landing Page

- Active shift context: who's currently on duty for each care recipient, with clear "you're active" indicator
- Today's timeline: chronological view of the day (completed tasks, upcoming tasks, medication times, appointments) with a "now" line that moves in real time
- Needs attention panel: overdue tasks, unread handoff notes, missed medications, unanswered messages — sorted by urgency
- Quick actions: one-tap access to the most common tasks (log a meal, record vitals, mark medication given, write a quick note, start/end shift)
- Recent activity feed: what other caregivers have logged recently for quick catch-up
- Care recipient switcher: toggle between recipients or view a combined dashboard (for multi-recipient households)
- Customizable widget layout: caregivers can rearrange or hide panels based on their role and preferences
- At-a-glance status indicators per care recipient: mood, last meal, last medication, next appointment

---

## Core: Care Recipient Profiles

- Create and manage profiles for each care recipient (elderly parent, disabled family member, etc.)
- Medical information summary: conditions, allergies, blood type, dietary restrictions
- Emergency contacts and healthcare provider directory
- Insurance information and policy details
- Preferred hospital / pharmacy / specialists
- Photo and basic bio for quick reference by rotating caregivers
- Document uploads (medical records, legal docs like power of attorney, advance directives)

---

## Contact & Provider Directory

- Dedicated, searchable directory separate from care recipient profiles
- Categories: medical providers, pharmacy, insurance, home health agency, personal contacts, neighbors, etc.
- Quick-call / quick-copy for phone numbers
- Notes per contact (e.g., "ask for nurse Janet", "fax preferred over email")
- Link contacts to specific care recipients
- "Favorites" or "frequently called" pinning for fast access

---

## Scheduling & Calendar

- Shared caregiving calendar visible to all authorized household/team members
- Shift scheduling: assign caregivers to time blocks (morning, afternoon, overnight, etc.)
- Recurring schedule templates (e.g., "Mom's weekly routine")
- Shift swap requests between caregivers
- Availability management: caregivers mark when they're available or unavailable
- Conflict detection: alerts when shifts overlap or gaps exist
- Calendar export (iCal/ICS) for syncing with personal calendars
- Optional integration hooks for Google Calendar / CalDAV

---

## Task Management

- Daily task checklists per care recipient (medications, meals, exercises, hygiene, etc.)
- Recurring task templates that auto-populate each day/week
- Task assignment to specific caregivers
- Task completion tracking with timestamps and who completed it
- Overdue task alerts and escalation
- Custom task categories (medical, household, emotional/social, errands)
- One-off task creation for ad-hoc needs ("pick up prescription at CVS by 3pm")

---

## Medication Management

- Full medication list per care recipient with dosage, frequency, and prescribing doctor
- Medication schedule tied to daily task checklists
- Administration logging: who gave what, when, and any notes
- Refill tracking with alerts when supply is running low
- Interaction warnings (basic flag system, not a substitute for pharmacist advice)
- PRN (as-needed) medication tracking with reason/symptom logging
- Medication history / audit log

---

## Care Logging & Daily Journal

- Structured daily log entries: meals eaten, mood, mobility, sleep quality, pain level, vitals
- Free-form journal notes for qualitative observations
- Tagging system for easy filtering (e.g., #fall, #anxiety, #goodday)
- Photo/attachment support for wound tracking, etc.
- Log entries tied to the caregiver who wrote them
- Timeline view of all entries for spotting trends over time
- Export logs for sharing with healthcare providers

---

## Shift Handoff (First-Class Flow)

- Structured handoff template triggered when ending a shift
- Required fields: mood/demeanor, meals eaten, medications given, any incidents, pending tasks
- Optional fields: pain level, sleep quality, bowel/bladder, visitor log, free-form notes
- Incoming caregiver sees handoff summary immediately upon starting their shift
- Handoff history: review past handoffs for pattern spotting
- Incomplete handoff warnings: flag if outgoing caregiver skipped the handoff

---

## Communication & Messaging

- In-app message board / feed per care recipient (like a private timeline)
- Pinned announcements (e.g., "New medication starting Monday")
- @mention system to flag specific caregivers
- Notification preferences: email, push (via web push or ntfy/Gotify integration), or digest
- Read receipts on critical messages

---

## Notification Center

- Unified in-app notification feed across all features (tasks, meds, messages, incidents, supplies, etc.)
- Configurable delivery channels per user: in-app, email (SMTP), web push, ntfy, Gotify, Pushover
- Urgency levels: low (in-app only), normal (in-app + preferred channel), critical (all channels, breaks through quiet hours)
- Quiet hours with per-user scheduling (e.g., off-duty caregiver sleeps undisturbed unless it's an emergency)
- Notification grouping and batching: digest mode for low-priority items
- Mark as read / dismiss / snooze
- Emergency override: panic button and critical health events always break through regardless of settings

---

## Appointments & Transportation *(Post-1.0 — use Tasks in 1.0)*

- Appointment tracker: upcoming doctor visits, therapy sessions, lab work
- Appointment reminders with configurable lead time
- Transportation coordination: who's driving, pickup time, any special needs (wheelchair, etc.)
- Appointment notes and follow-up action items
- Recurring appointment support

---

## Incident & Health Event Tracking *(Post-1.0 — care logs + emergency mode cover basics)*

- Incident reports: falls, behavioral episodes, ER visits, adverse reactions
- Structured severity levels
- Follow-up action tracking per incident
- Incident history and frequency reporting
- Emergency protocol checklists (customizable per household)

---

## Multi-Caregiver Access & Roles

- Role-based access: primary caregiver / family admin, family member, professional caregiver, medical proxy
- Invite system via email or shareable link with expiration
- Granular permissions: who can view medical info, who can edit schedules, who can only view logs
- Activity audit log: who did what and when
- Support for multiple care recipients under one household/account
- Professional caregiver mode: limited access scoped to their assigned recipients only

---

## Emergency Mode / Panic Button

- One-tap emergency activation that notifies all caregivers simultaneously
- Emergency screen: critical medical info on a single page (allergies, current medications, blood type, conditions, emergency contacts, insurance)
- Customizable emergency protocol checklists per care recipient (e.g., "seizure protocol", "fall protocol")
- Paramedic-friendly view: large text, high contrast, no login required via shareable emergency URL/QR code
- Incident auto-created when emergency mode is triggered
- Post-emergency debrief prompt

---

## Care Recipient Daily Routine & Preferences

- Dedicated reference document per care recipient, separate from tasks
- Personal preferences: food likes/dislikes, coffee order, favorite shows, comfort items
- Daily rhythm: what time they wake up, nap schedule, bedtime routine
- Behavioral notes: triggers for anxiety, how to redirect, what calms them down
- Communication tips: hearing difficulties, preferred name, topics to avoid
- Versioned history so changes are tracked over time
- Printable one-pager for new or temporary caregivers

---

## Supplies & Inventory Tracking *(Post-1.0)*

- Track consumable supplies: incontinence products, wound care, nutritional supplements, OTC medications, medical equipment
- Par levels with low-stock alerts (e.g., "fewer than 10 adult briefs remaining")
- Shopping list auto-generation from low-stock items
- Assign restocking responsibility to a specific caregiver
- Purchase logging with optional receipt photo
- Recurring supply needs tied to usage estimates

---

## Family Decision Log *(Post-1.0)*

- Document significant care decisions with date, who was involved, and the outcome
- Categories: medical decisions, financial, living arrangements, legal, care plan changes
- Attach supporting documents (doctor's recommendations, research, quotes)
- Voting / sign-off feature: family members can acknowledge or formally agree
- Searchable history to resolve "I thought we decided..." disputes
- Link decisions to follow-up tasks

---

## Caregiver Wellbeing & Load Balancing *(Post-1.0)*

- Track hours per caregiver over time
- Load balance dashboard: visualize how caregiving hours are distributed across the family
- Imbalance alerts when one caregiver is consistently shouldering more
- Optional self-check: caregivers can log their own stress/energy level
- Respite tracking: log when a caregiver takes time off and who covered
- Burnout awareness resources (links, not medical advice)

---

## Reporting & Insights *(Post-1.0)*

- Weekly/monthly care summary reports (auto-generated)
- Vitals and symptom trend charts (weight, blood pressure, mood, sleep, pain over time)
- Task completion rates per caregiver
- Medication adherence tracking
- Exportable reports (PDF, CSV) for sharing with doctors or family meetings
- Dashboard with at-a-glance status per care recipient

---

## Financial Tracking *(Post-1.0)*

- Log caregiving-related expenses (medical copays, supplies, mileage, etc.)
- Categorize expenses for tax or reimbursement purposes
- Split expense tracking across family members
- Receipt photo upload
- Monthly expense summary and export

---

## Self-Hosting & Technical

- Single binary deployment (Go)
- SQLite by default, with PostgreSQL option for larger deployments
- Docker image and docker-compose for easy setup
- Environment variable and config file based configuration
- Automatic database migrations
- Built-in backup and restore (database + uploaded files)
- Reverse proxy friendly (works behind Nginx, Caddy, Traefik)
- HTTPS support via built-in Let's Encrypt or reverse proxy
- Low resource footprint: designed to run on a Raspberry Pi or cheap VPS
- ARM64 and AMD64 builds
- Progressive Web App (PWA) support for mobile-like experience without app stores

---

## Authentication & Security

- Local account system with email/password
- Optional OIDC / OAuth2 support (Authelia, Authentik, Keycloak, etc.)
- Two-factor authentication (TOTP)
- Session management
- All medical data encrypted at rest
- HIPAA-awareness: designed with privacy best practices (not certified, but conscious of it)
- Configurable data retention policies

---

## Data Export & Portability

- Full data export: all records, logs, documents, and attachments in a single archive
- Standard formats: JSON for structured data, original files for attachments
- One-click export from admin panel
- Scheduled automatic backups (database + uploads) to local path or S3-compatible storage
- Import from export: spin up a new instance and restore everything
- Per-care-recipient export for sharing a subset of data (e.g., with a new care facility)

---

## API & Extensibility *(Post-1.0 — build API-first internally, public docs and webhooks later)*

- RESTful API for all core functionality
- Webhook support for external integrations (e.g., trigger a Home Assistant automation when a task is completed)
- Notification provider plugins: email (SMTP), ntfy, Gotify, Pushover, web push
- CalDAV server or iCal feed for calendar interop
- Import/export data in standard formats (JSON, CSV)

---

## Nice-to-Have / Future Roadmap *(Post-1.0)*

- Mobile app (Flutter or native) consuming the API
- Voice note support for quick log entries
- AI-assisted care summaries (optional, privacy-first)
- Multi-language support / i18n
- Printable daily care sheets for caregivers who prefer paper
- Guest access for visiting nurses or temporary helpers
- Care recipient self-service portal (for those who are able)
- Integration with medical device APIs (blood pressure monitors, glucose meters) via Bluetooth/USB bridge

---

## Design Philosophy

- **Privacy first**: all data stays on the user's hardware, no cloud dependency
- **Simple to deploy**: single binary + SQLite means no complex infrastructure
- **Accessible**: clean, high-contrast UI that works for non-technical family members and older users
- **Offline-capable**: core features work without internet via PWA + local-first sync
- **Extensible but opinionated**: sensible defaults, but hooks for power users
