package EasyLogMaster

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // embed IANA timezone data for environments without system zoneinfo
)

var defaultLogger *Logger

type fileHandles struct {
	plain  *log.Logger
	json   *log.Logger
	fPlain *os.File
	fJson  *os.File
	date   string
}

// Logger is the core log writer. Use Init to create the package-level instance,
// then call LogError, LogInfo, LogWarning, or LogDebug.
type Logger struct {
	config   LoggerConfig
	mu       sync.Mutex
	handles  map[string]*fileHandles
	location *time.Location
}

// Init initialises the package-level logger. Must be called once at application startup.
// If LogBasePath is empty it is derived as filepath.Dir(LogPath).
// The timezone for log timestamps defaults to Europe/Rome; UTC is used as fallback.
func Init(config LoggerConfig) {
	if config.LogBasePath == "" && config.LogPath != "" {
		config.LogBasePath = filepath.Dir(config.LogPath)
	}
	if config.DefaultLogField == "" {
		config.DefaultLogField = "app"
	}
	loc, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		loc = time.UTC
	}
	defaultLogger = &Logger{
		config:   config,
		handles:  make(map[string]*fileHandles),
		location: loc,
	}
}

// resolveFilePath returns the directory and base name for a log file.
//
// fileName conventions:
//   - ""                  → config defaults: (LogPath, LogFileName)
//   - "bridge_log"        → plain name, same dir as default: (LogPath, "bridge_log")
//   - "bridge/bridge_log" → contains "/", subdir under LogBasePath: ("logs/bridge", "bridge_log")
func (l *Logger) resolveFilePath(fileName string) (dirPath, baseName string) {
	if fileName == "" {
		return l.config.LogPath, l.config.LogFileName
	}
	if idx := strings.LastIndex(fileName, "/"); idx >= 0 {
		return l.config.LogBasePath + "/" + fileName[:idx], fileName[idx+1:]
	}
	return l.config.LogPath, fileName
}

// effectiveDirPath applies the GO_LOG_PATH env-var override used in Electron mode.
// In Electron mode all files land flat in GO_LOG_PATH regardless of subdirectory.
func effectiveDirPath(dirPath string) string {
	if override := os.Getenv("GO_LOG_PATH"); override != "" {
		return override
	}
	return dirPath
}

// getHandles returns cached file handles for (dirPath, baseName), creating them
// on first call or after a date rollover at midnight. Old handles are explicitly
// closed before replacement to avoid file descriptor leaks.
// log.Logger is internally thread-safe; the mutex only guards the handle map.
func (l *Logger) getHandles(dirPath, baseName string) (*fileHandles, error) {
	today := time.Now().Format("20060102")
	key := dirPath + "/" + baseName

	l.mu.Lock()
	defer l.mu.Unlock()

	if h, ok := l.handles[key]; ok {
		if h.date == today {
			return h, nil
		}
		// Date rolled over: close the old file descriptors before replacing them.
		h.fPlain.Close()
		h.fJson.Close()
	}

	if err := os.MkdirAll(dirPath, os.ModePerm); err != nil {
		return nil, fmt.Errorf("easyGoLog: mkdir %s: %w", dirPath, err)
	}

	fPlain, err := os.OpenFile(dirPath+"/"+baseName+"_"+today+".log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return nil, fmt.Errorf("easyGoLog: open plain log: %w", err)
	}
	fJson, err := os.OpenFile(dirPath+"/"+baseName+"_json_"+today+".log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		fPlain.Close()
		return nil, fmt.Errorf("easyGoLog: open json log: %w", err)
	}

	h := &fileHandles{
		plain:  log.New(fPlain, "", log.LstdFlags),
		json:   log.New(fJson, "", log.LstdFlags),
		fPlain: fPlain,
		fJson:  fJson,
		date:   today,
	}
	l.handles[key] = h
	return h, nil
}

func (l *Logger) write(fileName, route, message, sql, level string, jsonResponse any) {
	if !includeLevel(l.config.AppEnv, level) {
		return
	}

	dirPath, baseName := l.resolveFilePath(fileName)
	dirPath = effectiveDirPath(dirPath)

	h, err := l.getHandles(dirPath, baseName)
	if err != nil {
		fmt.Println(err)
		return
	}

	now := time.Now().In(l.location)

	entry := LogEntry{
		Timestamp:    now,
		Level:        level,
		Route:        route,
		Message:      message,
		Sql:          sql,
		JsonResponse: jsonResponse,
	}

	logJSON, err := json.Marshal(entry)
	if err != nil {
		fmt.Printf("easyGoLog: marshal error: %v\n", err)
		return
	}

	h.plain.Println(now.Format("2006-01-02 15:04:05") + " | " + level + " | " + route + " | " + message + " | " + sql)
	h.json.Println(string(logJSON))
}

func includeLevel(env, level string) bool {
	switch env {
	case "dev":
		return level == "ERROR" || level == "DEBUG" || level == "WARNING" || level == "INFO"
	case "preprod":
		return level == "ERROR" || level == "WARNING" || level == "INFO"
	case "prod":
		return level == "ERROR" || level == "INFO"
	}
	return false
}

// ── Package-level API ─────────────────────────────────────────────────────────

// LogError writes an ERROR-level entry.
// fileName controls the destination file: "" uses the default configured in Init;
// a plain name (e.g. "bridge_log") writes to the default directory;
// a slash-separated path (e.g. "bridge/bridge_log") writes to a subdirectory
// under LogBasePath.
func LogError(fileName, route, message, sql string, jsonResponse any) {
	if defaultLogger != nil {
		defaultLogger.write(fileName, route, message, sql, "ERROR", jsonResponse)
	}
}

// LogDebug writes a DEBUG-level entry.
// Omitted in preprod and prod environments (see LoggerConfig.AppEnv).
func LogDebug(fileName, route, message, sql string, jsonResponse any) {
	if defaultLogger != nil {
		defaultLogger.write(fileName, route, message, sql, "DEBUG", jsonResponse)
	}
}

// LogWarning writes a WARNING-level entry.
// Omitted in prod environment (see LoggerConfig.AppEnv).
func LogWarning(fileName, route, message, sql string, jsonResponse any) {
	if defaultLogger != nil {
		defaultLogger.write(fileName, route, message, sql, "WARNING", jsonResponse)
	}
}

// LogInfo writes an INFO-level entry.
func LogInfo(fileName, route, message, sql string, jsonResponse any) {
	if defaultLogger != nil {
		defaultLogger.write(fileName, route, message, sql, "INFO", jsonResponse)
	}
}
