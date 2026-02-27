package apperror

import (
	"fmt"
	"net/http"
	"time"
)

type Type int

const (
	TypeValidation  Type = iota // 400
	TypeNotFound                // 404
	TypeUnauthorized            // 401
	TypeForbidden               // 403
	TypeConflict                // 409
	TypeRateLimited             // 429
	TypeUnavailable             // 503
	TypeInternal                // 500
)

type Error struct {
	Type       Type
	Message    string
	Field      string
	RetryAfter time.Duration
	Err        error
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

func ConflictWithErr(message string, err error) *Error {
	return &Error{Type: TypeConflict, Message: message, Err: err}
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

func HTTPStatus(err *Error) int {
	switch err.Type {
	case TypeValidation:
		return http.StatusBadRequest
	case TypeNotFound:
		return http.StatusNotFound
	case TypeUnauthorized:
		return http.StatusUnauthorized
	case TypeForbidden:
		return http.StatusForbidden
	case TypeConflict:
		return http.StatusConflict
	case TypeRateLimited:
		return http.StatusTooManyRequests
	case TypeUnavailable:
		return http.StatusServiceUnavailable
	case TypeInternal:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}
