package middleware

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/shelterkin/shelterkin/internal/apperror"
	"github.com/shelterkin/shelterkin/internal/auth"
)

type SessionValidator interface {
	ValidateSession(ctx context.Context, sessionID string) (*auth.AuthUser, *apperror.Error)
}

// LoadSession reads the session cookie, verifies its signature, validates the
// session, and injects the AuthUser into context. It never rejects a request;
// if the session is missing or invalid it simply proceeds without setting a user.
func LoadSession(validator SessionValidator, sessionSecret string, secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookieValue, err := auth.GetSessionCookie(r)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}

			sessionID, err := auth.VerifyAndExtractSessionID(cookieValue, sessionSecret)
			if err != nil {
				slog.Debug("invalid session cookie signature", "error", err)
				auth.ClearSessionCookie(w, secure)
				next.ServeHTTP(w, r)
				return
			}

			user, appErr := validator.ValidateSession(r.Context(), sessionID)
			if appErr != nil {
				slog.Debug("session validation failed", "error", appErr)
				auth.ClearSessionCookie(w, secure)
				next.ServeHTTP(w, r)
				return
			}

			ctx := auth.WithUser(r.Context(), user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.GetUser(r.Context()) != nil {
			next.ServeHTTP(w, r)
			return
		}

		if isHTMX(r) {
			w.Header().Set("HX-Redirect", "/login")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
}

func RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := auth.GetUser(r.Context())
			if user == nil {
				if isHTMX(r) {
					w.Header().Set("HX-Redirect", "/login")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			for _, role := range roles {
				if user.Role == role {
					next.ServeHTTP(w, r)
					return
				}
			}

			http.Error(w, "forbidden", http.StatusForbidden)
		})
	}
}

func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}
