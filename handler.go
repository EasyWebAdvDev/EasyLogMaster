package EasyLogMaster

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// jsonRegex matches the outermost JSON object on a single log line.
var jsonRegex = regexp.MustCompile(`(?i)\{.*\}`)

// maxCachedFiles is the maximum number of log files kept in memory simultaneously.
// Set from ViewerConfig.MaxCachedFiles by ExportLogHandler; defaults to 10.
var maxCachedFiles = 10

// cachedLog holds the fully-parsed entries for one log file.
//
// searchIndex stores a lowercase JSON string for each entry, built once at parse
// time so that text-filter queries never need to re-marshal entries.
//
// fileSize tracks the byte offset up to which the file has been parsed, enabling
// incremental updates: when the file grows, only the new bytes are read.
//
// lastAccess is updated atomically on every access and drives LRU eviction.
type cachedLog struct {
	entries     []LogEntry
	searchIndex []string
	mtime       time.Time
	fileSize    int64
	lastAccess  atomic.Int64 // Unix nano
}

var (
	logCache = make(map[string]*cachedLog)
	cacheMu  sync.RWMutex
)

// ExportLogHandler returns a Gin handler for the log-export endpoint.
// Register it on a PROTECTED router — the endpoint reads production log files.
// Pass a ViewerConfig with AllowedRoleIDs to restrict access by role.
//
//	protectedRouter.GET("logger/:logField/:logData", easyGoLog.ExportLogHandler(cfg))
func ExportLogHandler(cfg ViewerConfig) gin.HandlerFunc {
	applyDefaults(&cfg)
	maxCachedFiles = cfg.MaxCachedFiles
	return func(c *gin.Context) {
		if defaultLogger == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"msg": "logger not initialized"})
			return
		}
		if len(cfg.AllowedRoleIDs) > 0 {
			role := resolveRole(c, cfg)
			if !containsStr(cfg.AllowedRoleIDs, role) {
				c.JSON(http.StatusForbidden, gin.H{"msg": "Accesso non autorizzato"})
				c.Abort()
				return
			}
		}
		defaultLogger.exportLog(c)
	}
}

// resolveRole returns the role of the current request.
// It first checks the Gin context key set by the JWT middleware,
// then falls back to decoding the JWT payload directly.
func resolveRole(c *gin.Context, cfg ViewerConfig) string {
	if role := c.GetString(cfg.GinRoleContextKey); role != "" {
		return role
	}
	return roleFromToken(c.GetHeader("Authorization"), cfg.RoleClaimKey)
}

// roleFromToken decodes the JWT payload without re-verifying the signature
// (the middleware already validated it) and returns the value of claimKey.
func roleFromToken(authHeader, claimKey string) string {
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 {
		return ""
	}
	segments := strings.Split(parts[1], ".")
	if len(segments) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		return ""
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	if v, ok := claims[claimKey]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func (l *Logger) exportLog(c *gin.Context) {
	logField := c.Param("logField")
	logData := c.Param("logData")
	if logField == "" || logData == "" {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "logField and logData are required"})
		return
	}

	level := strings.ToUpper(c.Query("level"))
	filter := c.Query("filter")
	order := c.Query("order")

	page, err1 := strconv.ParseUint(c.Query("page"), 10, 64)
	pageSize, err2 := strconv.ParseUint(c.Query("page_size"), 10, 64)
	if err1 != nil || err2 != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "invalid pagination params"})
		return
	}

	// Build the file path matching the convention used by the writer.
	// When logField matches DefaultLogField (default "app"), resolve to the
	// configured LogPath + LogFileName (the default writer path).
	// Otherwise: {LogBasePath}/{logField}/{logField}_log_json_{date}.log
	var filePath string
	if logField == l.config.DefaultLogField {
		filePath = l.config.LogPath + "/" + l.config.LogFileName + "_json_" + logData + ".log"
	} else {
		filePath = l.config.LogBasePath + "/" + logField + "/" + logField + "_log_json_" + logData + ".log"
	}

	cached, err := loadCached(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNoContent, "")
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"msg": err.Error()})
		}
		return
	}

	filtered := filterEntries(cached, level, filter)
	total := len(filtered)
	if total == 0 {
		c.JSON(http.StatusNoContent, nil)
		return
	}

	var paginated []LogEntry
	if order == "DESC" {
		paginated = paginateFromEnd(filtered, int(page), int(pageSize))
	} else {
		paginated = paginateArray(filtered, int(page), int(pageSize))
	}

	c.Header("X-Total-Count", strconv.Itoa(total))
	c.JSON(http.StatusOK, paginated)
}

// loadCached returns the cachedLog for filePath, parsing or updating as needed.
//
// Three cases:
//  1. Same mtime → return cached entry, zero I/O.
//  2. File has grown (append-only log) → parse only the new bytes from the previous
//     offset and merge them into the cached entry.
//  3. First load, truncation, or delta error → full parse.
func loadCached(filePath string) (*cachedLog, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			cacheMu.Lock()
			delete(logCache, filePath)
			cacheMu.Unlock()
		}
		return nil, err
	}
	mtime := info.ModTime()
	size := info.Size()

	cacheMu.RLock()
	cached, ok := logCache[filePath]
	cacheMu.RUnlock()

	if ok && cached.mtime.Equal(mtime) {
		cached.lastAccess.Store(time.Now().UnixNano())
		return cached, nil
	}

	next, parseErr := func() (*cachedLog, error) {
		if ok && size > cached.fileSize {
			// File has only grown: parse the new bytes and merge with cached data.
			dEntries, dIndex, err := parseLogFileDelta(filePath, cached.fileSize)
			if err == nil {
				n := len(cached.entries) + len(dEntries)
				merged := &cachedLog{
					entries:     make([]LogEntry, n),
					searchIndex: make([]string, n),
					mtime:       mtime,
					fileSize:    size,
				}
				copy(merged.entries, cached.entries)
				copy(merged.entries[len(cached.entries):], dEntries)
				copy(merged.searchIndex, cached.searchIndex)
				copy(merged.searchIndex[len(cached.searchIndex):], dIndex)
				return merged, nil
			}
		}
		// First load, truncation, or delta error: full parse.
		entries, index, err := parseLogFile(filePath)
		if err != nil {
			return nil, err
		}
		return &cachedLog{entries: entries, searchIndex: index, mtime: mtime, fileSize: size}, nil
	}()

	if parseErr != nil {
		return nil, parseErr
	}

	next.lastAccess.Store(time.Now().UnixNano())

	cacheMu.Lock()
	logCache[filePath] = next
	evictLRU()
	cacheMu.Unlock()

	return next, nil
}

// filterEntries returns the entries from cached that match level and filter.
// When both are empty the original slice is returned with no allocation.
// Text search uses the pre-computed searchIndex to avoid marshalling on each call.
func filterEntries(cached *cachedLog, level, filter string) []LogEntry {
	if level == "" && filter == "" {
		return cached.entries
	}
	filterLower := strings.ToLower(filter)
	out := make([]LogEntry, 0, len(cached.entries))
	for i := range cached.entries {
		e := &cached.entries[i]
		if level != "" && strings.ToUpper(e.Level) != level {
			continue
		}
		if filter != "" && !strings.Contains(cached.searchIndex[i], filterLower) {
			continue
		}
		out = append(out, *e)
	}
	return out
}

// paginateFromEnd extracts a page from the END of items (DESC order) without
// reversing the full slice. The returned entries are in newest-first order.
func paginateFromEnd[T any](items []T, page, pageSize int) []T {
	n := len(items)
	end := n - page*pageSize
	if end <= 0 {
		return []T{}
	}
	start := end - pageSize
	if start < 0 {
		start = 0
	}
	result := make([]T, end-start)
	for i, j := 0, end-1; j >= start; i, j = i+1, j-1 {
		result[i] = items[j]
	}
	return result
}

// evictLRU removes the least recently accessed entry when logCache exceeds
// maxCachedFiles. Must be called with cacheMu write lock held.
func evictLRU() {
	if len(logCache) <= maxCachedFiles {
		return
	}
	var lruPath string
	var lruTime int64 = 1<<63 - 1
	for path, entry := range logCache {
		if t := entry.lastAccess.Load(); t < lruTime {
			lruPath = path
			lruTime = t
		}
	}
	delete(logCache, lruPath)
}

// parseLogFile reads and parses the entire log file, returning entries in
// chronological order and a parallel searchIndex of lowercase JSON strings.
func parseLogFile(filePath string) ([]LogEntry, []string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	return parseReader(bufio.NewReaderSize(file, 1<<20))
}

// parseLogFileDelta reads and parses only the bytes from fromOffset to the end
// of the file, used for incremental cache updates when the log file has grown.
func parseLogFileDelta(filePath string, fromOffset int64) ([]LogEntry, []string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	if _, err := file.Seek(fromOffset, io.SeekStart); err != nil {
		return nil, nil, err
	}
	return parseReader(bufio.NewReaderSize(file, 1<<20))
}

// parseReader reads log entries line by line from r.
// It returns the parsed entries and a parallel searchIndex containing the
// lowercase raw JSON match for each entry, used for efficient text-filter queries.
func parseReader(r *bufio.Reader) ([]LogEntry, []string, error) {
	var entries []LogEntry
	var searchIndex []string

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, nil, err
		}

		match := jsonRegex.FindString(line)
		if match == "" {
			continue
		}

		var entry LogEntry
		if err := json.Unmarshal([]byte(match), &entry); err != nil {
			fmt.Printf("easyGoLog: decode error: %v\n", err)
			continue
		}

		entries = append(entries, entry)
		searchIndex = append(searchIndex, strings.ToLower(match))
	}

	return entries, searchIndex, nil
}

// paginateArray returns a slice of items for the given zero-based page and pageSize.
func paginateArray[T any](items []T, page, pageSize int) []T {
	if page < 0 || pageSize < 1 {
		return []T{}
	}
	start := page * pageSize
	if start >= len(items) {
		return []T{}
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}
