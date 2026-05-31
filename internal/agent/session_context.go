package agent

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/home"
	"github.com/charmbracelet/crush/internal/version"
)

// SessionContext holds memoized per-session context that is expensive to
// compute (git status, context files). It is inspired by claude-code's
// getSystemContext() / getUserContext() pattern, where both are computed
// once per conversation and reused across turns.
type SessionContext struct {
	mu            sync.RWMutex
	workingDir    string
	contextPaths  []string
	gitStatus     string
	gitLoaded     bool
	userContext   string
	userCtxLoaded bool
}

// NewSessionContext creates a SessionContext for the given working
// directory. Context file paths are taken from config; if empty, defaults
// are used.
func NewSessionContext(workingDir string, opts *config.Options) *SessionContext {
	paths := defaultContextPaths
	if opts != nil && len(opts.ContextPaths) > 0 {
		paths = opts.ContextPaths
	}
	return &SessionContext{
		workingDir:   workingDir,
		contextPaths: paths,
	}
}

// defaultContextPaths mirrors config.defaultContextPaths — the list of
// context files discovered from the working directory when none are
// explicitly configured.
var defaultContextPaths = []string{
	"AGENTS.md",
	"agents.md",
	"Agents.md",
	"CRUSH.md",
	"crush.md",
	"Crush.md",
	"CRUSH.local.md",
	"crush.local.md",
	"Crush.local.md",
	"CLAUDE.md",
	"CLAUDE.local.md",
	"GEMINI.md",
	"gemini.md",
	".github/copilot-instructions.md",
	".cursorrules",
	".cursor/rules/",
}

// GitStatus returns a snapshot of git state at session start, memoized.
// The returned string is suitable for injection into a system prompt.
func (sc *SessionContext) GitStatus() string {
	sc.mu.RLock()
	if sc.gitLoaded {
		defer sc.mu.RUnlock()
		return sc.gitStatus
	}
	sc.mu.RUnlock()

	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.gitLoaded {
		return sc.gitStatus
	}
	sc.gitStatus = sc.computeGitStatus()
	sc.gitLoaded = true
	return sc.gitStatus
}

func (sc *SessionContext) computeGitStatus() string {
	if _, err := exec.LookPath("git"); err != nil {
		return ""
	}
	// Check if we're in a git repo.
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = sc.workingDir
	if err := cmd.Run(); err != nil {
		return ""
	}

	var parts []string

	// Branch.
	if out, err := exec.Command("git", "branch", "--show-current").Output(); err == nil {
		branch := strings.TrimSpace(string(out))
		if branch != "" {
			parts = append(parts, "Current branch: "+branch)
		}
	}

	// Main branch.
	if out, err := exec.Command("git", "remote", "show", "origin").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "HEAD branch:") {
				parts = append(parts, "Main branch (you will usually use this for PRs): "+strings.TrimPrefix(line, "HEAD branch:"))
				break
			}
		}
	}

	// Git user.
	if out, err := exec.Command("git", "config", "user.name").Output(); err == nil {
		name := strings.TrimSpace(string(out))
		if name != "" {
			parts = append(parts, "Git user: "+name)
		}
	}

	// Status.
	statusCmd := exec.Command("git", "--no-optional-locks", "status", "--short")
	statusCmd.Dir = sc.workingDir
	statusOut, err := statusCmd.Output()
	if err == nil {
		status := strings.TrimSpace(string(statusOut))
		if status == "" {
			status = "(clean)"
		}
		parts = append(parts, "Status:\n"+status)
	}

	// Recent commits.
	logCmd := exec.Command("git", "--no-optional-locks", "log", "--oneline", "-n", "5")
	logCmd.Dir = sc.workingDir
	logOut, err := logCmd.Output()
	if err == nil {
		log := strings.TrimSpace(string(logOut))
		if log != "" {
			parts = append(parts, "Recent commits:\n"+log)
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return "This is the git status at the start of the conversation. Note that this status is a snapshot in time, and will not update during the conversation.\n\n" + strings.Join(parts, "\n\n")
}

// UserContext returns the content of discovered context files
// (AGENTS.md, CRUSH.md, etc.) memoized per session.
func (sc *SessionContext) UserContext() string {
	sc.mu.RLock()
	if sc.userCtxLoaded {
		defer sc.mu.RUnlock()
		return sc.userContext
	}
	sc.mu.RUnlock()

	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.userCtxLoaded {
		return sc.userContext
	}
	sc.userContext = sc.computeUserContext()
	sc.userCtxLoaded = true
	return sc.userContext
}

func (sc *SessionContext) computeUserContext() string {
	var out strings.Builder
	for _, p := range sc.contextPaths {
		// Expand home directory.
		expanded := home.Long(p)
		// Walk up from working dir to find context files.
		content, err := sc.readContextFile(expanded)
		if err != nil {
			slog.Debug("Context file not found or unreadable", "path", expanded, "error", err)
			continue
		}
		if content != "" {
			out.WriteString(content)
			out.WriteString("\n\n")
		}
	}
	// Append current date (claude-code pattern).
	out.WriteString(fmt.Sprintf("Today's date is %s.", nowDate()))
	return out.String()
}

func (sc *SessionContext) readContextFile(path string) (string, error) {
	// If path is relative, look in working dir.
	if !strings.HasPrefix(path, "/") {
		path = sc.workingDir + "/" + path
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// nowDate returns the current date formatted as YYYY-MM-DD.
// If CRUSH_DATE_OVERRIDE is set, its value is returned instead.
func nowDate() string {
	if d := os.Getenv("CRUSH_DATE_OVERRIDE"); d != "" {
		return d
	}
	return time.Now().Format("2006-01-02")
}

// Invalidate clears all cached context so the next access recomputes.
func (sc *SessionContext) Invalidate() {
	sc.mu.Lock()
	sc.gitLoaded = false
	sc.userCtxLoaded = false
	sc.gitStatus = ""
	sc.userContext = ""
	sc.mu.Unlock()
}

// SystemPromptPrefix returns version information suitable for a system
// prompt prefix, inspired by claude-code's user agent string.
func SystemPromptPrefix() string {
	return fmt.Sprintf("Crush %s (https://github.com/charmbracelet/crush)", version.Version)
}

// ClampContextFiles reads the working directory's context files and returns
// their concatenated content. This is the non-memoized variant used when a
// SessionContext is not available.
func ClampContextFiles(workingDir string, paths []string) string {
	if len(paths) == 0 {
		paths = defaultContextPaths
	}
	sc := &SessionContext{
		workingDir:   workingDir,
		contextPaths: paths,
	}
	return sc.UserContext()
}
