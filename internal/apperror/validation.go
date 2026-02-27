package apperror

import "strings"

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
	messages := make([]string, len(ve.Errors))
	for i, e := range ve.Errors {
		messages[i] = e.Message
	}
	return strings.Join(messages, "; ")
}
