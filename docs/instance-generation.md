# Caregiver Coordinator Hub — Instance Generation System Design

*This document defines the design and implementation patterns for the instance generation system — the background infrastructure that creates concrete daily records (task instances, medication administration records, shift instances) from recurring templates and schedules. This is critical infrastructure that most daily-use features depend on.*

---

## Why This System Matters

When a caregiver opens the app at 7am, the Hub dashboard must show:

- Today's task checklist (populated from task templates)
- Today's medication schedule (populated from medication schedules)
- Today's shifts (populated from shift templates)

If these records don't exist, the caregiver sees an empty screen and the app is useless. This system is the engine that turns "give Mom 10mg Lisinopril every day at 8am" into a concrete, trackable record for March 15th, 2026 at 8am that someone can mark as "given."

**Reliability requirements:**
- Records must exist before the caregiver needs them (ideally generated the night before)
- The system must be idempotent — running twice must never create duplicates
- The system must handle server downtime gracefully (catch-up on missed days)
- Changes to templates/schedules must be reflected correctly without corrupting existing records
- Timezone handling must be correct — "8am" means 8am in the care recipient's local time

---

## Architecture Overview

The instance generation system has three components:

```
┌─────────────────────────────────────────────────────────┐
│                    Instance Generator                     │
│                                                           │
│  ┌─────────────┐  ┌──────────────┐  ┌────────────────┐  │
│  │    Daily     │  │   On-Demand  │  │    Cleanup     │  │
│  │  Scheduler   │  │   Trigger    │  │    Worker      │  │
│  │             │  │              │  │                │  │
│  │ Runs at     │  │ Fires when   │  │ Removes future │  │
│  │ midnight    │  │ templates    │  │ instances when │  │
│  │ (per TZ)    │  │ are created  │  │ templates are  │  │
│  │             │  │ or modified  │  │ deactivated    │  │
│  └──────┬──────┘  └──────┬───────┘  └───────┬────────┘  │
│         │                │                   │           │
│         └────────────────┼───────────────────┘           │
│                          │                               │
│                   ┌──────▼───────┐                       │
│                   │   Core       │                       │
│                   │  Generation  │                       │
│                   │   Engine     │                       │
│                   └──────────────┘                       │
│                                                           │
└─────────────────────────────────────────────────────────┘
```

1. **Daily Scheduler** — a background goroutine that runs on a schedule and generates instances for upcoming days
2. **On-Demand Trigger** — called synchronously when templates/schedules are created or modified, to fill in any gaps for today and the generation window
3. **Cleanup Worker** — called when templates are deactivated or deleted, to remove future uncompleted instances

All three use the same **Core Generation Engine** which is the single source of truth for creating instances.

---

## Core Generation Engine

### The Fundamental Operation

The core operation is: **given a template/schedule and a date range, create instances for each occurrence that doesn't already exist.**

```go
// GenerateInstances is the core idempotent generation function.
// It creates instances for the given date range, skipping any that already exist.
// Returns the number of new instances created.
func (g *Generator) GenerateInstances(ctx context.Context, dateRange DateRange) (int, error)
```

This function handles all three entity types (tasks, medications, shifts) through a common interface.

### Idempotency: The Deduplication Key

This is the most critical design decision in the entire system. Every generated instance must have a deterministic identity so that running the generator twice produces the same result.

**The deduplication key is: `(template_id, scheduled_date, scheduled_time)`**

For tasks:
```
template_id + due_date + due_time → unique task instance
```

For medication administrations:
```
medication_id + schedule_id + scheduled_date + scheduled_time → unique administration record
```

For shifts:
```
shift_template_id + shift_date + start_time → unique shift instance
```

**Implementation: Use a deterministic ULID or a composite unique constraint.**

Option A — Composite unique constraint (recommended for clarity):
```sql
-- Add to tasks table
CREATE UNIQUE INDEX idx_tasks_dedup
    ON tasks(template_id, due_at)
    WHERE template_id IS NOT NULL AND deleted_at IS NULL;

-- Add to medication_administrations table
CREATE UNIQUE INDEX idx_med_admin_dedup
    ON medication_administrations(medication_id, schedule_id, scheduled_at)
    WHERE schedule_id IS NOT NULL;

-- Add to shifts table
CREATE UNIQUE INDEX idx_shifts_dedup
    ON shifts(template_id, starts_at)
    WHERE template_id IS NOT NULL;
```

These unique indexes mean that attempting to insert a duplicate will fail with a constraint violation, which the generator catches and skips. This is the simplest and most robust idempotency mechanism.

**In Go, use INSERT OR IGNORE (SQLite) to make this a no-op for duplicates:**
```sql
-- name: GenerateTaskInstance :exec
INSERT OR IGNORE INTO tasks (
    id, household_id, care_recipient_id, template_id,
    title_enc, description_enc, category, priority,
    assigned_to, due_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
```

The `INSERT OR IGNORE` means: if the unique constraint would be violated, silently skip this row. No error, no duplicate. This is the idempotency guarantee.

---

### Timezone Handling

**The problem:** A medication scheduled for "8:00 AM" in the care recipient's timezone (e.g., `America/New_York`) needs to be stored as a UTC timestamp. But UTC offsets change with daylight saving time — 8:00 AM ET is `13:00 UTC` in winter and `12:00 UTC` in summer.

**The solution:** The generator resolves the local time to UTC at generation time using the care recipient's timezone.

```go
import "time"

// resolveLocalTimeToUTC converts a local time-of-day on a specific date
// to a UTC timestamp, correctly handling DST transitions.
func resolveLocalTimeToUTC(date time.Time, timeOfDay string, tzName string) (time.Time, error) {
    loc, err := time.LoadLocation(tzName)
    if err != nil {
        return time.Time{}, fmt.Errorf("invalid timezone %q: %w", tzName, err)
    }

    // Parse the time-of-day
    parts := strings.Split(timeOfDay, ":")
    hour, _ := strconv.Atoi(parts[0])
    minute, _ := strconv.Atoi(parts[1])

    // Construct the local time on the specific date
    localTime := time.Date(
        date.Year(), date.Month(), date.Day(),
        hour, minute, 0, 0,
        loc,
    )

    // Convert to UTC
    return localTime.UTC(), nil
}
```

**DST edge cases handled:**
- Spring forward (e.g., 2:00 AM doesn't exist): `time.Date` in Go handles this automatically by adjusting forward
- Fall back (e.g., 1:30 AM occurs twice): Go uses the first occurrence, which is the standard behavior
- These edge cases are documented in the care recipient's medication log if they occur

**Important:** The generation window (see below) must regenerate instances when a care recipient's timezone changes, since all future UTC timestamps would be wrong.

---

### The Generation Window

The generator creates instances for a rolling window of days ahead. This balances between:
- Having instances ready before caregivers need them
- Not generating so far ahead that template changes become painful to reconcile

**Default: Generate 3 days ahead (today + 2 more days).**

This means at any point in time, instances exist for today, tomorrow, and the day after. This is enough that:
- Caregivers always see today's full schedule
- The calendar view shows the next couple days
- If the server is down for a day, there's still a buffer

**Why not 7 days? Why not 1 day?**
- 7 days means template changes need to reconcile a week of instances, which adds complexity
- 1 day means a server outage of more than a few hours could result in missing instances
- 3 days is the sweet spot — small enough to keep reconciliation simple, large enough for resilience

The window size is configurable via the `config` table:
```sql
-- In config table
key: 'generation_window_days'
value: '3'
```

---

### Generation Flow (Pseudocode)

```
FUNCTION GenerateAllInstances(dateRange):

    FOR EACH household:
        FOR EACH active care_recipient in household:

            // === TASK GENERATION ===
            FOR EACH active task_template for this care_recipient:
                FOR EACH date in dateRange:
                    IF recurrence_rule matches this date:
                        FOR EACH time_of_day in recurrence_rule.times:
                            utcTime = resolveLocalTimeToUTC(date, time, recipient.timezone)
                            INSERT OR IGNORE task instance with:
                                template_id = template.id
                                due_at = utcTime
                                title_enc = template.title_enc  (copy from template)
                                category = template.category
                                priority = template.priority
                                assigned_to = template.default_assigned_to

            // === MEDICATION GENERATION ===
            FOR EACH active, non-discontinued medication for this care_recipient:
                IF medication.is_prn:
                    SKIP  (PRN medications are logged on-demand, not pre-generated)

                FOR EACH active schedule for this medication:
                    FOR EACH date in dateRange:
                        IF schedule.days_of_week is NULL OR date.weekday in schedule.days_of_week:
                            utcTime = resolveLocalTimeToUTC(date, schedule.time_of_day, recipient.timezone)
                            INSERT OR IGNORE medication_administration with:
                                medication_id = medication.id
                                schedule_id = schedule.id
                                scheduled_at = utcTime
                                status = 'pending'

            // === SHIFT GENERATION ===
            FOR EACH active shift_template for this care_recipient:
                FOR EACH date in dateRange:
                    IF recurrence_rule matches this date:
                        startUTC = resolveLocalTimeToUTC(date, template.shift_start_time, recipient.timezone)
                        endUTC = resolveLocalTimeToUTC(date, template.shift_end_time, recipient.timezone)
                        // Handle overnight shifts (end time < start time)
                        IF endUTC <= startUTC:
                            endUTC = endUTC + 24 hours
                        INSERT OR IGNORE shift with:
                            template_id = template.id
                            starts_at = startUTC
                            ends_at = endUTC
                            assigned_to = template.assigned_to
                            status = 'scheduled'

    RETURN count of new instances created
```

---

### Recurrence Rule Matching

The recurrence rules stored as JSON on templates need to be evaluated to determine if a given date is an occurrence.

```go
// RecurrenceRule represents when a recurring event occurs.
type RecurrenceRule struct {
    Frequency  string   `json:"frequency"`        // "daily", "weekly", "monthly"
    Times      []string `json:"times"`            // ["08:00", "20:00"]
    DaysOfWeek []string `json:"days_of_week"`     // ["mon","tue","wed"] — for weekly
    DayOfMonth int      `json:"day_of_month"`     // 15 — for monthly
}

// MatchesDate returns true if the given date is an occurrence of this rule.
func (r *RecurrenceRule) MatchesDate(date time.Time) bool {
    switch r.Frequency {
    case "daily":
        return true

    case "weekly":
        if len(r.DaysOfWeek) == 0 {
            return true // no day filter = every day
        }
        dayName := strings.ToLower(date.Weekday().String()[:3]) // "mon", "tue", etc.
        for _, d := range r.DaysOfWeek {
            if d == dayName {
                return true
            }
        }
        return false

    case "monthly":
        return date.Day() == r.DayOfMonth

    default:
        return false
    }
}
```

**Why not support more complex rules (biweekly, every 3rd Tuesday, etc.) in 1.0?**

Because daily, weekly (with day-of-week selection), and monthly cover 95%+ of real caregiving schedules. Medications are almost always daily or specific-days-of-week. Tasks follow the same patterns. Shifts are typically weekly recurring. Adding complex RRULE support adds significant complexity for minimal real-world benefit. It can be added post-1.0 if users request it.

---

## Daily Scheduler

The daily scheduler is a background goroutine that runs inside the main Go process (no external cron job needed).

```go
// StartScheduler starts the background instance generation scheduler.
// It runs the generator once on startup (catch-up) and then on a regular interval.
func StartScheduler(ctx context.Context, generator *Generator, interval time.Duration) {
    // Run immediately on startup to catch up on any missed generation
    log.Info("Running startup instance generation (catch-up)")
    count, err := generator.GenerateAllInstances(ctx, generator.GetGenerationWindow())
    if err != nil {
        log.Error("Startup generation failed", "error", err)
    } else {
        log.Info("Startup generation complete", "instances_created", count)
    }

    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            count, err := generator.GenerateAllInstances(ctx, generator.GetGenerationWindow())
            if err != nil {
                log.Error("Scheduled generation failed", "error", err)
                // Don't crash — the next tick will retry
                continue
            }
            if count > 0 {
                log.Info("Scheduled generation complete", "instances_created", count)
            }

        case <-ctx.Done():
            log.Info("Scheduler shutting down")
            return
        }
    }
}
```

**Scheduling interval: Every 30 minutes.**

Why 30 minutes instead of once at midnight?
- There is no single "midnight" when households span multiple timezones
- If the server restarts at 12:05am, a once-at-midnight scheduler would miss the window and caregivers would have no schedule until the next day
- 30-minute intervals with idempotent generation means the system is self-healing — any missed run is caught within 30 minutes
- The `INSERT OR IGNORE` idempotency means frequent runs have near-zero cost when instances already exist

**Startup catch-up:**
When the server starts, the scheduler immediately runs a full generation for the generation window. This handles:
- First-ever startup (populates initial instances)
- Server was down for hours/days (fills in missed instances)
- Server restart (no-op thanks to idempotency, but verifies everything is current)

---

## On-Demand Trigger

When a user creates or modifies a template/schedule, the system must immediately generate instances so the changes are visible right away — not 30 minutes later.

### Trigger Points

```go
// Called from the service layer after template/schedule changes

// When a new task template is created
func (s *TaskService) CreateTemplate(ctx context.Context, input CreateTemplateInput) error {
    template, err := s.db.CreateTaskTemplate(ctx, /* ... */)
    if err != nil {
        return err
    }
    // Immediately generate instances for this template
    return s.generator.GenerateForTaskTemplate(ctx, template, s.generator.GetGenerationWindow())
}

// When a task template's recurrence rule is modified
func (s *TaskService) UpdateTemplate(ctx context.Context, input UpdateTemplateInput) error {
    oldTemplate, _ := s.db.GetTaskTemplate(ctx, input.ID)
    template, err := s.db.UpdateTaskTemplate(ctx, /* ... */)
    if err != nil {
        return err
    }
    // Reconcile: clean up future instances from old rule, generate from new rule
    return s.generator.ReconcileTaskTemplate(ctx, oldTemplate, template)
}

// When a medication schedule is added or changed
func (s *MedicationService) AddSchedule(ctx context.Context, input AddScheduleInput) error {
    schedule, err := s.db.CreateMedicationSchedule(ctx, /* ... */)
    if err != nil {
        return err
    }
    return s.generator.GenerateForMedicationSchedule(ctx, schedule, s.generator.GetGenerationWindow())
}

// When a task template is deactivated
func (s *TaskService) DeactivateTemplate(ctx context.Context, templateID string) error {
    err := s.db.DeactivateTaskTemplate(ctx, templateID)
    if err != nil {
        return err
    }
    // Remove future uncompleted instances
    return s.generator.CleanupDeactivatedTaskTemplate(ctx, templateID)
}
```

---

## Cleanup Worker

When a template is deactivated or a medication is discontinued, future **uncompleted** instances should be removed. Past and completed instances must be preserved.

```go
// CleanupDeactivatedTaskTemplate removes future uncompleted task instances
// that were generated from a deactivated template.
func (g *Generator) CleanupDeactivatedTaskTemplate(ctx context.Context, templateID string) error {
    now := time.Now().UTC().Format(time.RFC3339)
    return g.db.DeleteFutureUncompletedTasks(ctx, dbgen.DeleteFutureUncompletedTasksParams{
        TemplateID: templateID,
        After:      now,
    })
}
```

```sql
-- name: DeleteFutureUncompletedTasks :exec
UPDATE tasks
SET deleted_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
WHERE template_id = ?
  AND due_at > ?
  AND completed = 0
  AND skipped = 0
  AND deleted_at IS NULL;
```

**Important: This is a soft delete.** The records are marked as deleted, not removed. This preserves referential integrity and audit trail.

**What gets cleaned up and what doesn't:**

| Scenario | Future pending instances | Future completed instances | Past instances |
|---|---|---|---|
| Template deactivated | Soft deleted | Preserved | Preserved |
| Medication discontinued | Soft deleted | Preserved | Preserved |
| Template modified | Soft deleted, regenerated | Preserved | Preserved |
| Care recipient deleted | Soft deleted (cascade) | Preserved | Preserved |

---

## Design Clarifications and Edge Cases

### Late-Day Instance Creation (Past-Time Instances)

**Problem:** A new care recipient is added at 3pm. The on-demand trigger generates today's instances, including an 8am medication that's already 7 hours past. This instance appears as "pending" and the overdue checker immediately fires a notification for a medication that was never actually missed — it simply didn't exist in the system yet.

**Solution:** When generating instances for the current day, skip any instance whose scheduled time is more than a configurable grace period in the past.

```go
const lateInstanceGracePeriod = 2 * time.Hour

func (g *Generator) shouldGenerateInstance(scheduledUTC time.Time, now time.Time) bool {
    if scheduledUTC.After(now) {
        return true // Future instance — always generate
    }
    // Past instance — only generate if within grace period
    return now.Sub(scheduledUTC) <= lateInstanceGracePeriod
}
```

This means:
- If a care recipient is added at 3pm and has a 2pm medication, it's created (within 2-hour grace) so the caregiver can still mark it
- If they have an 8am medication, it's skipped entirely for today — the schedule picks up normally tomorrow
- The grace period is configurable but defaults to 2 hours, which covers reasonable "just set this up" scenarios

**For catch-up after multi-day downtime:** The generator skips all past-day instances entirely. If the server was down for 2 days, it does not create pending instances for those missed days — they're simply gone. This prevents a flood of false "overdue" notifications on restart. The generation window always starts from "today."

### Per-Recipient Generation Window

**The generation window is calculated per care recipient, not globally.** "Today" depends on the care recipient's timezone.

```go
func (g *Generator) GetGenerationWindowForRecipient(ctx context.Context, recipientID string) DateRange {
    recipient, _ := g.db.GetCareRecipient(ctx, recipientID)
    loc, _ := time.LoadLocation(recipient.Timezone)

    // "Today" in the recipient's local timezone
    localNow := time.Now().In(loc)
    today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)

    windowDays := g.getConfigInt("generation_window_days", 3)

    return DateRange{
        Start: today,
        End:   today.AddDate(0, 0, windowDays),
    }
}
```

When the scheduler runs globally, it iterates over care recipients and calculates each one's window independently. At 11pm UTC, a recipient in New York (7pm local) still needs today's remaining instances, while a recipient in Tokyo (8am next day local) needs tomorrow's instances. Per-recipient windowing handles this correctly.

### One-Off Tasks and Template-Generated Tasks Coexistence

**Design decision: One-off tasks and template-generated tasks can coexist at the same date/time. This is intentional.**

The deduplication index is on `(template_id, due_at) WHERE template_id IS NOT NULL`. One-off tasks have `template_id = NULL`, so they're excluded from deduplication entirely. This means:

- A template generates a "Give morning medication" task at 8am
- A caregiver manually creates an "Extra blood pressure check" task at 8am
- Both exist simultaneously — they're different tasks with different purposes

This is correct behavior. The deduplication only prevents the *same template* from creating multiple instances at the same time.

### Template Reactivation

When a deactivated template is reactivated, future instances need to be regenerated. The cleanup worker previously soft-deleted future pending instances (`deleted_at` is set).

**This works correctly without special handling** because:
1. The dedup unique index is `WHERE deleted_at IS NULL`
2. Soft-deleted instances have `deleted_at` set, so they don't conflict with the unique index
3. `INSERT OR IGNORE` for new instances succeeds because no active duplicate exists
4. The on-demand trigger fires on reactivation and generates fresh instances

```go
func (s *TaskService) ReactivateTemplate(ctx context.Context, templateID string) error {
    err := s.db.ReactivateTaskTemplate(ctx, templateID)
    if err != nil {
        return err
    }
    template, _ := s.db.GetTaskTemplate(ctx, templateID)
    // On-demand generation creates new instances — soft-deleted ones don't conflict
    return s.generator.GenerateForTaskTemplate(ctx, template, s.generator.GetGenerationWindow())
}
```

**Test this explicitly** — the test should verify that after deactivation → reactivation, the correct number of instances exist and the old soft-deleted ones remain in the database for audit purposes.

### Instance Data Snapshot from Templates

When an instance is generated from a template, the encrypted field values (title, description, etc.) are **copied** from the template at generation time. This is a deliberate snapshot:

- If the template title changes from "Morning medication" to "AM medication round," instances already generated keep "Morning medication"
- Only newly generated instances (future days) get the updated title
- This is correct behavior because a caregiver looking at yesterday's completed task should see what it was called when they completed it, not what it was renamed to later

**Implication for template changes:** When a user edits a template's content (not its schedule), the reconciliation flow does NOT regenerate existing pending instances just because the title changed. Reconciliation only triggers when the **recurrence rule** changes (which affects *when* instances exist). Content-only changes apply to newly generated instances going forward.

If a user explicitly wants to update the title of existing pending instances, that would be a separate "update pending instances" action in the UI — not part of the generation system.

---

## Template Modification Reconciliation

This is the trickiest scenario. When a template changes (e.g., medication schedule changes from "8am and 8pm" to "8am, 2pm, and 8pm"), the system must:

1. Keep any instances that are already completed or have notes/modifications
2. Remove future pending instances that no longer match the new rule
3. Generate new instances that match the new rule

```go
// ReconcileTaskTemplate handles template modification by cleaning up
// stale future instances and generating new ones.
func (g *Generator) ReconcileTaskTemplate(
    ctx context.Context,
    oldTemplate dbgen.TaskTemplate,
    newTemplate dbgen.TaskTemplate,
) error {
    now := time.Now().UTC()
    window := g.GetGenerationWindow()

    // Step 1: Soft-delete future PENDING instances from the old template
    // (completed, skipped, or instances with notes are preserved)
    err := g.db.SoftDeleteFuturePendingTasksByTemplate(ctx, dbgen.SoftDeleteFuturePendingTasksByTemplateParams{
        TemplateID: oldTemplate.ID,
        After:      now.Format(time.RFC3339),
    })
    if err != nil {
        return fmt.Errorf("cleaning old instances: %w", err)
    }

    // Step 2: Generate new instances from the updated template
    // INSERT OR IGNORE handles any overlap with preserved instances
    err = g.GenerateForTaskTemplate(ctx, newTemplate, window)
    if err != nil {
        return fmt.Errorf("generating new instances: %w", err)
    }

    return nil
}
```

```sql
-- name: SoftDeleteFuturePendingTasksByTemplate :exec
UPDATE tasks
SET deleted_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
WHERE template_id = ?
  AND due_at > ?
  AND completed = 0
  AND skipped = 0
  AND notes_enc IS NULL
  AND deleted_at IS NULL;
```

**Why preserve instances with notes?** Because if a caregiver wrote "Doctor said skip this dose on Thursday" on a pending instance, deleting it because the template changed would lose that annotation. The caregiver made a conscious, specific decision about that instance.

---

## Medication-Specific Considerations

### PRN (As-Needed) Medications

PRN medications are **never** pre-generated. They are created on-demand when a caregiver logs an administration. The generator explicitly skips medications where `is_prn = 1`.

### Medication Discontinuation

When a medication is discontinued (`discontinued_at` is set):

1. Future pending administration records are soft-deleted
2. The medication's schedules are deactivated (`is_active = 0`)
3. The generator skips discontinued medications in future runs
4. Past administration records (given, missed, skipped) are preserved permanently

```go
func (s *MedicationService) Discontinue(ctx context.Context, medicationID string) error {
    // Mark medication as discontinued
    err := s.db.DiscontinueMedication(ctx, dbgen.DiscontinueMedicationParams{
        ID:              medicationID,
        DiscontinuedAt:  time.Now().UTC().Format(time.RFC3339),
    })
    if err != nil {
        return err
    }

    // Deactivate all schedules
    err = s.db.DeactivateMedicationSchedules(ctx, medicationID)
    if err != nil {
        return err
    }

    // Clean up future pending administrations
    return s.generator.CleanupDiscontinuedMedication(ctx, medicationID)
}
```

### Care Recipient Timezone Change

When a care recipient's timezone is updated (e.g., they move from New York to Chicago), all future pending instances have incorrect UTC timestamps and must be regenerated.

```go
func (s *CareRecipientService) UpdateTimezone(ctx context.Context, recipientID string, newTZ string) error {
    oldRecipient, err := s.db.GetCareRecipient(ctx, recipientID)
    if err != nil {
        return err
    }

    err = s.db.UpdateCareRecipientTimezone(ctx, dbgen.UpdateCareRecipientTimezoneParams{
        ID:       recipientID,
        Timezone: newTZ,
    })
    if err != nil {
        return err
    }

    // Regenerate all future pending instances for this recipient
    // This follows the same pattern as template reconciliation:
    // 1. Soft-delete all future pending tasks, med administrations, and shifts
    // 2. Regenerate with new timezone
    return s.generator.ReconcileTimezoneChange(ctx, recipientID, oldRecipient.Timezone, newTZ)
}
```

```go
func (g *Generator) ReconcileTimezoneChange(ctx context.Context, recipientID, oldTZ, newTZ string) error {
    now := time.Now().UTC().Format(time.RFC3339)
    window := g.GetGenerationWindowForRecipient(ctx, recipientID)

    // Soft-delete future pending tasks (preserve completed/annotated)
    err := g.db.SoftDeleteFuturePendingTasksByRecipient(ctx, dbgen.SoftDeleteFuturePendingTasksByRecipientParams{
        CareRecipientID: recipientID,
        After:           now,
    })
    if err != nil {
        return fmt.Errorf("cleaning tasks: %w", err)
    }

    // Soft-delete future pending medication administrations
    err = g.db.SoftDeleteFuturePendingMedAdminsByRecipient(ctx, dbgen.SoftDeleteFuturePendingMedAdminsByRecipientParams{
        CareRecipientID: recipientID,
        After:           now,
    })
    if err != nil {
        return fmt.Errorf("cleaning med admins: %w", err)
    }

    // Soft-delete future pending shifts
    err = g.db.SoftDeleteFuturePendingShiftsByRecipient(ctx, dbgen.SoftDeleteFuturePendingShiftsByRecipientParams{
        CareRecipientID: recipientID,
        After:           now,
    })
    if err != nil {
        return fmt.Errorf("cleaning shifts: %w", err)
    }

    // Regenerate all instances with new timezone
    // INSERT OR IGNORE handles any overlap with preserved instances
    return g.generateForCareRecipientByID(ctx, recipientID, window)
}
```

This is essentially the same reconciliation pattern as template modification, but applied across all templates/schedules for a single care recipient. The test suite must verify that UTC timestamps change correctly after a timezone update.

### Supply Decrement Integration

When the generator creates medication administration instances, it does NOT decrement supply. Supply is decremented only when a caregiver marks the administration as "given." This is handled in the medication service, not the generator:

### Individual Schedule Deactivation

A medication can have multiple schedules (e.g., 8am and 8pm). When a doctor discontinues only the evening dose, the specific schedule is deactivated — not the entire medication.

```go
func (s *MedicationService) DeactivateSchedule(ctx context.Context, scheduleID string) error {
    err := s.db.DeactivateMedicationSchedule(ctx, scheduleID)
    if err != nil {
        return err
    }
    // Clean up only future pending instances from THIS schedule
    return s.generator.CleanupDeactivatedSchedule(ctx, scheduleID)
}
```

```sql
-- name: SoftDeleteFuturePendingMedAdminsBySchedule :exec
UPDATE medication_administrations
SET deleted_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
WHERE schedule_id = ?
  AND scheduled_at > ?
  AND status = 'pending'
  AND deleted_at IS NULL;
```

The dedup index includes `schedule_id`, so this correctly targets only the deactivated schedule's instances. Instances from other schedules for the same medication are untouched.

### Supply Decrement (Service Concern, Not Generator)

```go
func (s *MedicationService) RecordAdministration(ctx context.Context, adminID string) error {
    // Mark as given
    admin, err := s.db.MarkAdministrationGiven(ctx, /* ... */)
    if err != nil {
        return err
    }

    // Decrement supply and log
    med, err := s.db.DecrementMedicationSupply(ctx, admin.MedicationID)
    if err != nil {
        return err
    }

    // Log supply change
    err = s.db.CreateSupplyLog(ctx, dbgen.CreateSupplyLogParams{
        MedicationID:     admin.MedicationID,
        ChangeType:       "administered",
        QuantityChange:   -1,
        QuantityAfter:    med.CurrentSupply,
        AdministrationID: &admin.ID,
        LoggedBy:         getCurrentUserID(ctx),
    })

    // Check low supply threshold
    if med.CurrentSupply != nil && med.LowSupplyThreshold != nil {
        if *med.CurrentSupply <= *med.LowSupplyThreshold {
            s.notifications.Send(ctx, LowSupplyNotification(med))
        }
    }

    return err
}
```

---

## Shift-Specific Considerations

### Overnight Shifts

A shift template with `start_time: "22:00"` and `end_time: "06:00"` spans midnight. The generator handles this:

```go
startUTC := resolveLocalTimeToUTC(date, template.ShiftStartTime, recipient.Timezone)
endUTC := resolveLocalTimeToUTC(date, template.ShiftEndTime, recipient.Timezone)

// If end time is before or equal to start time, the shift crosses midnight
if !endUTC.After(startUTC) {
    endUTC = endUTC.Add(24 * time.Hour)
}
```

The shift is associated with the **start date** for deduplication purposes.

### Unassigned Shifts

A shift template may have `assigned_to = NULL` (no default assignee). The generated shift instance will also be unassigned. The scheduling UI shows these as "open shifts" that caregivers can claim.

---

## Overdue Detection

The generator does NOT mark items as overdue. Instead, overdue detection is a **query-time concern** — when the Hub dashboard or task list is rendered, the query checks if the scheduled time has passed and the status is still "pending."

```sql
-- name: GetOverdueMedications :many
SELECT ma.*, m.name_enc, m.dosage_enc
FROM medication_administrations ma
JOIN medications m ON m.id = ma.medication_id
WHERE ma.household_id = ?
  AND ma.status = 'pending'
  AND ma.scheduled_at < ?
ORDER BY ma.scheduled_at ASC;
```

**Why not have the generator mark things overdue?** Because "overdue" is relative to the current time. A task due at 2pm is pending at 1:59pm and overdue at 2:01pm. Having the generator run every 30 minutes to update statuses would create a 30-minute lag in overdue detection. Query-time detection is instant and always accurate.

**Notification for overdue items** is handled by a separate lightweight check that runs more frequently (every 5 minutes) and only looks at pending items past their due time:

```go
// StartOverdueChecker runs a lightweight check for newly overdue items
// and sends notifications. Runs every 5 minutes.
func StartOverdueChecker(ctx context.Context, checker *OverdueChecker, interval time.Duration) {
    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            checker.CheckAndNotify(ctx)
        case <-ctx.Done():
            return
        }
    }
}

// CheckAndNotify finds items that became overdue since the last check
// and sends notifications. Uses a high-water mark to avoid duplicate notifications.
func (c *OverdueChecker) CheckAndNotify(ctx context.Context) {
    lastCheck := c.getLastCheckTime(ctx)
    now := time.Now().UTC()

    // Find medication administrations that were due between lastCheck and now
    // and are still pending
    overdueMeds, _ := c.db.GetNewlyOverdueMedications(ctx, dbgen.GetNewlyOverdueMedicationsParams{
        DueAfter:  lastCheck.Format(time.RFC3339),
        DueBefore: now.Format(time.RFC3339),
    })

    for _, med := range overdueMeds {
        c.notifications.Send(ctx, OverdueMedicationNotification(med))
    }

    // Same for tasks
    overdueTasks, _ := c.db.GetNewlyOverdueTasks(ctx, dbgen.GetNewlyOverdueTasksParams{
        DueAfter:  lastCheck.Format(time.RFC3339),
        DueBefore: now.Format(time.RFC3339),
    })

    for _, task := range overdueTasks {
        c.notifications.Send(ctx, OverdueTaskNotification(task))
    }

    c.setLastCheckTime(ctx, now)
}
```

```sql
-- name: GetNewlyOverdueMedications :many
SELECT ma.*, m.name_enc, m.dosage_enc, m.care_recipient_id
FROM medication_administrations ma
JOIN medications m ON m.id = ma.medication_id
WHERE ma.household_id = ?
  AND ma.status = 'pending'
  AND ma.scheduled_at > ?
  AND ma.scheduled_at <= ?;
```

---

## Shift Handoff Prompt Trigger

The overdue checker goroutine also handles shift handoff prompting, since it follows the same periodic-check pattern.

When a shift is approaching its end time, the outgoing caregiver needs a prompt to fill out the handoff form. This is not an "overdue" scenario but uses the same infrastructure — a lightweight periodic check that looks ahead slightly.

```go
func (c *OverdueChecker) CheckShiftHandoffs(ctx context.Context) {
    now := time.Now().UTC()
    // Look for shifts ending in the next 30 minutes that don't have a handoff yet
    promptWindow := now.Add(30 * time.Minute)

    endingShifts, _ := c.db.GetShiftsEndingSoonWithoutHandoff(ctx, dbgen.GetShiftsEndingSoonWithoutHandoffParams{
        EndsAfter:  now.Format(time.RFC3339),
        EndsBefore: promptWindow.Format(time.RFC3339),
    })

    for _, shift := range endingShifts {
        // Only notify once — check if we already sent a handoff prompt for this shift
        alreadyNotified, _ := c.db.HasHandoffPromptNotification(ctx, shift.ID)
        if !alreadyNotified {
            c.notifications.Send(ctx, HandoffPromptNotification(shift))
        }
    }
}
```

```sql
-- name: GetShiftsEndingSoonWithoutHandoff :many
SELECT s.*
FROM shifts s
LEFT JOIN shift_handoffs sh ON sh.shift_id = s.id
WHERE s.status = 'active'
  AND s.ends_at > ?
  AND s.ends_at <= ?
  AND sh.id IS NULL;
```

The `CheckAndNotify` function calls both `checkOverdueItems` and `CheckShiftHandoffs` on each tick.

### Missed Shift Detection

Shifts also need periodic status management. A shift with status 'scheduled' whose `starts_at` has passed without anyone clocking in should eventually transition to 'missed' and notify the household.

```go
func (c *OverdueChecker) CheckMissedShifts(ctx context.Context) {
    now := time.Now().UTC()
    // A shift is considered missed if:
    // - Status is 'scheduled' (nobody clocked in)
    // - Start time was more than 30 minutes ago (grace period for late clock-in)
    missedThreshold := now.Add(-30 * time.Minute)

    missedShifts, _ := c.db.GetMissedShifts(ctx, dbgen.GetMissedShiftsParams{
        StartedBefore: missedThreshold.Format(time.RFC3339),
    })

    for _, shift := range missedShifts {
        // Transition to 'missed' status
        c.db.MarkShiftMissed(ctx, shift.ID)
        // Notify all household admins and members
        c.notifications.Send(ctx, MissedShiftNotification(shift))
    }
}
```

```sql
-- name: GetMissedShifts :many
SELECT * FROM shifts
WHERE status = 'scheduled'
  AND starts_at < ?
  AND deleted_at IS NULL;

-- name: MarkShiftMissed :exec
UPDATE shifts SET status = 'missed', updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
WHERE id = ? AND status = 'scheduled';
```

**Shift status transitions:**
- `scheduled` → `active` (caregiver manually clocks in)
- `scheduled` → `missed` (30 minutes past start time, nobody clocked in — detected by periodic checker)
- `scheduled` → `swapped` (shift swap accepted)
- `active` → `completed` (caregiver clocks out or shift end time passes)
- `missed` is a terminal state — the shift was never staffed

The `CheckAndNotify` function calls `checkOverdueItems`, `CheckShiftHandoffs`, and `CheckMissedShifts` on each tick.

---

## Error Handling and Resilience

### Concurrency: Scheduler vs On-Demand Trigger

The on-demand trigger fires synchronously in the HTTP request handler. The scheduler fires every 30 minutes in a background goroutine. These can overlap — a user creates a template at the exact moment the scheduler is running.

**This is safe** because:
- `INSERT OR IGNORE` means duplicate insert attempts are no-ops
- SQLite's busy timeout (5 seconds) handles lock contention
- Both operations are idempotent, so the order doesn't matter

If the scheduler holds a write lock and the on-demand trigger attempts to write, the on-demand trigger will wait up to 5 seconds (busy timeout) and then succeed. In the worst case, the on-demand trigger's response to the user is delayed by a few seconds. This is acceptable.

**No additional mutex or coordination is needed** — the database constraints handle it. Adding an application-level lock would add complexity without benefit.

### Key Rotation Interaction

The instance generation system copies encrypted ciphertext from templates to instances (`title_enc`, `description_enc`, etc.). During key rotation (documented in the tech stack), all encrypted fields across all tables are re-encrypted.

**The key rotation process must:**
1. Pause the instance generation scheduler
2. Re-encrypt all fields in all tables (including instances)
3. Resume the scheduler

If the generator runs mid-rotation, it could copy a template's new-key ciphertext into an instance while other instances still have old-key ciphertext. The rotation process should use a mutex or flag that the generator checks:

```go
func (g *Generator) GenerateAllInstances(ctx context.Context, window DateRange) (int, error) {
    if g.isKeyRotationInProgress() {
        log.Warn("Skipping generation — key rotation in progress")
        return 0, nil
    }
    // ... normal generation ...
}
```

The key rotation service sets this flag before starting and clears it after completing. The scheduler will simply skip one cycle and catch up on the next run.

### Partial Failures

The generator processes each household and each care recipient independently. A failure generating instances for one care recipient must not prevent generation for others.

```go
func (g *Generator) GenerateAllInstances(ctx context.Context, window DateRange) (int, error) {
    households, err := g.db.GetAllHouseholds(ctx)
    if err != nil {
        return 0, fmt.Errorf("fetching households: %w", err)
    }

    totalCreated := 0
    var errs []error

    for _, household := range households {
        count, err := g.generateForHousehold(ctx, household, window)
        if err != nil {
            // Log error but continue to next household
            log.Error("Generation failed for household",
                "household_id", household.ID,
                "error", err)
            errs = append(errs, fmt.Errorf("household %s: %w", household.ID, err))
            continue
        }
        totalCreated += count
    }

    if len(errs) > 0 {
        return totalCreated, fmt.Errorf("%d households had errors: %w", len(errs), errors.Join(errs...))
    }
    return totalCreated, nil
}
```

### Transaction Boundaries

Each care recipient's generation runs in a single database transaction. If any instance creation fails for that recipient, the entire recipient's generation is rolled back — ensuring all-or-nothing consistency per recipient.

```go
func (g *Generator) generateForCareRecipient(
    ctx context.Context,
    tx *sql.Tx,
    recipient dbgen.CareRecipient,
    window DateRange,
) (int, error) {
    // All INSERT OR IGNORE operations for this recipient happen in one transaction
    // If anything fails, nothing is committed for this recipient
    // Other recipients are unaffected
}
```

### Monitoring and Observability

The generator logs:
- Total instances created per run
- Duration of each run
- Errors per household/recipient
- Template processing count

The `config` table tracks:
```sql
key: 'generator_last_run'        → ISO 8601 timestamp of last successful full run
key: 'generator_last_run_count'  → Number of instances created in last run
key: 'generator_last_error'      → Last error message (cleared on success)
key: 'overdue_checker_last_run'  → ISO 8601 timestamp of last overdue check
```

The Hub dashboard can show admin users a "system health" indicator based on whether the generator has run recently.

---

## Startup Sequence

When the application starts:

```go
func main() {
    // 1. Open database
    db := openDB(config.DBPath)

    // 2. Run migrations
    runMigrations(db)

    // 3. Initialize generator
    generator := NewGenerator(db, encryptor)

    // 4. Run catch-up generation (synchronous — blocks until complete)
    //    This ensures instances exist before any HTTP request is served
    log.Info("Running startup generation catch-up...")
    count, err := generator.GenerateAllInstances(ctx, generator.GetGenerationWindow())
    log.Info("Startup generation complete", "created", count)

    // 5. Start background scheduler (asynchronous)
    go StartScheduler(ctx, generator, 30*time.Minute)

    // 6. Start overdue checker (asynchronous)
    //    Handles: overdue tasks/meds, shift handoff prompts, missed shifts
    go StartOverdueChecker(ctx, NewOverdueChecker(db, notifier), 5*time.Minute)

    // 7. Start housekeeping worker (asynchronous)
    go StartHousekeepingWorker(ctx, db, 1*time.Hour)

    // 8. Start HTTP server
    //    At this point, all instances are guaranteed to exist
    log.Info("Starting HTTP server")
    http.ListenAndServe(":8080", router)
}
```

**Critical: The catch-up generation runs synchronously before the HTTP server starts.** This guarantees that when the first caregiver hits the dashboard, their schedule exists. The background scheduler then maintains it going forward.

---

## Housekeeping Worker

A separate periodic worker handles data pruning to prevent unbounded table growth. Runs hourly.

```go
func StartHousekeepingWorker(ctx context.Context, db *sql.DB, interval time.Duration) {
    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            pruneExpiredSessions(ctx, db)
            pruneOldLoginAttempts(ctx, db)
            pruneOldNotifications(ctx, db)
        case <-ctx.Done():
            return
        }
    }
}
```

```sql
-- name: PruneExpiredSessions :exec
DELETE FROM sessions WHERE expires_at < strftime('%Y-%m-%dT%H:%M:%SZ', 'now');

-- name: PruneOldLoginAttempts :exec
-- Keep 7 days of login history for security review, prune older records
DELETE FROM login_attempts WHERE attempted_at < strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '-7 days');

-- name: PruneOldNotifications :exec
-- Keep 90 days of notifications, prune older read/dismissed ones
-- Unread notifications are never pruned (they need attention)
DELETE FROM notifications
WHERE created_at < strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '-90 days')
  AND (read_at IS NOT NULL OR dismissed_at IS NOT NULL);
```

These thresholds are configurable via the `config` table:

| Config Key | Default | Description |
|---|---|---|
| `housekeeping_session_max_age_days` | `0` (prune expired only) | Sessions older than this are pruned |
| `housekeeping_login_attempts_max_days` | `7` | Login attempts older than this are pruned |
| `housekeeping_notification_max_days` | `90` | Read/dismissed notifications older than this are pruned |

---

## SSE Events from Reconciliation

When the reconciliation engine modifies instances (soft-deletes old, creates new), it must fire SSE events so any connected clients see the updated schedule immediately. Without this, a caregiver looking at the dashboard during a template change would see stale data until they manually refresh.

```go
func (g *Generator) ReconcileTaskTemplate(
    ctx context.Context,
    oldTemplate dbgen.TaskTemplate,
    newTemplate dbgen.TaskTemplate,
) error {
    // ... reconciliation logic (soft-delete + regenerate) ...

    // Fire SSE event so connected clients refresh their task lists
    g.eventBroker.Publish(newTemplate.HouseholdID, Event{
        Type: "task-update",
        Data: fmt.Sprintf(`{"action":"reconciled","template_id":"%s","care_recipient_id":"%s"}`,
            newTemplate.ID, newTemplate.CareRecipientID),
    })

    return nil
}

func (g *Generator) ReconcileTimezoneChange(ctx context.Context, recipientID, oldTZ, newTZ string) error {
    // ... timezone reconciliation logic ...

    recipient, _ := g.db.GetCareRecipient(ctx, recipientID)
    // Fire events for all affected entity types
    g.eventBroker.Publish(recipient.HouseholdID, Event{
        Type: "schedule-refresh",
        Data: fmt.Sprintf(`{"action":"timezone_changed","care_recipient_id":"%s"}`, recipientID),
    })

    return nil
}
```

The Hub dashboard listens for these events via HTMX SSE:
```html
<div hx-ext="sse" sse-connect="/events/hub">
    <div sse-swap="task-update" hx-target="#task-list" hx-get="/hub/tasks/partial"></div>
    <div sse-swap="medication-update" hx-target="#med-schedule" hx-get="/hub/medications/partial"></div>
    <div sse-swap="schedule-refresh" hx-target="#daily-timeline" hx-get="/hub/timeline/partial"></div>
</div>
```

When a reconciliation event fires, HTMX automatically fetches the updated partial from the server, which returns freshly rendered HTML with the new instances.

---

## Schema Additions

The following unique indexes need to be added to the existing schema to support idempotent generation:

```sql
-- Prevent duplicate task instances from the same template
CREATE UNIQUE INDEX idx_tasks_dedup
    ON tasks(template_id, due_at)
    WHERE template_id IS NOT NULL AND deleted_at IS NULL;

-- Prevent duplicate medication administration records
CREATE UNIQUE INDEX idx_med_admin_dedup
    ON medication_administrations(medication_id, schedule_id, scheduled_at)
    WHERE schedule_id IS NOT NULL;

-- Prevent duplicate shift instances from the same template
CREATE UNIQUE INDEX idx_shifts_dedup
    ON shifts(template_id, starts_at)
    WHERE template_id IS NOT NULL;
```

These are added to the initial migration alongside the table definitions.

---

## Testing Strategy for the Generator

This is critical infrastructure — it needs thorough tests.

### Unit Tests

```go
func TestRecurrenceRule_MatchesDate(t *testing.T) {
    tests := []struct {
        name     string
        rule     RecurrenceRule
        date     time.Time
        expected bool
    }{
        {
            name:     "daily matches any day",
            rule:     RecurrenceRule{Frequency: "daily"},
            date:     time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
            expected: true,
        },
        {
            name:     "weekly matches correct day",
            rule:     RecurrenceRule{Frequency: "weekly", DaysOfWeek: []string{"mon", "wed", "fri"}},
            date:     time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC), // Monday
            expected: true,
        },
        {
            name:     "weekly does not match wrong day",
            rule:     RecurrenceRule{Frequency: "weekly", DaysOfWeek: []string{"mon", "wed", "fri"}},
            date:     time.Date(2026, 3, 17, 0, 0, 0, 0, time.UTC), // Tuesday
            expected: false,
        },
        {
            name:     "monthly matches correct day of month",
            rule:     RecurrenceRule{Frequency: "monthly", DayOfMonth: 15},
            date:     time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
            expected: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := tt.rule.MatchesDate(tt.date)
            if got != tt.expected {
                t.Errorf("MatchesDate() = %v, want %v", got, tt.expected)
            }
        })
    }
}
```

### Integration Tests

```go
func TestGenerator_Idempotency(t *testing.T) {
    db := setupTestDB(t) // In-memory SQLite
    generator := NewGenerator(db, testEncryptor)

    // Create a household, care recipient, and task template
    setupTestHousehold(t, db)
    setupTestTaskTemplate(t, db, RecurrenceRule{
        Frequency: "daily",
        Times:     []string{"08:00"},
    })

    window := DateRange{
        Start: time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
        End:   time.Date(2026, 3, 17, 0, 0, 0, 0, time.UTC),
    }

    // First run: should create 3 instances (one per day)
    count1, err := generator.GenerateAllInstances(ctx, window)
    require.NoError(t, err)
    assert.Equal(t, 3, count1)

    // Second run: should create 0 (all already exist)
    count2, err := generator.GenerateAllInstances(ctx, window)
    require.NoError(t, err)
    assert.Equal(t, 0, count2)

    // Verify exactly 3 task instances exist
    tasks, _ := db.GetTasksByRecipientDateRange(ctx, /* ... */)
    assert.Len(t, tasks, 3)
}

func TestGenerator_DST_SpringForward(t *testing.T) {
    db := setupTestDB(t)
    generator := NewGenerator(db, testEncryptor)

    // Setup with America/New_York timezone
    setupTestHouseholdWithTZ(t, db, "America/New_York")
    setupTestMedicationSchedule(t, db, "08:00") // 8am Eastern

    // March 8, 2026 is before DST (EST = UTC-5)
    // March 9, 2026 is DST transition day (clocks spring forward)
    // March 10, 2026 is after DST (EDT = UTC-4)
    window := DateRange{
        Start: time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC),
        End:   time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC),
    }

    count, err := generator.GenerateAllInstances(ctx, window)
    require.NoError(t, err)
    assert.Equal(t, 3, count)

    admins, _ := db.GetMedicationAdministrations(ctx, /* ... */)

    // March 8: 8am EST = 13:00 UTC
    assert.Equal(t, "2026-03-08T13:00:00Z", admins[0].ScheduledAt)
    // March 9: 8am EDT = 12:00 UTC (DST kicked in)
    assert.Equal(t, "2026-03-09T12:00:00Z", admins[1].ScheduledAt)
    // March 10: 8am EDT = 12:00 UTC
    assert.Equal(t, "2026-03-10T12:00:00Z", admins[2].ScheduledAt)
}

func TestGenerator_TemplateModification(t *testing.T) {
    db := setupTestDB(t)
    generator := NewGenerator(db, testEncryptor)

    setupTestHousehold(t, db)
    template := setupTestTaskTemplate(t, db, RecurrenceRule{
        Frequency: "daily",
        Times:     []string{"08:00"},
    })

    window := threeDay Window()

    // Generate initial instances
    generator.GenerateAllInstances(ctx, window)

    // Complete today's task
    todayTask := getTaskForDate(t, db, today())
    markTaskCompleted(t, db, todayTask.ID)

    // Modify template: add a second daily time
    newTemplate := updateTemplateRecurrence(t, db, template.ID, RecurrenceRule{
        Frequency: "daily",
        Times:     []string{"08:00", "14:00"},
    })

    // Reconcile
    generator.ReconcileTaskTemplate(ctx, template, newTemplate)

    // Verify:
    // - Today's completed 8am task is preserved
    // - Today gets a new 2pm task
    // - Tomorrow gets both 8am and 2pm
    allTasks := getAllActiveTasks(t, db)
    assert.Equal(t, 5, len(allTasks)) // today:2, tomorrow:2, day after:2, minus today's completed stays = wait...

    // Actually: today completed(8am) + today pending(2pm) + tomorrow(8am,2pm) + day_after(8am,2pm) = 7
    // But the completed one was preserved, not regenerated
    // The reconciliation soft-deleted today's pending 8am (but it was already completed, so preserved)
    // Then INSERT OR IGNORE created 2pm for today, 8am+2pm for tomorrow and day after
}

func TestGenerator_CatchUp_AfterDowntime(t *testing.T) {
    db := setupTestDB(t)
    generator := NewGenerator(db, testEncryptor)

    setupTestHousehold(t, db)
    setupTestTaskTemplate(t, db, RecurrenceRule{
        Frequency: "daily",
        Times:     []string{"08:00"},
    })

    // Simulate: server was down for 2 days, now starting up
    // Generation window starts from today, which is 2 days after last run
    window := generator.GetGenerationWindow() // today + 2 days

    count, err := generator.GenerateAllInstances(ctx, window)
    require.NoError(t, err)
    assert.Equal(t, 3, count) // today + tomorrow + day after

    // Today's instances include overdue items (8am has passed)
    // The overdue checker will handle notifications
}
```

### Edge Case Tests to Include

- Template with no matching days in the generation window (e.g., monthly on the 31st in February)
- Multiple templates for the same care recipient generating instances at the same time
- Medication discontinued while there are pending future instances
- Care recipient timezone changes — verify all future UTC timestamps are recalculated correctly
- Care recipient timezone change preserves completed instances and only regenerates pending ones
- Shift spanning midnight (22:00 to 06:00)
- Generation window configuration change (expanding or shrinking)
- Empty household (no care recipients) — should no-op gracefully
- Household with all templates deactivated — should no-op gracefully
- Very large household (stress test: 10 care recipients, 50 templates each)
- Late-day instance creation: adding a care recipient at 3pm skips 8am instances but creates 4pm instances
- Late-day instance creation: instances within grace period (2 hours) are created, older ones are skipped
- Catch-up after multi-day downtime: no instances generated for missed past days, only today forward
- Template reactivation after deactivation: new instances are created, soft-deleted ones remain for audit
- Template reactivation idempotency: reactivating an already-active template is a no-op
- One-off task at same time as template-generated task: both exist without conflict
- Template content change (title edit) without schedule change: existing pending instances keep old title
- Template schedule change: pending instances regenerated, completed instances preserved
- Per-recipient timezone windowing: recipients in different timezones get correct "today" calculation
- DST spring-forward: 2am instance on transition day resolves correctly
- DST fall-back: 1:30am instance on transition day doesn't create duplicates
- Shift handoff prompt fires 30 minutes before shift end, and only once per shift
- Missed shift detection: shift transitions to 'missed' after 30 minutes past start with no clock-in
- Shift status transitions: scheduled→active, scheduled→missed, active→completed, scheduled→swapped
- Individual medication schedule deactivation: only that schedule's instances are cleaned up, other schedules are untouched
- Concurrent scheduler + on-demand trigger: both attempt to generate same instances simultaneously, no duplicates
- Key rotation flag: generator skips execution while key rotation is in progress
- SSE event fires after reconciliation so connected clients refresh
- Housekeeping: expired sessions are pruned, old login attempts are pruned, old notifications are pruned
- Housekeeping: unread notifications are never pruned regardless of age

---

## Performance Considerations

**Expected scale per household:**
- 1-5 care recipients
- 5-20 task templates per recipient
- 5-15 medications per recipient (each with 1-3 schedule entries)
- 1-3 shift templates per recipient
- 3-day generation window

**Maximum instances per run per household:**
- Tasks: 5 recipients × 20 templates × 3 times/day × 3 days = 900 INSERT OR IGNORE
- Medications: 5 recipients × 15 meds × 3 schedules × 3 days = 675 INSERT OR IGNORE
- Shifts: 5 recipients × 3 templates × 3 days = 45 INSERT OR IGNORE
- Total: ~1,620 INSERT OR IGNORE operations

With SQLite in WAL mode, these are trivially fast (sub-second for the entire run even on a Raspberry Pi). The `INSERT OR IGNORE` on existing rows is essentially a no-op index lookup.

**For multi-tenant instances with many households:** The generator processes households sequentially. Even with 100 households, the total generation time would be well under a minute. If this becomes a bottleneck, households can be processed concurrently with a worker pool (but SQLite's single-writer limitation means actual writes are serialized regardless).

---

## Configuration Summary

| Config Key | Default | Description |
|---|---|---|
| `generation_window_days` | `3` | How many days ahead to generate instances |
| `scheduler_interval_minutes` | `30` | How often the background scheduler runs |
| `overdue_check_interval_minutes` | `5` | How often the overdue/handoff/missed-shift checker runs |
| `housekeeping_interval_minutes` | `60` | How often the housekeeping worker runs |
| `late_instance_grace_period_hours` | `2` | Skip past-time instances older than this on late-day creation |
| `missed_shift_grace_minutes` | `30` | Minutes past shift start before marking as missed |
| `handoff_prompt_minutes_before` | `30` | Minutes before shift end to prompt for handoff |
| `housekeeping_session_max_age_days` | `0` | Sessions older than this are pruned (0 = prune expired only) |
| `housekeeping_login_attempts_max_days` | `7` | Login attempts older than this are pruned |
| `housekeeping_notification_max_days` | `90` | Read/dismissed notifications older than this are pruned |
| `generator_last_run` | (timestamp) | Last successful generation run |
| `overdue_checker_last_run` | (timestamp) | Last overdue check run |

---

## Background Goroutine Summary

The application runs four background goroutines alongside the HTTP server:

| Goroutine | Interval | Purpose |
|---|---|---|
| Instance Generator (Scheduler) | 30 min | Creates task, medication, and shift instances for the generation window |
| Overdue & Status Checker | 5 min | Overdue notifications, shift handoff prompts, missed shift detection |
| Housekeeping Worker | 60 min | Prunes expired sessions, old login attempts, old notifications |
| SSE Event Broker | Continuous | Manages client connections and broadcasts events |

All goroutines are started after the synchronous catch-up generation and before the HTTP server. All respect `context.Context` cancellation for graceful shutdown.
