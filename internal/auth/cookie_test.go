package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSignAndVerifySessionID(t *testing.T) {
	secret := "test-secret-that-is-long-enough!!"
	sessionID := "01JWABCDEF1234567890ABCDEF"

	signed := signSessionID(sessionID, secret)
	got, err := VerifyAndExtractSessionID(signed, secret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != sessionID {
		t.Fatalf("expected session ID %q, got %q", sessionID, got)
	}
}

func TestVerifyWithWrongSecret(t *testing.T) {
	signed := signSessionID("some-session-id", "correct-secret-32-chars-long!!!!")
	_, err := VerifyAndExtractSessionID(signed, "wrong-secret-also-32-chars-long!")
	if err == nil {
		t.Fatal("expected error for wrong secret, got nil")
	}
}

func TestVerifyMalformedCookie(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"no delimiters", "garbage"},
		{"one delimiter", "part1|part2"},
		{"empty signature", "part1|part2|"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := VerifyAndExtractSessionID(tc.value, "some-secret")
			if err == nil {
				t.Fatalf("expected error for %q, got nil", tc.value)
			}
		})
	}
}

func TestSetSessionCookie(t *testing.T) {
	w := httptest.NewRecorder()
	SetSessionCookie(w, "test-session-id", "test-secret-32-chars-long!!!!!!!!", false)

	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}

	cookie := cookies[0]
	if cookie.Name != SessionCookieName {
		t.Fatalf("expected cookie name %q, got %q", SessionCookieName, cookie.Name)
	}
	if !cookie.HttpOnly {
		t.Fatal("expected HttpOnly to be true")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("expected SameSite=Lax")
	}
	if cookie.Secure {
		t.Fatal("expected Secure=false for non-https")
	}
	if cookie.MaxAge != cookieMaxAge {
		t.Fatalf("expected MaxAge %d, got %d", cookieMaxAge, cookie.MaxAge)
	}

	// verify the cookie value is a valid signed session ID
	got, err := VerifyAndExtractSessionID(cookie.Value, "test-secret-32-chars-long!!!!!!!!")
	if err != nil {
		t.Fatalf("cookie value failed verification: %v", err)
	}
	if got != "test-session-id" {
		t.Fatalf("expected session ID %q, got %q", "test-session-id", got)
	}
}

func TestClearSessionCookie(t *testing.T) {
	w := httptest.NewRecorder()
	ClearSessionCookie(w, false)

	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	if cookies[0].MaxAge != -1 {
		t.Fatalf("expected MaxAge -1, got %d", cookies[0].MaxAge)
	}
}
