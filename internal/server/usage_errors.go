package server

import "strings"

func sanitizeUsageError(err error) string {
	message := err.Error()
	if idx := strings.Index(message, ": {"); idx >= 0 {
		message = message[:idx]
	}
	if idx := strings.Index(message, " body="); idx >= 0 {
		message = message[:idx]
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "Usage information is temporarily unavailable."
	}
	return message
}
