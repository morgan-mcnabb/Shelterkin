package apperror

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestValidationError(t *testing.T) {
	err := Validation("email", "Email is required")
	if err.Type != TypeValidation {
		t.Errorf("expected TypeValidation, got %d", err.Type)
	}
	if err.Field != "email" {
		t.Errorf("expected field 'email', got %q", err.Field)
	}
	if err.Message != "Email is required" {
		t.Errorf("expected message 'Email is required', got %q", err.Message)
	}
	if err.Error() != "Email is required" {
		t.Errorf("expected Error() 'Email is required', got %q", err.Error())
	}
}

func TestNotFoundError(t *testing.T) {
	err := NotFound("medication", "abc123")
	if err.Type != TypeNotFound {
		t.Errorf("expected TypeNotFound, got %d", err.Type)
	}
	if err.Message != "medication not found" {
		t.Errorf("expected 'medication not found', got %q", err.Message)
	}
}

func TestInternalErrorWraps(t *testing.T) {
	underlying := errors.New("connection refused")
	err := Internal("Failed to save", underlying)
	if err.Type != TypeInternal {
		t.Errorf("expected TypeInternal, got %d", err.Type)
	}
	if err.Unwrap() != underlying {
		t.Error("expected Unwrap to return underlying error")
	}
	if err.Error() != "Failed to save: connection refused" {
		t.Errorf("unexpected Error(): %q", err.Error())
	}
}

func TestRateLimitedError(t *testing.T) {
	err := RateLimited("Too many login attempts", 30*time.Second)
	if err.Type != TypeRateLimited {
		t.Errorf("expected TypeRateLimited, got %d", err.Type)
	}
	if err.RetryAfter != 30*time.Second {
		t.Errorf("expected RetryAfter 30s, got %v", err.RetryAfter)
	}
}

func TestHTTPStatusMapping(t *testing.T) {
	tests := []struct {
		errType  Type
		wantCode int
	}{
		{TypeValidation, http.StatusBadRequest},
		{TypeNotFound, http.StatusNotFound},
		{TypeUnauthorized, http.StatusUnauthorized},
		{TypeForbidden, http.StatusForbidden},
		{TypeConflict, http.StatusConflict},
		{TypeRateLimited, http.StatusTooManyRequests},
		{TypeUnavailable, http.StatusServiceUnavailable},
		{TypeInternal, http.StatusInternalServerError},
	}

	for _, tt := range tests {
		err := &Error{Type: tt.errType, Message: "test"}
		got := HTTPStatus(err)
		if got != tt.wantCode {
			t.Errorf("HTTPStatus(%d) = %d, want %d", tt.errType, got, tt.wantCode)
		}
	}
}

func TestValidationErrors(t *testing.T) {
	ve := &ValidationErrors{}
	if ve.HasErrors() {
		t.Error("expected no errors initially")
	}

	ve.Add("name", "Name is required")
	ve.Add("email", "Email is required")

	if !ve.HasErrors() {
		t.Error("expected errors after Add")
	}
	if len(ve.Errors) != 2 {
		t.Errorf("expected 2 errors, got %d", len(ve.Errors))
	}
	if ve.Errors[0].Field != "name" {
		t.Errorf("expected first error field 'name', got %q", ve.Errors[0].Field)
	}
}

func TestIsUniqueConstraintViolation(t *testing.T) {
	if IsUniqueConstraintViolation(nil) {
		t.Error("expected false for nil error")
	}
	if IsUniqueConstraintViolation(errors.New("something else")) {
		t.Error("expected false for non-constraint error")
	}

	constraintErr := errors.New("UNIQUE constraint failed: users.email_hash")
	if !IsUniqueConstraintViolation(constraintErr) {
		t.Error("expected true for unique constraint error")
	}
}

func TestParseConstraintColumn(t *testing.T) {
	if col := ParseConstraintColumn(nil); col != "" {
		t.Errorf("expected empty for nil, got %q", col)
	}

	err := errors.New("UNIQUE constraint failed: users.email_hash")
	col := ParseConstraintColumn(err)
	if col != "email_hash" {
		t.Errorf("expected 'email_hash', got %q", col)
	}
}
