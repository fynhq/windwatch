package model

// TruncateText bounds diagnostic strings before they are persisted or logged.
// It intentionally counts bytes to keep the storage and log limits predictable.
func TruncateText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit]
}
