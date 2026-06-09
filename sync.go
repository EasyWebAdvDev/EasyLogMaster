package EasyLogMaster

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"
)

// WriteSyncLogFile appends a progress line to SYNC_STATUS.log.
// Used by the sync-client (Electron mode) to report per-entity import progress.
// When status is "OK" or "KO" the previous progress line is removed first,
// so the file always reflects the current operation.
func WriteSyncLogFile(entity, status string, pageNumbers, currentPage int) {
	if status == "OK" || status == "KO" {
		removeLastRowSyncLogFile()
	}
	message := time.Now().Format("02/01/2006 15:04") + " " + entity + ": " +
		strconv.Itoa(currentPage) + " di " + strconv.Itoa(pageNumbers) + " " + status
	appendSyncLog(message)
}

// RemoveLoggerSync truncates SYNC_STATUS.log, resetting the sync status display.
func RemoveLoggerSync() {
	path := syncLogPath()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		fmt.Printf("easyGoLog RemoveLoggerSync: %v\n", err)
		return
	}
	f.Close()
	if err := os.Truncate(path, 0); err != nil {
		fmt.Printf("easyGoLog RemoveLoggerSync truncate: %v\n", err)
	}
}

func appendSyncLog(message string) {
	path := syncLogPath()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		fmt.Printf("easyGoLog appendSyncLog: %v\n", err)
		return
	}
	defer f.Close()
	l := log.New(f, "", 0)
	l.Println(message)
}

func removeLastRowSyncLogFile() {
	path := syncLogPath()
	f, err := os.Open(path)
	if err != nil {
		return
	}
	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	f.Close()

	if len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}

	f, err = os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	for _, line := range lines {
		if _, err := f.WriteString(line + "\n"); err != nil {
			fmt.Printf("easyGoLog removeLastRow: %v\n", err)
			return
		}
	}
}

func syncLogPath() string {
	base := ""
	if defaultLogger != nil {
		base = defaultLogger.config.LogPath
	}
	if override := os.Getenv("GO_LOG_PATH"); override != "" {
		base = override
	}
	return base + "/SYNC_STATUS.log"
}
