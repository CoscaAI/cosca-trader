package store

import "time"

// parseTime é um parser tolerante dos formatos de timestamp que gravamos.
func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse("2006-01-02T15:04:05.999999999Z07:00", s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339Nano, s)
}
