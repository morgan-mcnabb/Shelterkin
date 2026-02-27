package auth

import "context"

type contextKey string

const userContextKey contextKey = "auth_user"

type AuthUser struct {
	ID          string
	HouseholdID string
	Role        string
	SessionID   string
}

func WithUser(ctx context.Context, user *AuthUser) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

func GetUser(ctx context.Context) *AuthUser {
	if user, ok := ctx.Value(userContextKey).(*AuthUser); ok {
		return user
	}
	return nil
}
