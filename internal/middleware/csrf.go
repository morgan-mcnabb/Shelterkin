package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

const (
	csrfTokenKey   contextKey = "csrf_token"
	csrfCookieName            = "_csrf"
	csrfHeaderName            = "X-CSRF-Token"
	csrfFormFieldName         = "_csrf_token"
	csrfTokenBytes            = 32
)

const csrfCookieMaxAge = 30 * 24 * 60 * 60 // 30 days

func CSRF(key string, secure bool) func(http.Handler) http.Handler {
	keyBytes := []byte(key)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSafeMethod(r.Method) {
				token := existingValidToken(r, keyBytes)
				if token == "" {
					token = newSignedToken(keyBytes)
					setCSRFCookie(w, token, secure)
				}
				ctx := context.WithValue(r.Context(), csrfTokenKey, token)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			cookie, err := r.Cookie(csrfCookieName)
			if err != nil {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}

			headerToken := r.Header.Get(csrfHeaderName)
			if headerToken == "" {
				headerToken = r.FormValue(csrfFormFieldName)
			}
			if !validTokenPair(cookie.Value, headerToken, keyBytes) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}

			// put token in context so re-rendered forms on error still have it
			ctx := context.WithValue(r.Context(), csrfTokenKey, cookie.Value)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func GetCSRFToken(ctx context.Context) string {
	if token, ok := ctx.Value(csrfTokenKey).(string); ok {
		return token
	}
	return ""
}

func isSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func newSignedToken(key []byte) string {
	b := make([]byte, csrfTokenBytes)
	rand.Read(b)
	nonce := hex.EncodeToString(b)
	mac := computeCSRFHMAC(nonce, key)
	return nonce + "." + mac
}

func computeCSRFHMAC(message string, key []byte) string {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(message))
	return hex.EncodeToString(h.Sum(nil))
}

func verifySignedToken(token string, key []byte) bool {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return false
	}
	expectedMAC := computeCSRFHMAC(parts[0], key)
	return hmac.Equal([]byte(parts[1]), []byte(expectedMAC))
}

func existingValidToken(r *http.Request, key []byte) string {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil {
		return ""
	}
	if !verifySignedToken(cookie.Value, key) {
		return ""
	}
	return cookie.Value
}

func validTokenPair(cookieValue, headerValue string, key []byte) bool {
	if headerValue == "" {
		return false
	}
	if !hmac.Equal([]byte(cookieValue), []byte(headerValue)) {
		return false
	}
	return verifySignedToken(cookieValue, key)
}

func setCSRFCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   csrfCookieMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}
