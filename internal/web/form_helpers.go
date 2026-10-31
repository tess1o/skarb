package web

import (
	"skarb/internal/domain"
	"strconv"
	"strings"
)

func parseOptionalInt64(raw string) *int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil
	}
	return &v
}

// Invalid input must not be mistaken for unlinking. The service validates the ID.
func parseTelegramID(raw string) (*int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, &domain.ValidationError{Field: "telegram_id", Message: "Telegram ID має бути додатним цілим числом."}
	}
	return &id, nil
}
