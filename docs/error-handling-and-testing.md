# Caregiver Coordinator Hub — Error Handling & Testing Strategy

*This document defines the error handling patterns and testing strategy for the Caregiver Coordinator Hub. It should be provided alongside the other design documents to any AI assistant or developer working on the project to ensure consistent patterns across all features.*

---

## Part 1: Error Handling

### Design Goals

- **Users never see raw errors.** A caregiver at 2am dealing with a sick parent should never encounter "sql: no rows in result set" or a stack trace. Every error surfaces as a human-readable message.
- **Errors are categorized by audience.** Some errors are the user's fault (bad input), some are the system's fault (database down), and some are authorization failures. Each category has different handling.
- **HTMX-friendly.** Most interactions happen via HTMX partial updates. Errors must render as HTML fragments that HTMX can swap into the page, not as JSON API responses.
- **Consistent across all features.** Every feature package uses the same error types, the same middleware, and the same rendering patterns.
- **Errors never leak sensitive information.** Internal details (table names, SQL errors, file paths) are logged server-side but never sent to the client.

---

### Error Type Hierarchy

```go
// internal/apperror/errors.go

package apperror

import "fmt"

// Type represents the category of error.
type Type int

const (
    TypeValidation      Type = iota // User input is invalid (400)
    TypeNotFound                    // Resource doesn't exist (404)
    TypeUnauthorized                // Not logged in (401)
    TypeForbidden                   // Logged in but not allowed (403)
    TypeConflict                    // Action conflicts with current state (409)
    TypeRateLimited                 // Too many attempts (429)
    TypeUnavailable                 // Service temporarily unavailable (503) — key rotation, backup, etc.
    TypeInternal                    // Something broke on our end (500)
)

// Error is the application's standard error type.
// All errors returned from the service layer should be this type.
type Error struct {
    Type       Type              // Category (determines HTTP status and rendering)
    Message    string            // Human-readable message safe to show to the user
    Field      string            // For validation errors: which form field is invalid
    RetryAfter time.Duration     // For rate-limited errors: how long to wait
    Err        error             // Wrapped underlying error (logged, never shown to user)
}

func (e *Error) Error() string {
    if e.Err != nil {
        return fmt.Sprintf("%s: %v", e.Message, e.Err)
    }
    return e.Message
}

func (e *Error) Unwrap() error {
    return e.Err
}

// Constructor functions — use these instead of creating Error structs directly.

func Validation(field, message string) *Error {
    return &Error{Type: TypeValidation, Message: message, Field: field}
}

func NotFound(entity, id string) *Error {
    return &Error{Type: TypeNotFound, Message: fmt.Sprintf("%s not found", entity)}
}

func Unauthorized(message string) *Error {
    return &Error{Type: TypeUnauthorized, Message: message}
}

func Forbidden(message string) *Error {
    return &Error{Type: TypeForbidden, Message: message}
}

func Conflict(message string) *Error {
    return &Error{Type: TypeConflict, Message: message}
}

func Internal(message string, err error) *Error {
    return &Error{Type: TypeInternal, Message: message, Err: err}
}

func RateLimited(message string, retryAfter time.Duration) *Error {
    return &Error{Type: TypeRateLimited, Message: message, RetryAfter: retryAfter}
}

func Unavailable(message string) *Error {
    return &Error{Type: TypeUnavailable, Message: message}
}

// ConflictWithErr wraps an underlying error (e.g., unique constraint violation)
func ConflictWithErr(message string, err error) *Error {
    return &Error{Type: TypeConflict, Message: message, Err: err}
}
```

**Why a custom error type instead of sentinel errors?**

Sentinel errors (`var ErrNotFound = errors.New("not found")`) don't carry context — you can't attach a user-friendly message or a field name for validation. The custom `Error` type carries everything needed to render a proper response: the category (which determines HTTP status code), the user-facing message, the optional field name (for inline form validation), and the wrapped internal error (for logging).

---

### HTTP Status Code Mapping

```go
func HTTPStatus(err *Error) int {
    switch err.Type {
    case TypeValidation:
        return http.StatusBadRequest           // 400
    case TypeNotFound:
        return http.StatusNotFound             // 404
    case TypeUnauthorized:
        return http.StatusUnauthorized         // 401
    case TypeForbidden:
        return http.StatusForbidden            // 403
    case TypeConflict:
        return http.StatusConflict             // 409
    case TypeRateLimited:
        return http.StatusTooManyRequests       // 429
    case TypeUnavailable:
        return http.StatusServiceUnavailable    // 503
    case TypeInternal:
        return http.StatusInternalServerError  // 500
    default:
        return http.StatusInternalServerError  // 500
    }
}
```

---

### Error Flow: Service → Handler → User

```
┌──────────┐     ┌──────────┐     ┌──────────────┐     ┌──────────┐
│  Database │ ──> │ Service  │ ──> │   Handler    │ ──> │   User   │
│           │     │  Layer   │     │              │     │          │
│ raw SQL   │     │ wraps in │     │ checks for   │     │ sees     │
│ errors    │     │ apperror │     │ HTMX header, │     │ friendly │
│           │     │          │     │ renders      │     │ message  │
│           │     │          │     │ appropriate  │     │          │
│           │     │          │     │ response     │     │          │
└──────────┘     └──────────┘     └──────────────┘     └──────────┘
```

**Service layer** is responsible for:
- Catching raw database/system errors and wrapping them in `apperror.Error`
- Performing validation and returning `apperror.Validation` errors
- Never returning raw `error` values to handlers

```go
// internal/medications/service.go

func (s *MedicationService) Create(ctx context.Context, input CreateMedicationInput) (*Medication, error) {
    // Validation
    if input.Name == "" {
        return nil, apperror.Validation("name", "Medication name is required")
    }
    if input.Dosage == "" {
        return nil, apperror.Validation("dosage", "Dosage is required")
    }

    // Encrypt fields
    nameEnc, err := s.enc.Encrypt(input.Name)
    if err != nil {
        return nil, apperror.Internal("Failed to save medication", err)
    }

    // Database operation
    row, err := s.db.CreateMedication(ctx, dbgen.CreateMedicationParams{/* ... */})
    if err != nil {
        return nil, apperror.Internal("Failed to save medication", err)
    }

    return s.rowToMedication(row)
}

func (s *MedicationService) GetByID(ctx context.Context, householdID, medicationID string) (*Medication, error) {
    row, err := s.db.GetMedication(ctx, dbgen.GetMedicationParams{
        ID:          medicationID,
        HouseholdID: householdID,
    })
    if err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return nil, apperror.NotFound("medication", medicationID)
        }
        return nil, apperror.Internal("Failed to load medication", err)
    }

    return s.rowToMedication(row)
}
```

**Handler layer** is responsible for:
- Calling the service layer
- Detecting error type and rendering the appropriate response
- Distinguishing HTMX requests from full page loads

---

### HTMX Error Rendering

This is the critical pattern for the entire app. Most user interactions are HTMX partial updates (clicking "Mark Done" on a task, submitting a form, etc.). Errors must render as HTML fragments that HTMX can swap into the correct place.

#### Error Response Helper

```go
// internal/web/respond.go

package web

import (
    "errors"
    "log/slog"
    "net/http"

    "caregiver-hub/internal/apperror"
)

// RespondError handles error responses for both HTMX and full-page requests.
func RespondError(w http.ResponseWriter, r *http.Request, err error) {
    var appErr *apperror.Error
    if !errors.As(err, &appErr) {
        // Unknown error type — treat as internal
        appErr = apperror.Internal("Something went wrong", err)
    }

    // Always log internal errors with full detail
    if appErr.Type == apperror.TypeInternal {
        slog.Error("Internal error",
            "error", appErr.Err,
            "message", appErr.Message,
            "path", r.URL.Path,
            "method", r.Method,
        )
    }

    status := apperror.HTTPStatus(appErr)

    if isHTMX(r) {
        renderHTMXError(w, r, appErr, status)
    } else {
        renderFullPageError(w, r, appErr, status)
    }
}

func isHTMX(r *http.Request) bool {
    return r.Header.Get("HX-Request") == "true"
}

func renderHTMXError(w http.ResponseWriter, r *http.Request, appErr *apperror.Error, status int) {
    w.WriteHeader(status)

    switch appErr.Type {
    case apperror.TypeValidation:
        // Render inline validation error next to the form field
        components.FormFieldError(appErr.Field, appErr.Message).Render(r.Context(), w)

    case apperror.TypeNotFound:
        components.AlertBanner("warning", appErr.Message).Render(r.Context(), w)

    case apperror.TypeUnauthorized:
        // Redirect to login via HTMX
        w.Header().Set("HX-Redirect", "/login")

    case apperror.TypeForbidden:
        components.AlertBanner("error", "You don't have permission to do this.").Render(r.Context(), w)

    case apperror.TypeRateLimited:
        // Include Retry-After header for clients that respect it
        if appErr.RetryAfter > 0 {
            w.Header().Set("Retry-After", fmt.Sprintf("%.0f", appErr.RetryAfter.Seconds()))
        }
        components.AlertBanner("warning", appErr.Message).Render(r.Context(), w)

    case apperror.TypeUnavailable:
        components.AlertBannerWithRetry(
            "The system is temporarily busy. Please try again shortly.",
            r.URL.Path,
        ).Render(r.Context(), w)

    default:
        // Internal error — generic message with request ID for support
        reqID := middleware.GetRequestID(r.Context())
        components.AlertBanner("error",
            fmt.Sprintf("Something went wrong. Please try again. (Ref: %s)", reqID),
        ).Render(r.Context(), w)
    }
}

func renderFullPageError(w http.ResponseWriter, r *http.Request, appErr *apperror.Error, status int) {
    w.WriteHeader(status)

    switch appErr.Type {
    case apperror.TypeNotFound:
        pages.NotFoundPage().Render(r.Context(), w)

    case apperror.TypeUnauthorized:
        http.Redirect(w, r, "/login", http.StatusSeeOther)

    case apperror.TypeForbidden:
        pages.ForbiddenPage().Render(r.Context(), w)

    default:
        pages.ErrorPage("Something went wrong. Please try again.").Render(r.Context(), w)
    }
}
```

#### Templ Error Components

```templ
// components/errors.templ

package components

// FormFieldError renders an inline validation error below a form field.
// Used by HTMX to swap into the field's error container.
templ FormFieldError(field string, message string) {
    <div id={ "error-" + field } class="label">
        <span class="label-text-alt text-error">{ message }</span>
    </div>
}

// AlertBanner renders a dismissible alert at the top of the content area.
templ AlertBanner(level string, message string) {
    <div class={ "alert", templ.KV("alert-error", level == "error"),
                          templ.KV("alert-warning", level == "warning"),
                          templ.KV("alert-info", level == "info") }
         role="alert">
        <span>{ message }</span>
        <button class="btn btn-sm btn-ghost" onclick="this.parentElement.remove()">✕</button>
    </div>
}

// AlertBannerWithRetry renders an error alert with a retry button.
templ AlertBannerWithRetry(message string, retryURL string) {
    <div class="alert alert-error" role="alert">
        <span>{ message }</span>
        <button class="btn btn-sm btn-outline"
                hx-get={ retryURL }
                hx-target="closest .alert"
                hx-swap="outerHTML">
            Retry
        </button>
    </div>
}
```

#### Form Validation Pattern

The standard pattern for forms in this app:

```templ
// medications/templates.templ

templ CreateMedicationForm() {
    <form hx-post="/medications"
          hx-target="#medication-list"
          hx-swap="beforeend"
          hx-target-error="#form-errors">

        // Global form errors appear here
        <div id="form-errors"></div>

        <div class="form-control w-full">
            <label class="label" for="name">
                <span class="label-text">Medication Name</span>
            </label>
            <input type="text" id="name" name="name"
                   class="input input-bordered w-full"
                   required />
            // Inline field error appears here
            <div id="error-name"></div>
        </div>

        <div class="form-control w-full">
            <label class="label" for="dosage">
                <span class="label-text">Dosage</span>
            </label>
            <input type="text" id="dosage" name="dosage"
                   class="input input-bordered w-full"
                   placeholder="e.g., 10mg"
                   required />
            <div id="error-dosage"></div>
        </div>

        <button type="submit" class="btn btn-primary">Add Medication</button>
    </form>
}
```

**How HTMX handles validation errors:**

HTMX has a built-in mechanism for targeting different elements on error responses. Using the `hx-target-error` attribute (from the `response-targets` extension) or by setting `HX-Retarget` in the response header:

```go
func renderHTMXError(w http.ResponseWriter, r *http.Request, appErr *apperror.Error, status int) {
    w.WriteHeader(status)

    if appErr.Type == apperror.TypeValidation && appErr.Field != "" {
        // Retarget to the specific field's error container
        w.Header().Set("HX-Retarget", "#error-"+appErr.Field)
        w.Header().Set("HX-Reswap", "innerHTML")
        components.FormFieldError(appErr.Field, appErr.Message).Render(r.Context(), w)
        return
    }

    // For non-field errors, target the form's global error area
    w.Header().Set("HX-Retarget", "#form-errors")
    w.Header().Set("HX-Reswap", "innerHTML")
    components.AlertBanner("error", appErr.Message).Render(r.Context(), w)
}
```

**Clearing errors on successful submission:**

When the form succeeds, the handler renders the new content (e.g., the new medication card) and also triggers clearing of any lingering error messages:

```go
func handleCreateMedication(w http.ResponseWriter, r *http.Request) {
    // ... parse form, call service ...

    if err != nil {
        web.RespondError(w, r, err)
        return
    }

    // Success — render the new medication card and clear errors
    w.Header().Set("HX-Trigger", "clearErrors")
    medications.MedicationCard(med).Render(r.Context(), w)
}
```

```html
<!-- In the layout, listen for clearErrors event -->
<body hx-on::clearErrors="document.querySelectorAll('[id^=error-]').forEach(e => e.innerHTML = '')">
```

---

### Multi-Field Validation

Some operations need to validate multiple fields and return all errors at once (not just the first one). The service layer can return a `ValidationErrors` collection:

```go
// internal/apperror/errors.go

type ValidationErrors struct {
    Errors []*Error
}

func (ve *ValidationErrors) Add(field, message string) {
    ve.Errors = append(ve.Errors, Validation(field, message))
}

func (ve *ValidationErrors) HasErrors() bool {
    return len(ve.Errors) > 0
}

func (ve *ValidationErrors) Error() string {
    return fmt.Sprintf("%d validation errors", len(ve.Errors))
}
```

```go
// In the service layer
func (s *CareRecipientService) Create(ctx context.Context, input CreateInput) (*CareRecipient, error) {
    ve := &apperror.ValidationErrors{}

    if input.Name == "" {
        ve.Add("name", "Name is required")
    }
    if input.Timezone == "" {
        ve.Add("timezone", "Timezone is required")
    } else if _, err := time.LoadLocation(input.Timezone); err != nil {
        ve.Add("timezone", "Invalid timezone")
    }

    if ve.HasErrors() {
        return nil, ve
    }

    // ... proceed with creation
}
```

```go
// In the error response helper
func RespondError(w http.ResponseWriter, r *http.Request, err error) {
    // Check for multi-field validation errors first
    var ve *apperror.ValidationErrors
    if errors.As(err, &ve) {
        renderMultiFieldValidation(w, r, ve)
        return
    }

    // ... existing single-error handling
}

func renderMultiFieldValidation(w http.ResponseWriter, r *http.Request, ve *apperror.ValidationErrors) {
    w.WriteHeader(http.StatusBadRequest)
    // Render all field errors at once using an OOB swap
    // (HTMX Out-of-Band swap updates multiple elements in one response)
    for _, fieldErr := range ve.Errors {
        components.FormFieldErrorOOB(fieldErr.Field, fieldErr.Message).Render(r.Context(), w)
    }
}
```

```templ
// Out-of-band swap — updates the specific field error element regardless of hx-target
templ FormFieldErrorOOB(field string, message string) {
    <div id={ "error-" + field } hx-swap-oob="innerHTML">
        <span class="label-text-alt text-error">{ message }</span>
    </div>
}
```

HTMX OOB (Out-of-Band) swaps allow a single response to update multiple elements. Each element with `hx-swap-oob` is extracted and swapped into the matching element by ID, regardless of the main `hx-target`. This means one response can set error messages on three different form fields simultaneously.

---

### Handler Pattern (Complete Example)

Every handler in the app follows this exact structure:

```go
// internal/medications/handler.go

func (h *MedicationHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
    // 1. Parse request
    if err := r.ParseForm(); err != nil {
        web.RespondError(w, r, apperror.Validation("", "Invalid form data"))
        return
    }

    // 2. Extract auth context
    user := auth.GetUser(r.Context())
    if user == nil {
        web.RespondError(w, r, apperror.Unauthorized("Please log in"))
        return
    }

    // 3. Build service input
    input := CreateMedicationInput{
        HouseholdID:     user.HouseholdID,
        CareRecipientID: r.PathValue("recipientId"),
        Name:            r.FormValue("name"),
        Dosage:          r.FormValue("dosage"),
        Form:            r.FormValue("form"),
        Instructions:    r.FormValue("instructions"),
        IsPRN:           r.FormValue("is_prn") == "on",
        CreatedBy:       user.ID,
    }

    // 4. Call service (all validation and business logic lives here)
    med, err := h.service.Create(r.Context(), input)
    if err != nil {
        web.RespondError(w, r, err)
        return
    }

    // 5. Render success response
    if web.IsHTMX(r) {
        // Return just the new medication card fragment
        w.Header().Set("HX-Trigger", "clearErrors")
        MedicationCard(med).Render(r.Context(), w)
    } else {
        // Full page redirect for non-HTMX requests
        http.Redirect(w, r, fmt.Sprintf("/care-recipients/%s/medications", input.CareRecipientID), http.StatusSeeOther)
    }
}
```

**Rules for handlers:**
1. Handlers are thin — parse, call service, render
2. No business logic in handlers — that's the service layer's job
3. No direct database calls in handlers
4. Always check for `apperror` types — never return raw errors to the user
5. Always distinguish HTMX requests from full page loads
6. Success responses include `HX-Trigger: clearErrors` to clean up any lingering validation messages

---

### Logging Strategy

```go
// Use Go's structured logging (slog) everywhere

import "log/slog"

// Internal errors — full detail for debugging
slog.Error("Failed to create medication",
    "error", err,
    "household_id", user.HouseholdID,
    "user_id", user.ID,
    "care_recipient_id", input.CareRecipientID,
)

// Warning — something unexpected but recoverable
slog.Warn("Medication supply below threshold",
    "medication_id", med.ID,
    "current_supply", med.CurrentSupply,
    "threshold", med.LowSupplyThreshold,
)

// Info — normal operations worth recording
slog.Info("Medication created",
    "medication_id", med.ID,
    "household_id", user.HouseholdID,
)
```

**Logging rules:**
- Never log encrypted values (the whole point of encryption is that they're not readable)
- Never log passwords or tokens (even hashed ones)
- Always log entity IDs for traceability
- Always log `household_id` so logs can be filtered per household for support
- Always log the request ID (see below) for correlation
- Use structured key-value pairs, not string formatting
- Internal errors (`TypeInternal`) are always logged at `Error` level
- Validation errors are NOT logged (they're expected user behavior, not system issues)

---

### Request ID / Correlation ID

Every HTTP request gets a unique ID for tracing through logs. When a user reports a problem, the request ID connects their error screen to the exact log entries.

```go
// internal/middleware/requestid.go

package middleware

import (
    "context"
    "crypto/rand"
    "encoding/hex"
    "net/http"
)

type contextKey string
const requestIDKey contextKey = "request_id"

func RequestID(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        id := generateRequestID()
        ctx := context.WithValue(r.Context(), requestIDKey, id)
        w.Header().Set("X-Request-ID", id)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}

func GetRequestID(ctx context.Context) string {
    id, _ := ctx.Value(requestIDKey).(string)
    return id
}

func generateRequestID() string {
    b := make([]byte, 8) // 16-character hex string
    rand.Read(b)
    return hex.EncodeToString(b)
}
```

**Usage in logging (automatic via middleware):**
```go
slog.Error("Failed to create medication",
    "request_id", middleware.GetRequestID(r.Context()),
    "error", err,
    "household_id", user.HouseholdID,
)
```

**Shown to users on internal errors:**
```
Something went wrong. Please try again. (Ref: a1b2c3d4e5f67890)
```

A user can report this reference code, and support can grep logs for it instantly.

---

### HTMX Global Configuration

HTMX requires specific configuration to handle errors correctly. Without this, non-2xx responses are silently ignored and validation errors never appear.

```html
<!-- In the base layout template, included on every page -->

<!-- HTMX core -->
<script src="/static/js/htmx.min.js"></script>
<!-- SSE extension for real-time updates -->
<script src="/static/js/htmx-sse.js"></script>

<script>
    // CRITICAL: Configure HTMX to process 4xx and 5xx responses
    // Without this, error responses are swallowed and the user sees nothing
    htmx.config.responseHandling = [
        { code: "204", swap: false },                    // No content — don't swap
        { code: "[23]..", swap: true },                   // 2xx/3xx — normal swap
        { code: "401", swap: false },                     // Unauthorized — handled via HX-Redirect
        { code: "404", swap: true, target: "#alerts" },   // Not found — show alert
        { code: "422", swap: true },                      // Validation — swap (retargeted by server)
        { code: "429", swap: true, target: "#alerts" },   // Rate limited — show alert
        { code: "[45]..", swap: true, target: "#alerts" }, // Other errors — show in alerts area
    ];

    // Global timeout — show a message if server doesn't respond in 10 seconds
    htmx.config.timeout = 10000;

    // Handle network errors (server down, connection lost)
    document.body.addEventListener("htmx:sendError", function(event) {
        document.getElementById("alerts").innerHTML =
            '<div class="alert alert-error">' +
            '<span>Connection lost. Please check your internet and try again.</span>' +
            '</div>';
    });

    // Handle timeout
    document.body.addEventListener("htmx:timeout", function(event) {
        document.getElementById("alerts").innerHTML =
            '<div class="alert alert-warning">' +
            '<span>The server is taking longer than expected. Please try again.</span>' +
            '</div>';
    });

    // Handle CSRF token expiry (server returns 403 with specific header)
    document.body.addEventListener("htmx:responseError", function(event) {
        if (event.detail.xhr.status === 403 &&
            event.detail.xhr.getResponseHeader("X-CSRF-Retry") === "true") {
            // CSRF token expired — refresh the page to get a new token
            document.getElementById("alerts").innerHTML =
                '<div class="alert alert-info">' +
                '<span>Your session needs refreshing. ' +
                '<a href="" class="link link-primary">Click here to reload.</a></span>' +
                '</div>';
        }
    });
</script>

<!-- Global alerts container — error responses target this -->
<div id="alerts" class="fixed top-4 right-4 z-50 w-96 space-y-2"></div>
```

**CSRF token injection for all HTMX requests:**

```html
<!-- Set CSRF token as a meta tag (rendered by the server) -->
<meta name="csrf-token" content="{{ .CSRFToken }}" />

<script>
    // Include CSRF token in all HTMX requests automatically
    document.body.addEventListener("htmx:configRequest", function(event) {
        event.detail.headers["X-CSRF-Token"] =
            document.querySelector('meta[name="csrf-token"]').content;
    });
</script>
```

**CSRF validation failure handling on the server:**

```go
func csrfErrorHandler(w http.ResponseWriter, r *http.Request) {
    // Signal to HTMX that this is a CSRF error, not a permission error
    w.Header().Set("X-CSRF-Retry", "true")

    if isHTMX(r) {
        w.WriteHeader(http.StatusForbidden)
        components.AlertBanner("info",
            "Your session has expired. Please refresh the page.",
        ).Render(r.Context(), w)
    } else {
        http.Redirect(w, r, r.URL.Path, http.StatusSeeOther)
    }
}

// Wire into gorilla/csrf
csrf.Protect(
    []byte(csrfKey),
    csrf.ErrorHandler(http.HandlerFunc(csrfErrorHandler)),
)
```

---

### Form Re-Population After Validation Errors

When a form submission fails validation, the user's previously entered data must be preserved. With HTMX, the cleanest approach is to re-render the entire form with the submitted values pre-filled and error messages attached.

```go
func (h *MedicationHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
    if err := r.ParseForm(); err != nil {
        web.RespondError(w, r, apperror.Validation("", "Invalid form data"))
        return
    }

    user := auth.GetUser(r.Context())
    input := CreateMedicationInput{
        HouseholdID:     user.HouseholdID,
        CareRecipientID: r.PathValue("recipientId"),
        Name:            r.FormValue("name"),
        Dosage:          r.FormValue("dosage"),
        Form:            r.FormValue("form"),
        Instructions:    r.FormValue("instructions"),
        IsPRN:           r.FormValue("is_prn") == "on",
        CreatedBy:       user.ID,
    }

    med, err := h.service.Create(r.Context(), input)
    if err != nil {
        // Check if it's a validation error — re-render form with values + errors
        var ve *apperror.ValidationErrors
        var appErr *apperror.Error
        if errors.As(err, &ve) {
            w.WriteHeader(http.StatusBadRequest)
            // HX-Retarget to replace the entire form, preserving user input
            w.Header().Set("HX-Retarget", "#medication-form")
            w.Header().Set("HX-Reswap", "outerHTML")
            CreateMedicationFormWithErrors(input, ve.Errors).Render(r.Context(), w)
            return
        }
        if errors.As(err, &appErr) && appErr.Type == apperror.TypeValidation {
            w.WriteHeader(http.StatusBadRequest)
            w.Header().Set("HX-Retarget", "#medication-form")
            w.Header().Set("HX-Reswap", "outerHTML")
            CreateMedicationFormWithErrors(input, []*apperror.Error{appErr}).Render(r.Context(), w)
            return
        }
        // Non-validation error — use standard error handling
        web.RespondError(w, r, err)
        return
    }

    // Success
    w.Header().Set("HX-Trigger", "clearErrors")
    MedicationCard(med).Render(r.Context(), w)
}
```

```templ
// The form template accepts optional pre-filled values and errors
templ CreateMedicationFormWithErrors(input CreateMedicationInput, errs []*apperror.Error) {
    <form id="medication-form"
          hx-post={ fmt.Sprintf("/care-recipients/%s/medications", input.CareRecipientID) }
          hx-target="#medication-list"
          hx-swap="beforeend">

        <div id="form-errors">
            for _, err := range errs {
                if err.Field == "" {
                    @AlertBanner("error", err.Message)
                }
            }
        </div>

        <div class="form-control w-full">
            <label class="label" for="name">
                <span class="label-text">Medication Name</span>
            </label>
            <input type="text" id="name" name="name"
                   value={ input.Name }
                   class={ "input input-bordered w-full", templ.KV("input-error", hasFieldError(errs, "name")) }
                   required />
            <div id="error-name">
                if msg := getFieldError(errs, "name"); msg != "" {
                    <span class="label-text-alt text-error">{ msg }</span>
                }
            </div>
        </div>

        <div class="form-control w-full">
            <label class="label" for="dosage">
                <span class="label-text">Dosage</span>
            </label>
            <input type="text" id="dosage" name="dosage"
                   value={ input.Dosage }
                   class={ "input input-bordered w-full", templ.KV("input-error", hasFieldError(errs, "dosage")) }
                   required />
            <div id="error-dosage">
                if msg := getFieldError(errs, "dosage"); msg != "" {
                    <span class="label-text-alt text-error">{ msg }</span>
                }
            </div>
        </div>

        <button type="submit" class="btn btn-primary">Add Medication</button>
    </form>
}
```

This pattern ensures the user never loses what they typed, and invalid fields are highlighted with `input-error` DaisyUI styling.

---

### Database Constraint Violation Handling

SQLite returns specific error codes for constraint violations. The service layer must detect these and convert them to appropriate `apperror` types.

```go
// internal/apperror/sqlite.go

package apperror

import (
    "strings"
)

// IsUniqueConstraintViolation checks if an error is a SQLite UNIQUE constraint failure.
// SQLite error messages follow the pattern: "UNIQUE constraint failed: table.column"
func IsUniqueConstraintViolation(err error) bool {
    if err == nil {
        return false
    }
    return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// ParseConstraintColumn extracts the column name from a SQLite constraint error.
// Input:  "UNIQUE constraint failed: users.email_hash"
// Output: "email_hash"
func ParseConstraintColumn(err error) string {
    msg := err.Error()
    idx := strings.LastIndex(msg, ".")
    if idx < 0 {
        return ""
    }
    return msg[idx+1:]
}
```

**Usage in service layer:**

```go
func (s *UserService) Create(ctx context.Context, input CreateUserInput) (*User, error) {
    // ... validation, encryption ...

    row, err := s.db.CreateUser(ctx, params)
    if err != nil {
        if apperror.IsUniqueConstraintViolation(err) {
            col := apperror.ParseConstraintColumn(err)
            switch col {
            case "email_hash":
                return nil, apperror.Conflict("A user with this email already exists")
            default:
                return nil, apperror.Conflict("This record already exists")
            }
        }
        return nil, apperror.Internal("Failed to create user", err)
    }

    return s.rowToUser(row)
}
```

**Common constraint violations to handle:**
- `users.email_hash` — duplicate email during registration or invite acceptance
- `invites.token_hash` — token collision (extremely unlikely with 32 bytes, but handle gracefully)
- `tasks(template_id, due_at)` — instance generation dedup (handled by `INSERT OR IGNORE`, but catch if called outside generator)
- `caregiver_assignments(user_id, care_recipient_id)` — duplicate assignment

---

### Audit Log Integration with Error Handling

Security-relevant failures should be recorded in the audit log, not just application logs. These create a persistent, queryable record of suspicious activity.

```go
// internal/middleware/audit.go

// AuditSecurityEvent records security-relevant events in the audit log.
// Called from error handling paths, not normal request flow.
func AuditSecurityEvent(ctx context.Context, db *dbgen.Queries, event AuditEvent) {
    // Best-effort — audit logging failures should not disrupt the user's request
    err := db.CreateAuditLog(ctx, dbgen.CreateAuditLogParams{
        ID:          ulid.Make().String(),
        HouseholdID: event.HouseholdID,
        UserID:      event.UserID,
        Action:      event.Action,
        EntityType:  event.EntityType,
        EntityID:    event.EntityID,
        Metadata:    event.Metadata,
        IPAddress:   event.IPAddress,
    })
    if err != nil {
        slog.Error("Failed to write audit log", "error", err, "event", event)
    }
}
```

**Events that should be audit-logged:**

| Event | Action | When |
|---|---|---|
| Failed login | `login_failed` | Wrong password (already in login_attempts, but also audit for admin visibility) |
| Cross-household access attempt | `access_denied_cross_household` | User tries to access another household's data |
| Role-based access denial | `access_denied_forbidden` | Readonly user tries to modify data |
| Emergency mode activation | `emergency_activate` | Panic button pressed |
| Emergency mode deactivation | `emergency_deactivate` | Emergency resolved |
| User role changed | `role_changed` | Admin changes someone's role |
| User removed from household | `user_removed` | Admin removes a caregiver |
| Encryption key rotation started | `key_rotation_started` | Admin initiates key rotation |
| Data export | `data_exported` | Admin exports household data |
| Bulk deletion | `bulk_delete` | Admin deletes a care recipient (cascade) |

```go
// In the authorization middleware or service layer
func (s *MedicationService) GetByID(ctx context.Context, householdID, medID string) (*Medication, error) {
    row, err := s.db.GetMedication(ctx, dbgen.GetMedicationParams{
        ID:          medID,
        HouseholdID: householdID,
    })
    if err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            // Check if the medication exists in a DIFFERENT household
            // to distinguish "doesn't exist" from "not your data"
            exists, _ := s.db.MedicationExistsGlobal(ctx, medID)
            if exists {
                // This is a cross-household access attempt — audit it
                user := auth.GetUser(ctx)
                AuditSecurityEvent(ctx, s.auditDB, AuditEvent{
                    HouseholdID: householdID,
                    UserID:      &user.ID,
                    Action:      "access_denied_cross_household",
                    EntityType:  "medication",
                    EntityID:    &medID,
                    IPAddress:   user.IPAddress,
                })
            }
            // Always return NotFound (don't reveal whether the entity exists in another household)
            return nil, apperror.NotFound("medication", medID)
        }
        return nil, apperror.Internal("Failed to load medication", err)
    }

    return s.rowToMedication(row)
}
```

**Important:** The response to the user is always `NotFound` regardless of whether the entity exists in another household. Returning `Forbidden` would confirm the entity exists, which is an information leak.

---

### Error Handling in Background Goroutines

Background goroutines (scheduler, overdue checker, housekeeping) cannot return errors to a user. They must log errors and continue operating.

```go
// Pattern for background workers
func (w *Worker) run(ctx context.Context) {
    for {
        select {
        case <-w.ticker.C:
            if err := w.doWork(ctx); err != nil {
                slog.Error("Background worker failed",
                    "worker", w.name,
                    "error", err,
                )
                // Store last error for admin dashboard visibility
                w.db.SetConfig(ctx, w.name+"_last_error", err.Error())
                // Do NOT crash — next tick will retry
                continue
            }
            // Clear error on success
            w.db.SetConfig(ctx, w.name+"_last_error", "")

        case <-ctx.Done():
            return
        }
    }
}
```

**Critical rule: Background goroutines never panic.** A panic in a goroutine crashes the entire process, which means the entire app goes down. All background work must be wrapped in recovery with automatic restart:

```go
// safeGo runs a function in a goroutine with panic recovery and auto-restart.
// If the goroutine panics, it logs the panic and restarts after a backoff delay.
// Respects context cancellation — won't restart if the context is done.
func safeGo(ctx context.Context, name string, fn func(context.Context)) {
    go func() {
        backoff := 1 * time.Second
        maxBackoff := 5 * time.Minute

        for {
            func() {
                defer func() {
                    if r := recover(); r != nil {
                        slog.Error("Goroutine panicked",
                            "name", name,
                            "panic", r,
                            "stack", string(debug.Stack()),
                            "restart_in", backoff.String(),
                        )
                    }
                }()
                fn(ctx)
            }()

            // If context is cancelled, don't restart
            if ctx.Err() != nil {
                slog.Info("Goroutine stopped (context cancelled)", "name", name)
                return
            }

            // Goroutine exited or panicked — wait and restart
            slog.Warn("Goroutine restarting", "name", name, "backoff", backoff.String())
            select {
            case <-time.After(backoff):
                // Exponential backoff, capped at maxBackoff
                backoff = min(backoff*2, maxBackoff)
            case <-ctx.Done():
                return
            }
        }
    }()
}

// Usage in startup
safeGo(ctx, "scheduler", func(ctx context.Context) {
    StartScheduler(ctx, generator, 30*time.Minute)
})
safeGo(ctx, "overdue-checker", func(ctx context.Context) {
    StartOverdueChecker(ctx, checker, 5*time.Minute)
})
safeGo(ctx, "housekeeping", func(ctx context.Context) {
    StartHousekeepingWorker(ctx, db, 1*time.Hour)
})
```

The exponential backoff (1s → 2s → 4s → ... → 5min max) prevents a crash loop from consuming all resources while still recovering quickly from transient panics.

---

### Encryption Error Handling

Encryption/decryption failures are a special category. If decryption fails, it usually means either:
1. The encryption key is wrong (app started with wrong `ENCRYPTION_SECRET`)
2. The data is corrupted
3. Key rotation was interrupted partway through

These are not user errors — they're system failures that affect the entire application.

```go
func (s *CareRecipientService) rowToRecipient(row dbgen.CareRecipient) (*CareRecipient, error) {
    name, err := s.enc.Decrypt(row.NameEnc)
    if err != nil {
        // This is serious — log full details
        slog.Error("Decryption failed for care recipient name",
            "care_recipient_id", row.ID,
            "household_id", row.HouseholdID,
            "error", err,
        )
        return nil, apperror.Internal(
            "Unable to load this record. This may indicate a configuration issue. Please contact your administrator.",
            err,
        )
    }

    // ... decrypt other fields ...
}
```

**On startup, the app should perform a decryption health check:**

```go
func verifyEncryptionKey(db *sql.DB, enc *Encryptor) error {
    // Try to decrypt a known test value stored during setup
    testValue, err := db.GetConfig(ctx, "encryption_test_value")
    if err != nil {
        return fmt.Errorf("no encryption test value found — is this a fresh install?")
    }

    decrypted, err := enc.Decrypt(testValue)
    if err != nil {
        return fmt.Errorf("ENCRYPTION_SECRET appears to be incorrect — cannot decrypt test value: %w", err)
    }

    if decrypted != "caregiver-hub-encryption-test" {
        return fmt.Errorf("decryption produced unexpected value — encryption key mismatch")
    }

    return nil
}
```

This prevents the app from starting with the wrong key, which would result in garbage data displayed to users.

---

## Part 2: Testing Strategy

### Testing Philosophy

This app manages medication schedules and emergency information. A bug in the medication administration logic could result in a missed or double dose. A bug in the emergency profile could show wrong allergy information to paramedics. Testing is not optional.

**Testing priorities (in order):**
1. Medication administration logic (highest stakes)
2. Instance generation system (foundational infrastructure)
3. Emergency mode and emergency profiles
4. Authentication and authorization (security)
5. Encryption/decryption round-trips
6. Everything else (CRUD, rendering, etc.)

---

### Test Categories

| Category | What it tests | Database | Speed |
|---|---|---|---|
| Unit tests | Pure logic: recurrence rules, timezone conversion, error types, validation | None | Very fast |
| Service tests | Business logic: CRUD, authorization, workflows | In-memory SQLite | Fast |
| Handler tests | HTTP layer: request parsing, HTMX response, status codes | In-memory SQLite | Fast |
| Integration tests | Cross-feature: instance generation, notifications, reconciliation | In-memory SQLite | Medium |
| Encryption tests | Round-trip encrypt/decrypt, key rotation, key mismatch detection | In-memory SQLite | Fast |

**No external test dependencies.** Tests use in-memory SQLite (`:memory:`), not Docker containers, not separate databases, not external services. Tests should run with `go test ./...` and nothing else.

---

### Test Database Setup

Every test that needs a database gets a fresh in-memory SQLite instance with migrations applied:

```go
// internal/testutil/db.go

package testutil

import (
    "database/sql"
    "embed"
    "testing"

    "github.com/pressly/goose/v3"
    _ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// NewTestDB creates a fresh in-memory SQLite database with all migrations applied.
// The database is automatically closed when the test completes.
func NewTestDB(t *testing.T) *sql.DB {
    t.Helper()

    db, err := sql.Open("sqlite", ":memory:?_journal_mode=WAL&_foreign_keys=ON")
    if err != nil {
        t.Fatalf("Failed to open test database: %v", err)
    }

    goose.SetBaseFS(migrations)
    if err := goose.Up(db, "migrations"); err != nil {
        t.Fatalf("Failed to run migrations: %v", err)
    }

    t.Cleanup(func() { db.Close() })

    return db
}

// NewTestEncryptor creates an encryptor with a fixed test key.
// ONLY for tests — never use a fixed key in production.
func NewTestEncryptor(t *testing.T) *crypto.Encryptor {
    t.Helper()
    // Fixed 32-byte key for deterministic test results
    key := []byte("test-key-00000000000000000000000")
    enc, err := crypto.NewEncryptor(key)
    if err != nil {
        t.Fatalf("Failed to create test encryptor: %v", err)
    }
    return enc
}
```

---

### Test Data Factories

Creating test data should be concise and readable. Use factory functions that provide sensible defaults and allow overrides:

```go
// internal/testutil/factories.go

package testutil

import (
    "caregiver-hub/internal/db/dbgen"
    "time"
)

type HouseholdOption func(*dbgen.CreateHouseholdParams)

func WithHouseholdName(name string) HouseholdOption {
    return func(p *dbgen.CreateHouseholdParams) { p.NameEnc = name }
}

func CreateTestHousehold(t *testing.T, db *sql.DB, enc *crypto.Encryptor, opts ...HouseholdOption) dbgen.Household {
    t.Helper()

    params := dbgen.CreateHouseholdParams{
        ID:              ulid.Make().String(),
        NameEnc:         mustEncrypt(t, enc, "Test Household"),
        EncryptionSalt:  "test-salt",
        OnboardingProgress: "{}",
        Settings:        "{}",
    }

    for _, opt := range opts {
        opt(&params)
    }

    q := dbgen.New(db)
    household, err := q.CreateHousehold(context.Background(), params)
    if err != nil {
        t.Fatalf("Failed to create test household: %v", err)
    }
    return household
}

type UserOption func(*dbgen.CreateUserParams)

func WithRole(role string) UserOption {
    return func(p *dbgen.CreateUserParams) { p.Role = role }
}

func WithTimezone(tz string) UserOption {
    return func(p *dbgen.CreateUserParams) { p.Timezone = tz }
}

func CreateTestUser(t *testing.T, db *sql.DB, enc *crypto.Encryptor, householdID string, opts ...UserOption) dbgen.User {
    t.Helper()

    id := ulid.Make().String()
    params := dbgen.CreateUserParams{
        ID:              id,
        HouseholdID:     householdID,
        EmailEnc:        mustEncrypt(t, enc, id+"@test.com"),
        EmailHash:       hmacHash(id + "@test.com"),
        PasswordHash:    "$2a$10$testhashedpassword",
        DisplayNameEnc:  mustEncrypt(t, enc, "Test User"),
        Role:            "member",
        AuthProvider:    "local",
        Timezone:        "America/New_York",
    }

    for _, opt := range opts {
        opt(&params)
    }

    q := dbgen.New(db)
    user, err := q.CreateUser(context.Background(), params)
    if err != nil {
        t.Fatalf("Failed to create test user: %v", err)
    }
    return user
}

type CareRecipientOption func(*dbgen.CreateCareRecipientParams)

func WithRecipientTimezone(tz string) CareRecipientOption {
    return func(p *dbgen.CreateCareRecipientParams) { p.Timezone = tz }
}

func CreateTestCareRecipient(t *testing.T, db *sql.DB, enc *crypto.Encryptor, householdID string, opts ...CareRecipientOption) dbgen.CareRecipient {
    t.Helper()

    params := dbgen.CreateCareRecipientParams{
        ID:          ulid.Make().String(),
        HouseholdID: householdID,
        NameEnc:     mustEncrypt(t, enc, "Test Care Recipient"),
        Timezone:    "America/New_York",
    }

    for _, opt := range opts {
        opt(&params)
    }

    q := dbgen.New(db)
    recipient, err := q.CreateCareRecipient(context.Background(), params)
    if err != nil {
        t.Fatalf("Failed to create test care recipient: %v", err)
    }
    return recipient
}

type MedicationOption func(*dbgen.CreateMedicationParams)

func WithPRN() MedicationOption {
    return func(p *dbgen.CreateMedicationParams) { p.IsPRN = 1 }
}

func CreateTestMedication(t *testing.T, db *sql.DB, enc *crypto.Encryptor, householdID, recipientID string, opts ...MedicationOption) dbgen.Medication {
    t.Helper()

    params := dbgen.CreateMedicationParams{
        ID:              ulid.Make().String(),
        HouseholdID:     householdID,
        CareRecipientID: recipientID,
        NameEnc:         mustEncrypt(t, enc, "Test Medication"),
        DosageEnc:       mustEncrypt(t, enc, "10mg"),
        IsPRN:           0,
    }

    for _, opt := range opts {
        opt(&params)
    }

    q := dbgen.New(db)
    med, err := q.CreateMedication(context.Background(), params)
    if err != nil {
        t.Fatalf("Failed to create test medication: %v", err)
    }
    return med
}

func mustEncrypt(t *testing.T, enc *crypto.Encryptor, plaintext string) string {
    t.Helper()
    encrypted, err := enc.Encrypt(plaintext)
    if err != nil {
        t.Fatalf("Failed to encrypt test data: %v", err)
    }
    return encrypted
}
```

**Usage in tests:**

```go
func TestMedicationService_Create(t *testing.T) {
    db := testutil.NewTestDB(t)
    enc := testutil.NewTestEncryptor(t)

    household := testutil.CreateTestHousehold(t, db, enc)
    user := testutil.CreateTestUser(t, db, enc, household.ID, testutil.WithRole("admin"))
    recipient := testutil.CreateTestCareRecipient(t, db, enc, household.ID)

    svc := medications.NewService(dbgen.New(db), enc)

    med, err := svc.Create(ctx, medications.CreateMedicationInput{
        HouseholdID:     household.ID,
        CareRecipientID: recipient.ID,
        Name:            "Lisinopril",
        Dosage:          "10mg",
        CreatedBy:       user.ID,
    })

    require.NoError(t, err)
    assert.Equal(t, "Lisinopril", med.Name) // Decrypted in the returned object
    assert.Equal(t, "10mg", med.Dosage)
}
```

---

### Table-Driven Tests (Go Convention)

All tests that check multiple scenarios use Go's table-driven pattern:

```go
func TestMedicationService_Create_Validation(t *testing.T) {
    db := testutil.NewTestDB(t)
    enc := testutil.NewTestEncryptor(t)

    household := testutil.CreateTestHousehold(t, db, enc)
    user := testutil.CreateTestUser(t, db, enc, household.ID)
    recipient := testutil.CreateTestCareRecipient(t, db, enc, household.ID)

    svc := medications.NewService(dbgen.New(db), enc)

    tests := []struct {
        name        string
        input       medications.CreateMedicationInput
        wantErrType apperror.Type
        wantField   string
    }{
        {
            name: "missing name",
            input: medications.CreateMedicationInput{
                HouseholdID:     household.ID,
                CareRecipientID: recipient.ID,
                Name:            "",
                Dosage:          "10mg",
                CreatedBy:       user.ID,
            },
            wantErrType: apperror.TypeValidation,
            wantField:   "name",
        },
        {
            name: "missing dosage",
            input: medications.CreateMedicationInput{
                HouseholdID:     household.ID,
                CareRecipientID: recipient.ID,
                Name:            "Lisinopril",
                Dosage:          "",
                CreatedBy:       user.ID,
            },
            wantErrType: apperror.TypeValidation,
            wantField:   "dosage",
        },
        {
            name: "invalid care recipient",
            input: medications.CreateMedicationInput{
                HouseholdID:     household.ID,
                CareRecipientID: "nonexistent-id",
                Name:            "Lisinopril",
                Dosage:          "10mg",
                CreatedBy:       user.ID,
            },
            wantErrType: apperror.TypeNotFound,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            _, err := svc.Create(context.Background(), tt.input)
            require.Error(t, err)

            var appErr *apperror.Error
            require.ErrorAs(t, err, &appErr)
            assert.Equal(t, tt.wantErrType, appErr.Type)
            if tt.wantField != "" {
                assert.Equal(t, tt.wantField, appErr.Field)
            }
        })
    }
}
```

---

### Handler Tests

Handler tests verify HTTP-level behavior: correct status codes, HTMX response headers, and rendered HTML.

```go
func TestMedicationHandler_Create_Success(t *testing.T) {
    db := testutil.NewTestDB(t)
    enc := testutil.NewTestEncryptor(t)

    household := testutil.CreateTestHousehold(t, db, enc)
    user := testutil.CreateTestUser(t, db, enc, household.ID)
    recipient := testutil.CreateTestCareRecipient(t, db, enc, household.ID)

    handler := medications.NewHandler(medications.NewService(dbgen.New(db), enc))

    // Build HTMX request
    form := url.Values{
        "name":   {"Lisinopril"},
        "dosage": {"10mg"},
    }
    req := httptest.NewRequest("POST",
        fmt.Sprintf("/care-recipients/%s/medications", recipient.ID),
        strings.NewReader(form.Encode()))
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
    req.Header.Set("HX-Request", "true")

    // Inject auth context
    ctx := auth.WithUser(req.Context(), &auth.User{
        ID:          user.ID,
        HouseholdID: household.ID,
        Role:        "admin",
    })
    req = req.WithContext(ctx)

    rec := httptest.NewRecorder()
    handler.HandleCreate(rec, req)

    // Verify response
    assert.Equal(t, http.StatusOK, rec.Code)
    assert.Contains(t, rec.Header().Get("HX-Trigger"), "clearErrors")
    assert.Contains(t, rec.Body.String(), "Lisinopril") // Rendered in the card
}

func TestMedicationHandler_Create_ValidationError(t *testing.T) {
    // ... similar setup ...

    form := url.Values{
        "name":   {""},  // Missing name
        "dosage": {"10mg"},
    }
    // ... build request ...

    rec := httptest.NewRecorder()
    handler.HandleCreate(rec, req)

    assert.Equal(t, http.StatusBadRequest, rec.Code)
    assert.Contains(t, rec.Header().Get("HX-Retarget"), "#error-name")
    assert.Contains(t, rec.Body.String(), "required")
}
```

---

### Authorization Tests

Every feature must be tested for proper role enforcement:

```go
func TestMedicationService_Create_Authorization(t *testing.T) {
    db := testutil.NewTestDB(t)
    enc := testutil.NewTestEncryptor(t)

    household := testutil.CreateTestHousehold(t, db, enc)
    recipient := testutil.CreateTestCareRecipient(t, db, enc, household.ID)

    // Create users with different roles
    admin := testutil.CreateTestUser(t, db, enc, household.ID, testutil.WithRole("admin"))
    member := testutil.CreateTestUser(t, db, enc, household.ID, testutil.WithRole("member"))
    readonly := testutil.CreateTestUser(t, db, enc, household.ID, testutil.WithRole("readonly"))

    // Create a user in a DIFFERENT household
    otherHousehold := testutil.CreateTestHousehold(t, db, enc, testutil.WithHouseholdName("Other"))
    outsider := testutil.CreateTestUser(t, db, enc, otherHousehold.ID)

    svc := medications.NewService(dbgen.New(db), enc)

    input := medications.CreateMedicationInput{
        CareRecipientID: recipient.ID,
        Name:            "Lisinopril",
        Dosage:          "10mg",
    }

    tests := []struct {
        name        string
        user        dbgen.User
        wantErr     bool
        wantErrType apperror.Type
    }{
        {"admin can create", admin, false, 0},
        {"member can create", member, false, 0},
        {"readonly cannot create", readonly, true, apperror.TypeForbidden},
        {"outsider cannot create", outsider, true, apperror.TypeForbidden},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            input.HouseholdID = tt.user.HouseholdID
            input.CreatedBy = tt.user.ID
            ctx := auth.WithUser(context.Background(), toAuthUser(tt.user))

            _, err := svc.Create(ctx, input)

            if tt.wantErr {
                require.Error(t, err)
                var appErr *apperror.Error
                require.ErrorAs(t, err, &appErr)
                assert.Equal(t, tt.wantErrType, appErr.Type)
            } else {
                require.NoError(t, err)
            }
        })
    }
}
```

**Critical: Always test cross-household access.** The outsider test above verifies that a user from household B cannot create medications for a care recipient in household A. This is the most important security test in the entire application.

---

### Encryption Round-Trip Tests

```go
func TestEncryption_RoundTrip(t *testing.T) {
    enc := testutil.NewTestEncryptor(t)

    tests := []struct {
        name      string
        plaintext string
    }{
        {"simple text", "Hello World"},
        {"empty string", ""},
        {"unicode", "こんにちは世界"},
        {"long text", strings.Repeat("a", 10000)},
        {"special chars", "O'Brien's \"medication\" — 10mg/5ml"},
        {"medical data", "Allergies: Penicillin (anaphylaxis), Sulfa (rash)"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            encrypted, err := enc.Encrypt(tt.plaintext)
            require.NoError(t, err)

            // Encrypted value should not equal plaintext
            assert.NotEqual(t, tt.plaintext, encrypted)

            decrypted, err := enc.Decrypt(encrypted)
            require.NoError(t, err)
            assert.Equal(t, tt.plaintext, decrypted)
        })
    }
}

func TestEncryption_DifferentCiphertexts(t *testing.T) {
    enc := testutil.NewTestEncryptor(t)

    // Same plaintext encrypted twice should produce different ciphertext
    // (because of random nonce)
    enc1, _ := enc.Encrypt("test")
    enc2, _ := enc.Encrypt("test")
    assert.NotEqual(t, enc1, enc2)

    // But both decrypt to the same value
    dec1, _ := enc.Decrypt(enc1)
    dec2, _ := enc.Decrypt(enc2)
    assert.Equal(t, dec1, dec2)
    assert.Equal(t, "test", dec1)
}

func TestEncryption_WrongKey(t *testing.T) {
    enc1 := testutil.NewTestEncryptor(t)

    key2 := []byte("different-key-000000000000000000")
    enc2, _ := crypto.NewEncryptor(key2)

    encrypted, _ := enc1.Encrypt("sensitive data")

    // Decrypting with wrong key should fail
    _, err := enc2.Decrypt(encrypted)
    assert.Error(t, err)
}
```

---

### Household Isolation Tests

This test pattern should be applied to every feature to verify data isolation:

```go
func TestHouseholdIsolation_Medications(t *testing.T) {
    db := testutil.NewTestDB(t)
    enc := testutil.NewTestEncryptor(t)

    // Setup two completely separate households
    householdA := testutil.CreateTestHousehold(t, db, enc, testutil.WithHouseholdName("Family A"))
    userA := testutil.CreateTestUser(t, db, enc, householdA.ID)
    recipientA := testutil.CreateTestCareRecipient(t, db, enc, householdA.ID)

    householdB := testutil.CreateTestHousehold(t, db, enc, testutil.WithHouseholdName("Family B"))
    userB := testutil.CreateTestUser(t, db, enc, householdB.ID)
    recipientB := testutil.CreateTestCareRecipient(t, db, enc, householdB.ID)

    svc := medications.NewService(dbgen.New(db), enc)

    // Create medications in both households
    medA, _ := svc.Create(authCtx(userA), medications.CreateMedicationInput{
        HouseholdID: householdA.ID, CareRecipientID: recipientA.ID,
        Name: "Household A Med", Dosage: "5mg", CreatedBy: userA.ID,
    })
    medB, _ := svc.Create(authCtx(userB), medications.CreateMedicationInput{
        HouseholdID: householdB.ID, CareRecipientID: recipientB.ID,
        Name: "Household B Med", Dosage: "10mg", CreatedBy: userB.ID,
    })

    // User A should only see their household's medications
    medsForA, _ := svc.ListByRecipient(authCtx(userA), householdA.ID, recipientA.ID)
    assert.Len(t, medsForA, 1)
    assert.Equal(t, "Household A Med", medsForA[0].Name)

    // User A should NOT be able to access Household B's medication
    _, err := svc.GetByID(authCtx(userA), householdA.ID, medB.ID)
    require.Error(t, err)
    var appErr *apperror.Error
    require.ErrorAs(t, err, &appErr)
    assert.Equal(t, apperror.TypeNotFound, appErr.Type)

    // User B should NOT be able to access Household A's medication
    _, err = svc.GetByID(authCtx(userB), householdB.ID, medA.ID)
    require.Error(t, err)
    require.ErrorAs(t, err, &appErr)
    assert.Equal(t, apperror.TypeNotFound, appErr.Type)
}
```

**This test pattern is mandatory for every feature.** Cross-household data leakage is the worst possible bug in a multi-tenant app handling medical data.

---

### Testing HTMX Interactions

For testing that HTMX attributes and response headers are correct:

```go
func TestTaskCompletion_HTMXFlow(t *testing.T) {
    db := testutil.NewTestDB(t)
    enc := testutil.NewTestEncryptor(t)

    // ... setup household, user, recipient, task ...

    handler := tasks.NewHandler(tasks.NewService(dbgen.New(db), enc))

    // Simulate HTMX button click: "Mark Done"
    req := httptest.NewRequest("POST",
        fmt.Sprintf("/tasks/%s/complete", task.ID), nil)
    req.Header.Set("HX-Request", "true")
    req.Header.Set("HX-Target", "#task-"+task.ID)
    req = req.WithContext(auth.WithUser(req.Context(), toAuthUser(user)))

    rec := httptest.NewRecorder()
    handler.HandleComplete(rec, req)

    // Verify: successful response
    assert.Equal(t, http.StatusOK, rec.Code)

    // Verify: response contains the updated task row HTML
    body := rec.Body.String()
    assert.Contains(t, body, "line-through")     // Completed task has strikethrough
    assert.Contains(t, body, task.Title)          // Task title is present

    // Verify: task is actually completed in the database
    updated, _ := taskSvc.GetByID(authCtx(user), household.ID, task.ID)
    assert.True(t, updated.Completed)
    assert.NotNil(t, updated.CompletedAt)
    assert.Equal(t, user.ID, *updated.CompletedBy)
}
```

---

### Testing the Instance Generation System

The instance generation document already includes detailed test examples. Here's the test organization:

```
internal/generator/
├── generator.go              # Core generation engine
├── generator_test.go         # Unit + integration tests
├── recurrence.go             # Recurrence rule matching
├── recurrence_test.go        # Recurrence unit tests
├── reconciler.go             # Template modification reconciliation
├── reconciler_test.go        # Reconciliation tests
├── overdue.go                # Overdue checker
├── overdue_test.go           # Overdue detection tests
└── housekeeping.go           # Housekeeping worker
```

**Minimum test coverage for the generator:**
- Idempotency (run twice, same result)
- DST transitions (spring forward, fall back)
- Timezone change reconciliation
- Template modification reconciliation
- Template deactivation cleanup
- Template reactivation
- Late-day instance creation (past-time grace period)
- Catch-up after downtime
- PRN medication skipping
- Medication discontinuation
- Overnight shift handling
- Concurrent generation safety
- Per-recipient timezone windowing
- All 31 edge cases listed in the instance generation document

---

### Running Tests

```bash
# Run all tests
go test ./...

# Run tests with verbose output
go test -v ./...

# Run tests for a specific package
go test -v ./internal/medications/...

# Run a specific test
go test -v -run TestMedicationService_Create ./internal/medications/

# Run tests with race detector (catches concurrency bugs)
go test -race ./...

# Run tests with coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```

**CI pipeline should run:**
```bash
go test -race -coverprofile=coverage.out ./...
```

The `-race` flag is critical — it detects data races in concurrent code (like the generator + overdue checker goroutines). Run this in CI on every commit.

---

### Test Naming Conventions

```
TestServiceName_MethodName                    # Happy path
TestServiceName_MethodName_ValidationError    # Specific error case
TestServiceName_MethodName_Unauthorized       # Auth failure
TestServiceName_MethodName_CrossHousehold     # Isolation test
TestHandlerName_MethodName_HTMX              # HTMX-specific behavior
TestHandlerName_MethodName_FullPage          # Non-HTMX behavior
TestGenerator_Idempotency                    # Infrastructure tests
TestEncryption_RoundTrip                     # Crypto tests
```

---

### What NOT to Test

- **sqlc-generated code** — it's generated and tested by the sqlc project
- **HTMX library behavior** — trust that HTMX works; test that your attributes and headers are correct
- **DaisyUI/Tailwind rendering** — CSS is not testable in Go; visual testing is a post-1.0 concern
- **Third-party notification providers** — mock them; don't call real ntfy/Pushover in tests
- **Templ rendering internals** — test that the right data reaches the template; trust that templ renders correctly

---

### Testing the Error Response Helper

The `RespondError` function is critical infrastructure — if it breaks, every error in the app renders wrong. Test it directly:

```go
func TestRespondError_ValidationHTMX(t *testing.T) {
    err := apperror.Validation("name", "Name is required")

    req := httptest.NewRequest("GET", "/test", nil)
    req.Header.Set("HX-Request", "true")
    rec := httptest.NewRecorder()

    web.RespondError(rec, req, err)

    assert.Equal(t, http.StatusBadRequest, rec.Code)
    assert.Equal(t, "#error-name", rec.Header().Get("HX-Retarget"))
    assert.Equal(t, "innerHTML", rec.Header().Get("HX-Reswap"))
    assert.Contains(t, rec.Body.String(), "Name is required")
}

func TestRespondError_InternalHTMX_IncludesRequestID(t *testing.T) {
    err := apperror.Internal("Database error", fmt.Errorf("connection refused"))

    req := httptest.NewRequest("GET", "/test", nil)
    req.Header.Set("HX-Request", "true")
    // Inject request ID via middleware context
    ctx := context.WithValue(req.Context(), middleware.RequestIDKey, "abc123")
    req = req.WithContext(ctx)
    rec := httptest.NewRecorder()

    web.RespondError(rec, req, err)

    assert.Equal(t, http.StatusInternalServerError, rec.Code)
    assert.Contains(t, rec.Body.String(), "abc123")          // Request ID shown to user
    assert.NotContains(t, rec.Body.String(), "connection refused") // Internal detail NOT shown
}

func TestRespondError_UnauthorizedHTMX_Redirects(t *testing.T) {
    err := apperror.Unauthorized("Please log in")

    req := httptest.NewRequest("GET", "/test", nil)
    req.Header.Set("HX-Request", "true")
    rec := httptest.NewRecorder()

    web.RespondError(rec, req, err)

    assert.Equal(t, "/login", rec.Header().Get("HX-Redirect"))
}

func TestRespondError_RateLimited_IncludesRetryAfter(t *testing.T) {
    err := apperror.RateLimited("Too many attempts. Please try again in 5 minutes.", 5*time.Minute)

    req := httptest.NewRequest("POST", "/login", nil)
    req.Header.Set("HX-Request", "true")
    rec := httptest.NewRecorder()

    web.RespondError(rec, req, err)

    assert.Equal(t, http.StatusTooManyRequests, rec.Code)
    assert.Equal(t, "300", rec.Header().Get("Retry-After"))
    assert.Contains(t, rec.Body.String(), "5 minutes")
}

func TestRespondError_FullPage_NotFound(t *testing.T) {
    err := apperror.NotFound("medication", "some-id")

    req := httptest.NewRequest("GET", "/test", nil)
    // No HX-Request header — full page request
    rec := httptest.NewRecorder()

    web.RespondError(rec, req, err)

    assert.Equal(t, http.StatusNotFound, rec.Code)
    // Should render the full not-found page, not just a fragment
    assert.Contains(t, rec.Body.String(), "<html") // Full page
}

func TestRespondError_UnknownErrorType(t *testing.T) {
    // Raw error (not apperror.Error) should be treated as internal
    err := fmt.Errorf("some raw error")

    req := httptest.NewRequest("GET", "/test", nil)
    req.Header.Set("HX-Request", "true")
    rec := httptest.NewRecorder()

    web.RespondError(rec, req, err)

    assert.Equal(t, http.StatusInternalServerError, rec.Code)
    assert.Contains(t, rec.Body.String(), "Something went wrong")
    assert.NotContains(t, rec.Body.String(), "some raw error") // Raw error NOT exposed
}
```

---

### Testing Middleware

Middleware affects every route — bugs here are catastrophic. Test each middleware independently:

```go
// Test: Auth middleware rejects unauthenticated requests
func TestAuthMiddleware_NoSession(t *testing.T) {
    handler := middleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK) // Should never reach here
    }))

    req := httptest.NewRequest("GET", "/dashboard", nil)
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)

    assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// Test: Auth middleware passes valid session through
func TestAuthMiddleware_ValidSession(t *testing.T) {
    db := testutil.NewTestDB(t)
    enc := testutil.NewTestEncryptor(t)
    household := testutil.CreateTestHousehold(t, db, enc)
    user := testutil.CreateTestUser(t, db, enc, household.ID)
    session := testutil.CreateTestSession(t, db, user.ID, household.ID)

    var capturedUser *auth.User
    handler := middleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        capturedUser = auth.GetUser(r.Context())
        w.WriteHeader(http.StatusOK)
    }))

    req := httptest.NewRequest("GET", "/dashboard", nil)
    req.AddCookie(&http.Cookie{Name: "session", Value: session.ID})
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)

    assert.Equal(t, http.StatusOK, rec.Code)
    assert.Equal(t, user.ID, capturedUser.ID)
    assert.Equal(t, household.ID, capturedUser.HouseholdID)
}

// Test: Household scoping middleware prevents cross-household access
func TestHouseholdScopeMiddleware_WrongHousehold(t *testing.T) {
    handler := middleware.RequireHouseholdAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK) // Should never reach here
    }))

    req := httptest.NewRequest("GET", "/households/other-household-id/recipients", nil)
    // User is authenticated but for a different household
    ctx := auth.WithUser(req.Context(), &auth.User{
        ID:          "user-1",
        HouseholdID: "my-household-id",
    })
    req = req.WithContext(ctx)
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)

    // Should return 404 (not 403, to avoid revealing the household exists)
    assert.Equal(t, http.StatusNotFound, rec.Code)
}

// Test: Role middleware blocks insufficient permissions
func TestRoleMiddleware_ReadonlyCannotWrite(t *testing.T) {
    handler := middleware.RequireRole("member", "admin")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK) // Should never reach here
    }))

    req := httptest.NewRequest("POST", "/medications", nil)
    ctx := auth.WithUser(req.Context(), &auth.User{
        ID:          "user-1",
        HouseholdID: "household-1",
        Role:        "readonly",
    })
    req = req.WithContext(ctx)
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)

    assert.Equal(t, http.StatusForbidden, rec.Code)
}

// Test: Request ID middleware adds ID to context and response header
func TestRequestIDMiddleware(t *testing.T) {
    var capturedID string
    handler := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        capturedID = middleware.GetRequestID(r.Context())
        w.WriteHeader(http.StatusOK)
    }))

    req := httptest.NewRequest("GET", "/test", nil)
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)

    assert.NotEmpty(t, capturedID)
    assert.Equal(t, capturedID, rec.Header().Get("X-Request-ID"))
}

// Test: Rate limit middleware blocks after threshold
func TestRateLimitMiddleware_BlocksAfterThreshold(t *testing.T) {
    handler := middleware.RateLimit(3, 1*time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    }))

    for i := 0; i < 3; i++ {
        req := httptest.NewRequest("POST", "/login", nil)
        req.RemoteAddr = "192.168.1.1:12345"
        rec := httptest.NewRecorder()
        handler.ServeHTTP(rec, req)
        assert.Equal(t, http.StatusOK, rec.Code)
    }

    // 4th request should be rate limited
    req := httptest.NewRequest("POST", "/login", nil)
    req.RemoteAddr = "192.168.1.1:12345"
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)
    assert.Equal(t, http.StatusTooManyRequests, rec.Code)
    assert.NotEmpty(t, rec.Header().Get("Retry-After"))
}
```

---

### Graceful Shutdown

The application manages multiple goroutines, database connections, and in-flight HTTP requests. When the process receives SIGTERM or SIGINT, it must shut down cleanly.

```go
func main() {
    // Create a root context that cancels on OS signal
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    // ... database setup, migrations, catch-up generation ...

    // Start background goroutines (all respect ctx cancellation)
    safeGo(ctx, "scheduler", func(ctx context.Context) {
        StartScheduler(ctx, generator, 30*time.Minute)
    })
    safeGo(ctx, "overdue-checker", func(ctx context.Context) {
        StartOverdueChecker(ctx, checker, 5*time.Minute)
    })
    safeGo(ctx, "housekeeping", func(ctx context.Context) {
        StartHousekeepingWorker(ctx, db, 1*time.Hour)
    })

    // Start HTTP server
    server := &http.Server{
        Addr:    ":8080",
        Handler: router,
    }

    // Start serving in a goroutine
    go func() {
        slog.Info("Starting HTTP server", "addr", server.Addr)
        if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            slog.Error("HTTP server error", "error", err)
        }
    }()

    // Wait for shutdown signal
    <-ctx.Done()
    slog.Info("Shutdown signal received, gracefully stopping...")

    // Give in-flight requests up to 30 seconds to complete
    shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer shutdownCancel()

    // Shut down HTTP server (stops accepting new connections, waits for in-flight)
    if err := server.Shutdown(shutdownCtx); err != nil {
        slog.Error("HTTP server shutdown error", "error", err)
    }

    // Background goroutines are already stopping (ctx is cancelled)
    // Give them a moment to finish current work
    time.Sleep(2 * time.Second)

    // Close database connection
    if err := db.Close(); err != nil {
        slog.Error("Database close error", "error", err)
    }

    // Checkpoint WAL before exit for clean SQLite state
    db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")

    slog.Info("Shutdown complete")
}
```

**Shutdown order:**
1. Stop accepting new HTTP requests
2. Wait for in-flight requests to complete (up to 30 seconds)
3. Cancel context → background goroutines stop at their next tick
4. Brief pause for goroutines to finish current iteration
5. Checkpoint SQLite WAL for clean state
6. Close database connection

**Testing graceful shutdown:**

```go
func TestGracefulShutdown(t *testing.T) {
    db := testutil.NewTestDB(t)
    ctx, cancel := context.WithCancel(context.Background())

    // Track goroutine completion
    var schedulerStopped, checkerStopped atomic.Bool

    go func() {
        StartScheduler(ctx, generator, 100*time.Millisecond)
        schedulerStopped.Store(true)
    }()
    go func() {
        StartOverdueChecker(ctx, checker, 100*time.Millisecond)
        checkerStopped.Store(true)
    }()

    // Let them run for a bit
    time.Sleep(300 * time.Millisecond)

    // Signal shutdown
    cancel()

    // Give goroutines time to stop
    time.Sleep(200 * time.Millisecond)

    assert.True(t, schedulerStopped.Load(), "Scheduler should have stopped")
    assert.True(t, checkerStopped.Load(), "Overdue checker should have stopped")
}
```

---

### Mocking External Dependencies

For notification providers and any future external integrations, use interfaces:

```go
// internal/notifications/sender.go

type Sender interface {
    Send(ctx context.Context, notification Notification) error
}

// Production implementation
type MultiChannelSender struct {
    email    EmailSender
    push     PushSender
    channels ChannelStore
}

// Test implementation
type MockSender struct {
    Sent []Notification
}

func (m *MockSender) Send(ctx context.Context, n Notification) error {
    m.Sent = append(m.Sent, n)
    return nil
}
```

```go
// In tests
mockNotifier := &notifications.MockSender{}
svc := medications.NewService(db, enc, medications.WithNotifier(mockNotifier))

// ... do something that triggers a notification ...

assert.Len(t, mockNotifier.Sent, 1)
assert.Equal(t, "medication_overdue", mockNotifier.Sent[0].Type)
```

---

### Pre-Commit Checklist

Before committing any feature, verify:

1. `go test ./...` passes
2. `go test -race ./...` passes (no data races)
3. `go vet ./...` clean
4. New feature has service-layer tests covering:
   - Happy path
   - Validation errors
   - Authorization (all four roles)
   - Household isolation
5. If the feature has HTMX interactions, at least one handler test verifying correct response headers
6. If the feature touches encrypted data, encryption round-trip test
7. If the feature creates/modifies recurring instances, generator integration test
