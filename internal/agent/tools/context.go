package tools

import (
	"context"
	"net/http"

	"github.com/charmbracelet/crush/internal/agent/filestatecache"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/filetracker"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/skills"
)

// ToolContext is a rich dependency injection struct passed to tool
// constructors and available at tool execution time via context. It
// replaces the scattered pattern of individual constructor parameters
// (workingDir, permissions, lspManager, etc.) with a single struct that
// can be extended without breaking every constructor signature.
//
// ToolContext is inspired by claude-code's ToolUseContext, which carries
// all tool dependencies in one place.
type ToolContext struct {
	WorkingDir  string
	Permissions permission.Service
	LSPManager  *lsp.Manager
	FileTracker filetracker.Service
	History     history.Service
	Sessions    session.Service
	Config      *config.ConfigStore
	HTTPClient  *http.Client

	// Skills state (session-start snapshot).
	AllSkills    []*skills.Skill
	ActiveSkills []*skills.Skill
	SkillTracker *skills.Tracker

	// Optional tool-specific configuration.
	Attribution *config.Attribution
	ModelID     string
	LsConfig    config.ToolLs
	GrepConfig  config.ToolGrep
	LogFile     string
	SkillsPaths []string

	// UserContext is the memoized project context file content
	// (AGENTS.md, CRUSH.md, etc.) loaded at session start.
	UserContext string

	// SystemContext is the memoized system state snapshot (git status,
	// branch, recent commits) captured at session start.
	SystemContext string

	// FileStateCache is an LRU cache of recently read files, used for
	// deduplication and staleness detection.
	FileStateCache *filestatecache.Cache
}

// toolContextKey is the context key for *ToolContext.
type toolContextKey struct{}

// WithToolContext returns a context carrying tc.
func WithToolContext(ctx context.Context, tc *ToolContext) context.Context {
	return context.WithValue(ctx, toolContextKey{}, tc)
}

// GetToolContext returns the *ToolContext stored in ctx, or nil.
func GetToolContext(ctx context.Context) *ToolContext {
	tc, _ := ctx.Value(toolContextKey{}).(*ToolContext)
	return tc
}
