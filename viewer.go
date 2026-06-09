package EasyLogMaster

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed viewer.html
var viewerHTML string

// ViewerConfig configures both the log viewer page and the ExportLogHandler endpoint.
type ViewerConfig struct {
	// LoginURL is the POST endpoint used for authentication, e.g. "/api/login".
	LoginURL string
	// LogAPIBase is the GET base path for log data, e.g. "/api/logger".
	LogAPIBase string
	// Sources lists the log sources shown in the viewer's source dropdown.
	// Each value must match the logField path segment used by ExportLogHandler.
	Sources []string

	// AllowedRoleIDs restricts access to users whose role matches one of these IDs.
	// Leave empty to allow any authenticated user.
	AllowedRoleIDs []string
	// GinRoleContextKey is the Gin context key where the JWT middleware stores the role.
	// Defaults to "X-Logged-User-Role".
	GinRoleContextKey string
	// RoleClaimKey is the JWT payload claim used as fallback when GinRoleContextKey
	// is not set in the Gin context. Defaults to "role_id".
	RoleClaimKey string

	// MaxCachedFiles is the maximum number of log files kept in memory simultaneously.
	// When exceeded, the least recently accessed file is evicted. Defaults to 10.
	MaxCachedFiles int
}

// applyDefaults fills in zero-value fields of cfg with sensible defaults.
func applyDefaults(cfg *ViewerConfig) {
	if cfg.LoginURL == "" {
		cfg.LoginURL = "/api/login"
	}
	if cfg.LogAPIBase == "" {
		cfg.LogAPIBase = "/api/logger"
	}
	if len(cfg.Sources) == 0 {
		cfg.Sources = []string{"app"}
	}
	if cfg.GinRoleContextKey == "" {
		cfg.GinRoleContextKey = "X-Logged-User-Role"
	}
	if cfg.RoleClaimKey == "" {
		cfg.RoleClaimKey = "role_id"
	}
	if cfg.MaxCachedFiles <= 0 {
		cfg.MaxCachedFiles = 10
	}
}

// ViewerHandler returns a Gin handler that serves the HTML log viewer page.
// The page handles its own JWT authentication via the browser's localStorage,
// so it can be registered on a public (unauthenticated) router.
//
//	publicRouter.GET("logger/viewer", easyGoLog.ViewerHandler(easyGoLog.ViewerConfig{
//	    LoginURL:   "/api/login",
//	    LogAPIBase: "/api/logger",
//	    Sources:    []string{"app", "bridge", "shopify"},
//	}))
func ViewerHandler(cfg ViewerConfig) gin.HandlerFunc {
	applyDefaults(&cfg)

	sourcesJSON, _ := json.Marshal(cfg.Sources)
	allowedJSON, _ := json.Marshal(cfg.AllowedRoleIDs)

	html := viewerHTML
	html = strings.ReplaceAll(html, "{{LOGIN_URL}}", cfg.LoginURL)
	html = strings.ReplaceAll(html, "{{LOG_API_BASE}}", cfg.LogAPIBase)
	html = strings.ReplaceAll(html, "{{SOURCES_JSON}}", string(sourcesJSON))
	html = strings.ReplaceAll(html, "{{ALLOWED_ROLES_JSON}}", string(allowedJSON))
	html = strings.ReplaceAll(html, "{{ROLE_CLAIM_KEY}}", cfg.RoleClaimKey)
	page := []byte(html)

	return func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", page)
	}
}
