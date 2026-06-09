package EasyLogMaster

// LoggerConfig holds all configuration for the package-level logger.
// LogPath must be in the form "{LogBasePath}/{module}", e.g. "logs/myapp".
// LogBasePath is derived automatically from LogPath if not set explicitly.
type LoggerConfig struct {
	// LogBasePath is the root directory shared by all log modules, e.g. "logs".
	// Derived from LogPath when empty.
	LogBasePath string
	// LogPath is the directory for this module's log files, e.g. "logs/myapp".
	LogPath string
	// LogFileName is the default base filename used when no override is passed to
	// the write functions, e.g. "log".
	LogFileName string
	// AppEnv controls which severity levels are written to disk.
	// "dev" → all levels; "preprod" → INFO/WARNING/ERROR; "prod" → INFO/ERROR.
	AppEnv string
	// DefaultLogField is the logField value that ExportLogHandler maps to the
	// default log path (LogPath / LogFileName). Defaults to "app" when empty.
	DefaultLogField string
}
