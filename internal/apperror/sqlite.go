package apperror

import "strings"

func IsUniqueConstraintViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed")
}

func ParseConstraintColumn(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	prefix := "UNIQUE constraint failed: "
	idx := strings.Index(msg, prefix)
	if idx == -1 {
		return ""
	}
	column := msg[idx+len(prefix):]
	// format is "table.column", extract the column part
	if dotIdx := strings.LastIndex(column, "."); dotIdx != -1 {
		return column[dotIdx+1:]
	}
	return column
}
