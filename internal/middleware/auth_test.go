package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shelterkin/shelterkin/internal/apperror"
	"github.com/shelterkin/shelterkin/internal/auth"
)

const testSessionSecret = "test-session-secret-that-is-32ch"

type mockSessionValidator struct {
	user   *auth.AuthUser
	appErr *apperror.Error
}

func (m *mockSessionValidator) ValidateSession(_ context.Context, _ string) (*auth.AuthUser, *apperror.Error) {
	return m.user, m.appErr
}

// signedCookieValue uses SetSessionCookie to produce a validly-signed cookie value
func signedCookieValue(sessionID string) string {
	rec := httptest.NewRecorder()
	auth.SetSessionCookie(rec, sessionID, testSessionSecret, false)
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			return c.Value
		}
	}
	return ""
}

// LoadSession tests

func TestLoadSessionInjectsUser(t *testing.T) {
	expectedUser := &auth.AuthUser{
		ID:          "user-1",
		HouseholdID: "hh-1",
		Role:        "admin",
		SessionID:   "sess-1",
	}

	validator := &mockSessionValidator{user: expectedUser}
	handler := LoadSession(validator, testSessionSecret, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := auth.GetUser(r.Context())
		if user == nil {
			t.Fatal("expected user in context")
		}
		if user.ID != expectedUser.ID {
			t.Errorf("expected user ID %q, got %q", expectedUser.ID, user.ID)
		}
		if user.HouseholdID != expectedUser.HouseholdID {
			t.Errorf("expected household ID %q, got %q", expectedUser.HouseholdID, user.HouseholdID)
		}
		if user.Role != expectedUser.Role {
			t.Errorf("expected role %q, got %q", expectedUser.Role, user.Role)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: signedCookieValue("sess-1")})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestLoadSessionNoCookie(t *testing.T) {
	validator := &mockSessionValidator{user: &auth.AuthUser{ID: "should-not-appear"}}
	handler := LoadSession(validator, testSessionSecret, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := auth.GetUser(r.Context())
		if user != nil {
			t.Error("expected no user in context without cookie")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestLoadSessionInvalidSignature(t *testing.T) {
	validator := &mockSessionValidator{user: &auth.AuthUser{ID: "should-not-appear"}}
	handler := LoadSession(validator, testSessionSecret, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := auth.GetUser(r.Context())
		if user != nil {
			t.Error("expected no user with invalid cookie signature")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "garbage|data|here"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestLoadSessionExpiredSession(t *testing.T) {
	validator := &mockSessionValidator{appErr: apperror.Unauthorized("Session expired")}
	handler := LoadSession(validator, testSessionSecret, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := auth.GetUser(r.Context())
		if user != nil {
			t.Error("expected no user with expired session")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: signedCookieValue("expired-sess")})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestLoadSessionNeverRejects(t *testing.T) {
	validator := &mockSessionValidator{appErr: apperror.Internal("db error", nil)}
	var called bool
	handler := LoadSession(validator, testSessionSecret, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: signedCookieValue("sess-1")})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("LoadSession should never reject; next handler must always be called")
	}
}

func TestLoadSessionClearsCookieOnInvalidSignature(t *testing.T) {
	validator := &mockSessionValidator{user: &auth.AuthUser{ID: "should-not-appear"}}
	handler := LoadSession(validator, testSessionSecret, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "garbage|data|here"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName && c.MaxAge < 0 {
			return
		}
	}
	t.Error("expected session cookie to be cleared on invalid signature")
}

func TestLoadSessionClearsCookieOnExpiredSession(t *testing.T) {
	validator := &mockSessionValidator{appErr: apperror.Unauthorized("Session expired")}
	handler := LoadSession(validator, testSessionSecret, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: signedCookieValue("expired-sess")})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName && c.MaxAge < 0 {
			return
		}
	}
	t.Error("expected session cookie to be cleared on expired session")
}

func TestLoadSessionDoesNotClearCookieOnSuccess(t *testing.T) {
	validator := &mockSessionValidator{user: &auth.AuthUser{ID: "user-1"}}
	handler := LoadSession(validator, testSessionSecret, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: signedCookieValue("sess-1")})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			t.Error("expected no Set-Cookie for session on successful validation")
		}
	}
}

// RequireAuth tests

func TestRequireAuthAllowsAuthenticated(t *testing.T) {
	handler := RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	ctx := auth.WithUser(req.Context(), &auth.AuthUser{ID: "user-1"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestRequireAuthRedirectsUnauthenticatedFullPage(t *testing.T) {
	handler := RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/dashboard", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("expected redirect to /login, got %q", loc)
	}
}

func TestRequireAuthHTMXRedirect(t *testing.T) {
	handler := RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/dashboard", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
	if hxRedirect := rec.Header().Get("HX-Redirect"); hxRedirect != "/login" {
		t.Errorf("expected HX-Redirect /login, got %q", hxRedirect)
	}
}

// RequireRole tests

func TestRequireRoleAllowsMatchingRole(t *testing.T) {
	handler := RequireRole("admin", "member")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, role := range []string{"admin", "member"} {
		req := httptest.NewRequest("GET", "/", nil)
		ctx := auth.WithUser(req.Context(), &auth.AuthUser{ID: "user-1", Role: role})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req.WithContext(ctx))

		if rec.Code != http.StatusOK {
			t.Errorf("role %q: expected 200, got %d", role, rec.Code)
		}
	}
}

func TestRequireRoleDeniesNonMatchingRole(t *testing.T) {
	handler := RequireRole("admin")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	for _, role := range []string{"member", "caregiver", "readonly"} {
		req := httptest.NewRequest("GET", "/", nil)
		ctx := auth.WithUser(req.Context(), &auth.AuthUser{ID: "user-1", Role: role})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req.WithContext(ctx))

		if rec.Code != http.StatusForbidden {
			t.Errorf("role %q: expected 403, got %d", role, rec.Code)
		}
	}
}

func TestRequireRoleRedirectsUnauthenticated(t *testing.T) {
	handler := RequireRole("admin")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/admin", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("expected redirect to /login, got %q", loc)
	}
}

func TestRequireRoleHTMXRedirectsUnauthenticated(t *testing.T) {
	handler := RequireRole("admin")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/admin", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
	if hxRedirect := rec.Header().Get("HX-Redirect"); hxRedirect != "/login" {
		t.Errorf("expected HX-Redirect /login, got %q", hxRedirect)
	}
}

func TestRequireRoleAllFourRoles(t *testing.T) {
	handler := RequireRole("admin")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		role       string
		wantStatus int
	}{
		{"admin", http.StatusOK},
		{"member", http.StatusForbidden},
		{"caregiver", http.StatusForbidden},
		{"readonly", http.StatusForbidden},
	}

	for _, tt := range tests {
		req := httptest.NewRequest("GET", "/", nil)
		ctx := auth.WithUser(req.Context(), &auth.AuthUser{ID: "user-1", Role: tt.role})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req.WithContext(ctx))

		if rec.Code != tt.wantStatus {
			t.Errorf("role %q: expected %d, got %d", tt.role, tt.wantStatus, rec.Code)
		}
	}
}

// isHTMX tests

func TestIsHTMX(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	if isHTMX(req) {
		t.Error("expected false without HX-Request header")
	}

	req.Header.Set("HX-Request", "true")
	if !isHTMX(req) {
		t.Error("expected true with HX-Request: true")
	}

	req.Header.Set("HX-Request", "false")
	if isHTMX(req) {
		t.Error("expected false with HX-Request: false")
	}
}
