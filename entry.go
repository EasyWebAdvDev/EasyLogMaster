package EasyLogMaster

import "time"

// LogEntry represents a single structured log event as stored in the JSON log file.
type LogEntry struct {
	// Timestamp is the time the event was logged, in the configured timezone.
	Timestamp time.Time `json:"timestamp"`
	// Level is the severity: ERROR, WARNING, INFO, or DEBUG.
	Level string `json:"level"`
	// Route identifies the caller context, e.g. an HTTP route or domain operation.
	Route string `json:"route"`
	// Message is the human-readable description of the event.
	Message string `json:"message"`
	// Sql contains any SQL query associated with the event (may be empty).
	Sql string `json:"sql"`
	// JsonResponse holds any structured data attached to the event (optional).
	JsonResponse any `json:"jsonResponse"`
}

// JsonResponse is a helper type for attaching both input and output JSON to a log entry.
type JsonResponse struct {
	JsonInput    any `json:"jsonInput"`
	JsonResponse any `json:"jsonResponse"`
}
