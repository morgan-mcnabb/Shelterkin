package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const testCSRFKey = "01234567890123456789012345678901"

func TestCSRFGetSetsTokenAndCookie(t *testing.T) {
	handler := CSRF(testCSRFKey, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := GetCSRFToken(r.Context())
		if token == "" {
			t.Error("expected CSRF token in context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var found bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == csrfCookieName {
			found = true
			if c.Value == "" {
				t.Error("expected non-empty CSRF cookie value")
			}
			if !c.HttpOnly {
				t.Error("expected HttpOnly flag on CSRF cookie")
			}
			if c.SameSite != http.SameSiteLaxMode {
				t.Error("expected SameSite=Lax on CSRF cookie")
			}
			if c.MaxAge != csrfCookieMaxAge {
				t.Errorf("expected MaxAge=%d, got %d", csrfCookieMaxAge, c.MaxAge)
			}
		}
	}
	if !found {
		t.Error("expected _csrf cookie to be set")
	}
}

func TestCSRFGetReusesExistingValidToken(t *testing.T) {
	existingToken := newSignedToken([]byte(testCSRFKey))

	var contextToken string
	handler := CSRF(testCSRFKey, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contextToken = GetCSRFToken(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: existingToken})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if contextToken != existingToken {
		t.Error("expected middleware to reuse existing valid token")
	}

	for _, c := range rec.Result().Cookies() {
		if c.Name == csrfCookieName {
			t.Error("expected no new _csrf cookie when existing one is valid")
		}
	}
}

func TestCSRFGetReplacesInvalidExistingToken(t *testing.T) {
	handler := CSRF(testCSRFKey, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := GetCSRFToken(r.Context())
		if token == "" {
			t.Error("expected CSRF token in context")
		}
		if token == "garbage" {
			t.Error("expected a new token, not the garbage one")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "garbage"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var found bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == csrfCookieName {
			found = true
		}
	}
	if !found {
		t.Error("expected new _csrf cookie to replace invalid one")
	}
}

func TestCSRFHeadAndOptionsPass(t *testing.T) {
	handler := CSRF(testCSRFKey, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, method := range []string{"HEAD", "OPTIONS"} {
		req := httptest.NewRequest(method, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", method, rec.Code)
		}
	}
}

func TestCSRFPostAllowedWithValidToken(t *testing.T) {
	token := newSignedToken([]byte(testCSRFKey))

	handler := CSRF(testCSRFKey, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	req.Header.Set(csrfHeaderName, token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestCSRFPostSetsTokenInContext(t *testing.T) {
	token := newSignedToken([]byte(testCSRFKey))

	handler := CSRF(testCSRFKey, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxToken := GetCSRFToken(r.Context())
		if ctxToken != token {
			t.Errorf("expected token in context on POST, got %q", ctxToken)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	req.Header.Set(csrfHeaderName, token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
}

func TestCSRFPostBlockedWithoutToken(t *testing.T) {
	handler := CSRF(testCSRFKey, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	req := httptest.NewRequest("POST", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestCSRFPostAllowedWithFormField(t *testing.T) {
	token := newSignedToken([]byte(testCSRFKey))

	handler := CSRF(testCSRFKey, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	form := url.Values{csrfFormFieldName: {token}}
	req := httptest.NewRequest("POST", "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestCSRFPostHeaderTakesPrecedenceOverFormField(t *testing.T) {
	token := newSignedToken([]byte(testCSRFKey))
	wrongToken := newSignedToken([]byte(testCSRFKey))

	handler := CSRF(testCSRFKey, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// header has the correct token, form field has a different one
	form := url.Values{csrfFormFieldName: {wrongToken}}
	req := httptest.NewRequest("POST", "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	req.Header.Set(csrfHeaderName, token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 (header takes precedence), got %d", rec.Code)
	}
}

func TestCSRFPostBlockedWithoutHeaderOrFormField(t *testing.T) {
	token := newSignedToken([]byte(testCSRFKey))

	handler := CSRF(testCSRFKey, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	req := httptest.NewRequest("POST", "/", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestCSRFPostBlockedWithMismatchedTokens(t *testing.T) {
	token1 := newSignedToken([]byte(testCSRFKey))
	token2 := newSignedToken([]byte(testCSRFKey))

	handler := CSRF(testCSRFKey, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	req := httptest.NewRequest("POST", "/", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token1})
	req.Header.Set(csrfHeaderName, token2)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestCSRFPostBlockedWithInvalidSignature(t *testing.T) {
	handler := CSRF(testCSRFKey, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	forgedToken := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	req := httptest.NewRequest("POST", "/", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: forgedToken})
	req.Header.Set(csrfHeaderName, forgedToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestCSRFPostBlockedWithWrongKey(t *testing.T) {
	wrongKey := "abcdefghijklmnopqrstuvwxyz012345"
	token := newSignedToken([]byte(wrongKey))

	handler := CSRF(testCSRFKey, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	req := httptest.NewRequest("POST", "/", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	req.Header.Set(csrfHeaderName, token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestCSRFPutDeletePatchRequireToken(t *testing.T) {
	handler := CSRF(testCSRFKey, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("handler should not be called for %s", r.Method)
	}))

	for _, method := range []string{"PUT", "DELETE", "PATCH"} {
		req := httptest.NewRequest(method, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: expected 403, got %d", method, rec.Code)
		}
	}
}

func TestCSRFCookieSecureFlag(t *testing.T) {
	handler := CSRF(testCSRFKey, true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	for _, c := range rec.Result().Cookies() {
		if c.Name == csrfCookieName {
			if !c.Secure {
				t.Error("expected Secure flag when secure=true")
			}
			return
		}
	}
	t.Error("expected _csrf cookie to be set")
}

func TestGetCSRFTokenWithoutMiddleware(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	token := GetCSRFToken(req.Context())
	if token != "" {
		t.Errorf("expected empty CSRF token without middleware, got %q", token)
	}
}
