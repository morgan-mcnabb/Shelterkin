package components

func alertClass(level string) string {
	switch level {
	case "error":
		return "alert-error"
	case "warning":
		return "alert-warning"
	case "info":
		return "alert-info"
	case "success":
		return "alert-success"
	default:
		return "alert-info"
	}
}
