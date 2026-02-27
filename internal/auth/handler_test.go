package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/shelterkin/shelterkin/internal/ulid"
)

const handlerTestSecret = "test-session-secret-that-is-32ch"

func noopCSRFToken(_ context.Context) string { return "test-csrf-token" }

func setupHandler(t *testing.T) (*Handler, *Service) {
	t.Helper()
	svc, _ := setupService(t)
	h := NewHandler(svc, handlerTestSecret, false, noopCSRFToken)
	return h, svc
}

func postForm(target string, values url.Values) *http.Request {
	req := httptest.NewRequest("POST", target, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// --- GET /login ---

func TestHandleLoginPage_RendersForm(t *testing.T) {
	h, _ := setupHandler(t)
	req := httptest.NewRequest("GET", "/login", nil)
	rec := httptest.NewRecorder()

	h.HandleLoginPage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Sign in to Shelterkin") {
		t.Fatal("expected login form heading in response body")
	}
}

func TestHandleLoginPage_RedirectsAuthenticatedUser(t *testing.T) {
	h, _ := setupHandler(t)
	req := httptest.NewRequest("GET", "/login", nil)
	ctx := WithUser(req.Context(), &AuthUser{ID: "user-1", HouseholdID: "hh-1", Role: "admin"})
	rec := httptest.NewRecorder()

	h.HandleLoginPage(rec, req.WithContext(ctx))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Fatalf("expected redirect to /, got %q", loc)
	}
}

// --- POST /login ---

func TestHandleLogin_Success_FullPage(t *testing.T) {
	h, svc := setupHandler(t)
	registerFirstUser(t, svc)

	form := url.Values{"email": {"admin@test.com"}, "password": {"password123"}}
	req := postForm("/login", form)
	rec := httptest.NewRecorder()

	h.HandleLogin(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Fatalf("expected redirect to /, got %q", loc)
	}
	if !hasSessionCookie(rec) {
		t.Fatal("expected session cookie to be set")
	}
}

func TestHandleLogin_Success_HTMX(t *testing.T) {
	h, svc := setupHandler(t)
	registerFirstUser(t, svc)

	form := url.Values{"email": {"admin@test.com"}, "password": {"password123"}}
	req := postForm("/login", form)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	h.HandleLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if hxRedirect := rec.Header().Get("HX-Redirect"); hxRedirect != "/" {
		t.Fatalf("expected HX-Redirect /, got %q", hxRedirect)
	}
	if !hasSessionCookie(rec) {
		t.Fatal("expected session cookie to be set")
	}
}

func TestHandleLogin_InvalidCredentials(t *testing.T) {
	h, svc := setupHandler(t)
	registerFirstUser(t, svc)

	form := url.Values{"email": {"admin@test.com"}, "password": {"wrongpassword"}}
	req := postForm("/login", form)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	h.HandleLogin(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Invalid email or password") {
		t.Fatal("expected error message in response body")
	}
}

func TestHandleLogin_ValidationError(t *testing.T) {
	h, _ := setupHandler(t)

	form := url.Values{"email": {""}, "password": {""}}
	req := postForm("/login", form)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	h.HandleLogin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleLogin_RateLimited(t *testing.T) {
	h, svc := setupHandler(t)
	registerFirstUser(t, svc)

	for i := 0; i < maxFailedLoginsByEmail; i++ {
		form := url.Values{"email": {"admin@test.com"}, "password": {"wrong"}}
		req := postForm("/login", form)
		rec := httptest.NewRecorder()
		h.HandleLogin(rec, req)
	}

	form := url.Values{"email": {"admin@test.com"}, "password": {"password123"}}
	req := postForm("/login", form)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	h.HandleLogin(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if retryAfter := rec.Header().Get("Retry-After"); retryAfter == "" {
		t.Fatal("expected Retry-After header")
	}
}

func TestHandleLogin_NonHTMX_ErrorRendersFullPage(t *testing.T) {
	h, _ := setupHandler(t)

	form := url.Values{"email": {""}, "password": {""}}
	req := postForm("/login", form)
	rec := httptest.NewRecorder()

	h.HandleLogin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	body := rec.Body.String()
	// full page should include the layout doctype
	if !strings.Contains(body, "<!doctype html>") {
		t.Fatal("expected full page layout in non-HTMX error response")
	}
}

// --- GET /register ---

func TestHandleRegisterPage_RendersForm(t *testing.T) {
	h, _ := setupHandler(t)
	req := httptest.NewRequest("GET", "/register", nil)
	rec := httptest.NewRecorder()

	h.HandleRegisterPage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Create your account") {
		t.Fatal("expected register form heading in response body")
	}
}

func TestHandleRegisterPage_WithInviteToken(t *testing.T) {
	h, _ := setupHandler(t)
	req := httptest.NewRequest("GET", "/register?token=abc123", nil)
	rec := httptest.NewRecorder()

	h.HandleRegisterPage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "abc123") {
		t.Fatal("expected invite token value in response body")
	}
	// with an invite token, household name field should not appear
	if strings.Contains(body, "Household name") {
		t.Fatal("expected household name field to be hidden with invite token")
	}
}

func TestHandleRegisterPage_WithoutInviteToken(t *testing.T) {
	h, _ := setupHandler(t)
	req := httptest.NewRequest("GET", "/register", nil)
	rec := httptest.NewRecorder()

	h.HandleRegisterPage(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "Household name") {
		t.Fatal("expected household name field without invite token")
	}
}

func TestHandleRegisterPage_RedirectsAuthenticatedUser(t *testing.T) {
	h, _ := setupHandler(t)
	req := httptest.NewRequest("GET", "/register", nil)
	ctx := WithUser(req.Context(), &AuthUser{ID: "user-1", HouseholdID: "hh-1", Role: "admin"})
	rec := httptest.NewRecorder()

	h.HandleRegisterPage(rec, req.WithContext(ctx))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", rec.Code)
	}
}

// --- POST /register ---

func TestHandleRegister_FirstUser_FullPage(t *testing.T) {
	h, _ := setupHandler(t)

	form := url.Values{
		"email":          {"new@test.com"},
		"password":       {"password123"},
		"display_name":   {"New User"},
		"household_name": {"My Household"},
	}
	req := postForm("/register", form)
	rec := httptest.NewRecorder()

	h.HandleRegister(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Fatalf("expected redirect to /, got %q", loc)
	}
	if !hasSessionCookie(rec) {
		t.Fatal("expected session cookie to be set")
	}
}

func TestHandleRegister_FirstUser_HTMX(t *testing.T) {
	h, _ := setupHandler(t)

	form := url.Values{
		"email":          {"new@test.com"},
		"password":       {"password123"},
		"display_name":   {"New User"},
		"household_name": {"My Household"},
	}
	req := postForm("/register", form)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	h.HandleRegister(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if hxRedirect := rec.Header().Get("HX-Redirect"); hxRedirect != "/" {
		t.Fatalf("expected HX-Redirect /, got %q", hxRedirect)
	}
}

func TestHandleRegister_ValidationErrors(t *testing.T) {
	h, _ := setupHandler(t)

	form := url.Values{
		"email":    {""},
		"password": {"short"},
	}
	req := postForm("/register", form)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	h.HandleRegister(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleRegister_DuplicateEmail(t *testing.T) {
	h, svc := setupHandler(t)
	registerFirstUser(t, svc)

	form := url.Values{
		"email":          {"admin@test.com"},
		"password":       {"password123"},
		"display_name":   {"Another User"},
		"household_name": {"Another Household"},
	}
	req := postForm("/register", form)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	h.HandleRegister(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
}

func TestHandleRegister_PreservesInputOnError(t *testing.T) {
	h, _ := setupHandler(t)

	form := url.Values{
		"email":          {"test@example.com"},
		"password":       {"short"},
		"display_name":   {"Test User"},
		"household_name": {"Test House"},
	}
	req := postForm("/register", form)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	h.HandleRegister(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "test@example.com") {
		t.Fatal("expected email to be preserved in error response")
	}
	if !strings.Contains(body, "Test User") {
		t.Fatal("expected display name to be preserved in error response")
	}
}

// --- POST /logout ---

func TestHandleLogout_ClearsCookieAndRedirects(t *testing.T) {
	h, svc := setupHandler(t)
	session := registerFirstUser(t, svc)

	req := httptest.NewRequest("POST", "/logout", nil)
	ctx := WithUser(req.Context(), &AuthUser{
		ID:          session.UserID,
		HouseholdID: session.HouseholdID,
		Role:        "admin",
		SessionID:   session.ID,
	})
	rec := httptest.NewRecorder()

	h.HandleLogout(rec, req.WithContext(ctx))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("expected redirect to /login, got %q", loc)
	}
	if !hasClearedSessionCookie(rec) {
		t.Fatal("expected session cookie to be cleared")
	}

	// verify session was actually deleted
	_, appErr := svc.ValidateSession(req.Context(), session.ID)
	if appErr == nil {
		t.Fatal("expected session to be deleted after logout")
	}
}

func TestHandleLogout_HTMX(t *testing.T) {
	h, svc := setupHandler(t)
	session := registerFirstUser(t, svc)

	req := httptest.NewRequest("POST", "/logout", nil)
	req.Header.Set("HX-Request", "true")
	ctx := WithUser(req.Context(), &AuthUser{
		ID:          session.UserID,
		HouseholdID: session.HouseholdID,
		Role:        "admin",
		SessionID:   session.ID,
	})
	rec := httptest.NewRecorder()

	h.HandleLogout(rec, req.WithContext(ctx))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if hxRedirect := rec.Header().Get("HX-Redirect"); hxRedirect != "/login" {
		t.Fatalf("expected HX-Redirect /login, got %q", hxRedirect)
	}
}

func TestHandleLogout_WithoutSession(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("POST", "/logout", nil)
	rec := httptest.NewRecorder()

	h.HandleLogout(rec, req)

	// should still clear cookie and redirect, even without a session
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", rec.Code)
	}
	if !hasClearedSessionCookie(rec) {
		t.Fatal("expected session cookie to be cleared")
	}
}

// --- HTMX response header tests ---

func TestHandleLogin_HTMX_ErrorRendersCardOnly(t *testing.T) {
	h, _ := setupHandler(t)

	form := url.Values{"email": {""}, "password": {""}}
	req := postForm("/login", form)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	h.HandleLogin(rec, req)

	body := rec.Body.String()
	// HTMX error should render just the card, not a full page
	if strings.Contains(body, "<!doctype html>") {
		t.Fatal("expected card-only response for HTMX error, got full page")
	}
	if !strings.Contains(body, "Sign in to Shelterkin") {
		t.Fatal("expected login card content in HTMX error response")
	}
}

func TestHandleRegister_HTMX_ErrorRendersCardOnly(t *testing.T) {
	h, _ := setupHandler(t)

	form := url.Values{"email": {""}, "password": {"short"}}
	req := postForm("/register", form)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	h.HandleRegister(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, "<!doctype html>") {
		t.Fatal("expected card-only response for HTMX error, got full page")
	}
	if !strings.Contains(body, "Create your account") {
		t.Fatal("expected register card content in HTMX error response")
	}
}

// --- Rate limit by IP through handler ---

func TestHandleLogin_RateLimitByIP(t *testing.T) {
	h, _ := setupHandler(t)

	for i := 0; i < maxFailedLoginsByIP; i++ {
		email := ulid.New() + "@test.com"
		form := url.Values{"email": {email}, "password": {"wrong"}}
		req := postForm("/login", form)
		rec := httptest.NewRecorder()
		h.HandleLogin(rec, req)
	}

	form := url.Values{"email": {"any@test.com"}, "password": {"any"}}
	req := postForm("/login", form)
	rec := httptest.NewRecorder()

	h.HandleLogin(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if retryAfter := rec.Header().Get("Retry-After"); retryAfter != fmt.Sprintf("%d", int(rateLimitRetryAfter.Seconds())) {
		t.Fatalf("expected Retry-After %d, got %q", int(rateLimitRetryAfter.Seconds()), retryAfter)
	}
}

// --- helpers ---

func hasSessionCookie(rec *httptest.ResponseRecorder) bool {
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName && c.Value != "" && c.MaxAge >= 0 {
			return true
		}
	}
	return false
}

func hasClearedSessionCookie(rec *httptest.ResponseRecorder) bool {
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName && c.MaxAge < 0 {
			return true
		}
	}
	return false
}
