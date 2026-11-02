package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/shell"
	"github.com/stretchr/testify/require"
)

type mockBashPermissionService struct {
	*pubsub.Broker[permission.PermissionRequest]
}

func (m *mockBashPermissionService) Request(ctx context.Context, req permission.CreatePermissionRequest) (bool, error) {
	return true, nil
}

func (m *mockBashPermissionService) Grant(req permission.PermissionRequest) bool { return true }

func (m *mockBashPermissionService) Deny(req permission.PermissionRequest) bool { return true }

func (m *mockBashPermissionService) GrantPersistent(req permission.PermissionRequest) bool {
	return true
}

func (m *mockBashPermissionService) AutoApproveSession(sessionID string) {}

func (m *mockBashPermissionService) ActiveRequest() (permission.PermissionRequest, bool) {
	return permission.PermissionRequest{}, false
}

func (m *mockBashPermissionService) SetSkipRequests(skip bool) {}

func (m *mockBashPermissionService) SkipRequests() bool {
	return false
}

func (m *mockBashPermissionService) SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[permission.PermissionNotification] {
	return make(<-chan pubsub.Event[permission.PermissionNotification])
}

func (m *mockBashPermissionService) SetPermissionMode(mode permission.PermissionMode) {}

func (m *mockBashPermissionService) PermissionMode() permission.PermissionMode {
	return permission.PermissionModeNormal
}

func (m *mockBashPermissionService) SubscribeModeChanges(ctx context.Context) <-chan pubsub.Event[permission.ModeChangedEvent] {
	return make(<-chan pubsub.Event[permission.ModeChangedEvent])
}

func TestBashTool_DefaultAutoBackgroundThreshold(t *testing.T) {
	workingDir := t.TempDir()
	tool := newBashToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "default threshold",
		Command:     "echo done",
	})

	require.False(t, resp.IsError)
	var meta BashResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.False(t, meta.Background)
	require.Empty(t, meta.ShellID)
	require.Contains(t, meta.Output, "done")
}

func TestBashTool_CustomAutoBackgroundThreshold(t *testing.T) {
	workingDir := t.TempDir()
	tool := newBashToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description:         "custom threshold",
		Command:             "sleep 1.5 && echo done",
		AutoBackgroundAfter: 1,
	})

	require.False(t, resp.IsError)
	var meta BashResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.True(t, meta.Background)
	require.NotEmpty(t, meta.ShellID)
	require.Contains(t, resp.Content, "moved to background")

	bgManager := shell.GetBackgroundShellManager()
	require.NoError(t, bgManager.Kill(meta.ShellID))
}

type recordingPermissionService struct {
	*pubsub.Broker[permission.PermissionRequest]
	requestCount int
	allow        bool
	mode         permission.PermissionMode
	lastDanger   string
}

func (m *recordingPermissionService) Request(ctx context.Context, req permission.CreatePermissionRequest) (bool, error) {
	m.requestCount++
	m.lastDanger = req.Danger
	return m.allow, nil
}

func (m *recordingPermissionService) Grant(req permission.PermissionRequest) bool { return true }

func (m *recordingPermissionService) Deny(req permission.PermissionRequest) bool { return true }

func (m *recordingPermissionService) GrantPersistent(req permission.PermissionRequest) bool {
	return true
}

func (m *recordingPermissionService) AutoApproveSession(sessionID string) {}

func (m *recordingPermissionService) ActiveRequest() (permission.PermissionRequest, bool) {
	return permission.PermissionRequest{}, false
}

func (m *recordingPermissionService) SetSkipRequests(skip bool) {}

func (m *recordingPermissionService) SkipRequests() bool {
	return false
}

func (m *recordingPermissionService) SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[permission.PermissionNotification] {
	return make(<-chan pubsub.Event[permission.PermissionNotification])
}

func (m *recordingPermissionService) SetPermissionMode(mode permission.PermissionMode) { m.mode = mode }

func (m *recordingPermissionService) PermissionMode() permission.PermissionMode { return m.mode }

func (m *recordingPermissionService) SubscribeModeChanges(ctx context.Context) <-chan pubsub.Event[permission.ModeChangedEvent] {
	return make(<-chan pubsub.Event[permission.ModeChangedEvent])
}

func newBashToolForTest(workingDir string) fantasy.AgentTool {
	permissions := &mockBashPermissionService{Broker: pubsub.NewBroker[permission.PermissionRequest]()}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	return NewBashTool(permissions, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil)
}

func newBashToolWithRecordingPerms(workingDir string, allow bool) (fantasy.AgentTool, *recordingPermissionService) {
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  allow,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	return NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil), perms
}

// blockIfconfig returns a permissions config that blocks ifconfig, standing
// in for a user-configured dangerous-command list. The built-in list is empty,
// so these tests have to opt in.
func blockIfconfig() *config.Permissions {
	return &config.Permissions{BlockedCommands: []string{"ifconfig"}}
}

func TestBashTool_ChainedCommandsRequirePermission(t *testing.T) {
	workingDir := t.TempDir()
	tool, perms := newBashToolWithRecordingPerms(workingDir, true)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	// A chain is judged by its parts, so joining two read-only commands is
	// no more of a prompt than either one alone would be.
	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "chained ls",
		Command:     "ls && echo done",
	})

	require.False(t, resp.IsError)
	require.Equal(t, 0, perms.requestCount, "a chain of read-only commands should not prompt")

	// One part that writes is enough to require approval for the whole chain.
	perms.requestCount = 0
	resp = runBashTool(t, tool, ctx, BashParams{
		Description: "chained touch",
		Command:     "ls && touch newfile",
	})

	require.False(t, resp.IsError)
	require.Equal(t, 1, perms.requestCount, "a chain containing a write must prompt")

	// Plain ls should NOT trigger permission check.
	perms.requestCount = 0
	resp = runBashTool(t, tool, ctx, BashParams{
		Description: "plain ls",
		Command:     "ls -la",
	})

	require.False(t, resp.IsError)
	require.Equal(t, 0, perms.requestCount, "plain ls should not trigger permission request")
}

func TestBashTool_ChainedCommandsDenied(t *testing.T) {
	workingDir := t.TempDir()
	tool, perms := newBashToolWithRecordingPerms(workingDir, false)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "chained write denied",
		Command:     "ls && touch newfile",
	})

	require.Equal(t, 1, perms.requestCount)
	require.Contains(t, resp.Content, "User denied permission")
}

func TestBashTool_DestructiveCommandRejected(t *testing.T) {
	workingDir := t.TempDir()
	tool, perms := newBashToolWithRecordingPerms(workingDir, true)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	// A catastrophic command is rejected before the permission layer, so
	// no permission request is made even in allow mode.
	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "destructive command",
		Command:     "ls && rm -rf /",
	})

	require.Equal(t, 0, perms.requestCount)
	require.Contains(t, resp.Content, "Refused to run a destructive command")
}

// Commands are not blocked out of the box: the built-in list is empty so
// that ordinary tools like curl and sudo only ever prompt. Catastrophic
// commands are handled by checkDangerousCommand instead.
func TestDefaultBlockListIsEmpty(t *testing.T) {
	require.Empty(t, resolveBlockedCommands(nil))
	require.Nil(t, blockFuncs(resolveBlockedCommands(nil)))
}

// A user who wants a policy can configure one, and it is matched the same
// way the shell matches command names.
func TestConfiguredBlockList(t *testing.T) {
	tests := []struct {
		name      string
		perms     *config.Permissions
		command   string
		dangerous bool
	}{
		{
			name:      "curl is not blocked by default",
			command:   "curl https://example.com",
			dangerous: false,
		},
		{
			name:      "sudo is not blocked by default",
			command:   "sudo apt-get update",
			dangerous: false,
		},
		{
			name:      "configured command is blocked",
			perms:     &config.Permissions{BlockedCommands: []string{"curl"}},
			command:   "curl https://example.com",
			dangerous: true,
		},
		{
			name:      "configuration is normalized",
			perms:     &config.Permissions{BlockedCommands: []string{"/usr/bin/CURL"}},
			command:   "curl https://example.com",
			dangerous: true,
		},
		{
			name:      "quoted command name does not evade the list",
			perms:     &config.Permissions{BlockedCommands: []string{"curl"}},
			command:   `"curl" https://example.com`,
			dangerous: true,
		},
		{
			name: "allowed command overrides the list",
			perms: &config.Permissions{
				BlockedCommands: []string{"curl"},
				AllowedCommands: []string{"curl"},
			},
			command:   "curl https://example.com",
			dangerous: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shell.IsCommandBlocked(tt.command, blockFuncs(resolveBlockedCommands(tt.perms)))
			require.Equal(t, tt.dangerous, result, "command: %s", tt.command)
		})
	}
}

func runBashTool(t *testing.T, tool fantasy.AgentTool, ctx context.Context, params BashParams) fantasy.ToolResponse {
	t.Helper()

	input, err := json.Marshal(params)
	require.NoError(t, err)

	call := fantasy.ToolCall{
		ID:    "test-call",
		Name:  BashToolName,
		Input: string(input),
	}

	resp, err := tool.Run(ctx, call)
	require.NoError(t, err)
	return resp
}

func TestTruncateOutputValidUTF8(t *testing.T) {
	t.Parallel()
	// CJK characters are 2 cells wide; this string is far wider than
	// MaxOutputLength so TruncateOutput must truncate it.
	content := strings.Repeat("你好世界", MaxOutputLength)

	out := TruncateOutput(content, t.TempDir())
	require.True(t, utf8.ValidString(out), "truncated output must stay valid UTF-8")
	require.Contains(t, out, "lines truncated")
}

func TestTruncateOutputShortContent(t *testing.T) {
	t.Parallel()
	content := "short output"
	require.Equal(t, content, TruncateOutput(content, t.TempDir()))
}

func TestTruncateOutputEmoji(t *testing.T) {
	t.Parallel()
	// Emoji with ZWJ sequences should not be split.
	content := strings.Repeat("👨‍👩‍👧‍👦", MaxOutputLength)

	out := TruncateOutput(content, t.TempDir())
	require.True(t, utf8.ValidString(out), "truncated output must stay valid UTF-8")
	require.Contains(t, out, "lines truncated")
}

func TestResolveBlockedCommands(t *testing.T) {
	t.Parallel()

	t.Run("nil config blocks nothing", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, resolveBlockedCommands(nil))
	})

	t.Run("blocked_commands is the whole list", func(t *testing.T) {
		t.Parallel()
		got := resolveBlockedCommands(&config.Permissions{
			BlockedCommands: []string{"kubectl", "terraform"},
		})
		require.Equal(t, []string{"kubectl", "terraform"}, got)
	})

	t.Run("allowed_commands removes entries", func(t *testing.T) {
		t.Parallel()
		got := resolveBlockedCommands(&config.Permissions{
			BlockedCommands: []string{"curl", "wget", "sudo"},
			AllowedCommands: []string{"curl", "wget"},
		})
		require.Equal(t, []string{"sudo"}, got)
	})

	t.Run("allowed_commands wins over blocked_commands", func(t *testing.T) {
		t.Parallel()
		got := resolveBlockedCommands(&config.Permissions{
			BlockedCommands: []string{"kubectl"},
			AllowedCommands: []string{"kubectl"},
		})
		require.NotContains(t, got, "kubectl")
	})

	t.Run("entries keep their order and duplicates collapse", func(t *testing.T) {
		t.Parallel()
		got := resolveBlockedCommands(&config.Permissions{
			BlockedCommands: []string{"curl", "kubectl", "kubectl"},
		})
		// The list is embedded verbatim in the tool description, so entries
		// must keep the order they were written in.
		require.Equal(t, []string{"curl", "kubectl"}, got,
			"additions keep their order; duplicates are dropped")
	})

	t.Run("allowing a command stops it reading as dangerous", func(t *testing.T) {
		t.Parallel()
		blocked := resolveBlockedCommands(&config.Permissions{
			BlockedCommands: []string{"curl", "sudo"},
			AllowedCommands: []string{"curl"},
		})
		require.False(t, shell.IsCommandBlocked("curl https://example.com", blockFuncs(blocked)))
		require.True(t, shell.IsCommandBlocked("sudo rm -rf /", blockFuncs(blocked)))
	})
}

// TestBashTool_ApprovedDangerousCommandRuns pins the rule that a command the
// user was warned about and approved anyway reaches the shell unguarded:
// re-blocking it would override the answer they just gave.
func TestBashTool_ApprovedDangerousCommandRuns(t *testing.T) {
	workingDir := t.TempDir()
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  true,
		mode:   permission.PermissionModeNormal,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, blockIfconfig())
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	// `ifconfig` is on the default dangerous list, so it is prompted for.
	// Approving it must not then be overridden by the exec-time block list.
	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "approved dangerous command",
		Command:     "ifconfig --help",
	})

	require.Equal(t, 1, perms.requestCount, "a dangerous command must be prompted for")
	require.Equal(t, "ifconfig", perms.lastDanger,
		"the prompt must name what tripped the check")
	require.NotContains(t, resp.Content, "is a dangerous command",
		"an approved command must not be re-blocked at exec time")
}

// TestBashTool_UnflaggedCommandKeepsBlockList is the other side of that rule.
// The static check expands commands with globbing off, so a glob hides the
// dangerous name from it and yolo mode auto-approves the call unseen. The block
// list stays on at exec time, where the glob has resolved and the real command
// name is finally visible.
func TestBashTool_UnflaggedCommandKeepsBlockList(t *testing.T) {
	workingDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workingDir, "ifconfig"), []byte("#!/bin/sh\n"), 0o755))
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  true,
		mode:   permission.PermissionModeYolo,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, blockIfconfig())
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "indirect dangerous command",
		Command:     "ifconf?g --help",
	})

	require.Empty(t, perms.lastDanger, "the static check cannot see through a glob")
	require.Contains(t, resp.Content, "is a dangerous command")
}

// TestBashTool_SysadminModeSkipsBlockList pins sysadmin mode as the deliberate
// exception: it asks for nothing and blocks nothing.
func TestBashTool_SysadminModeSkipsBlockList(t *testing.T) {
	workingDir := t.TempDir()
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  true,
		mode:   permission.PermissionModeSysadmin,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, blockIfconfig())
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	require.NoError(t, os.WriteFile(filepath.Join(workingDir, "ifconfig"), []byte("#!/bin/sh\nexit 0\n"), 0o755))

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "indirect dangerous command",
		Command:     "ifconf?g --help",
	})

	require.NotContains(t, resp.Content, "is a dangerous command")
}

// TestBashTool_DeniedDangerousCommandDoesNotRun is the other half: denial at
// the permission layer stops the command, no block list required.
func TestBashTool_DeniedDangerousCommandDoesNotRun(t *testing.T) {
	workingDir := t.TempDir()
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  false,
		mode:   permission.PermissionModeNormal,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, blockIfconfig())
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "denied dangerous command",
		Command:     "ifconfig --help",
	})

	require.Equal(t, 1, perms.requestCount)
	require.True(t, resp.IsError)
}

// TestBashTool_SafeReadOnlyPathStillEnforcesBlockList pins the one branch that
// never asks: a safe-prefix command skips the permission request entirely, so
// the block list stays on there as a backstop.
func TestBashTool_SafeReadOnlyPathStillEnforcesBlockList(t *testing.T) {
	workingDir := t.TempDir()
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  true,
		mode:   permission.PermissionModeNormal,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, blockIfconfig())
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "safe command",
		Command:     "pwd",
	})

	require.Zero(t, perms.requestCount, "safe read-only commands must not prompt")
	require.False(t, resp.IsError)
}

// TestBashTool_YoloModeDoesNotPromptForDangerousCommands pins the rule that
// yolo mode means stop asking. It drives the real permission service rather
// than a mock, because the whole behaviour under test lives there.
func TestBashTool_YoloModeDoesNotPromptForDangerousCommands(t *testing.T) {
	workingDir := t.TempDir()
	perms := permission.NewPermissionService(workingDir, nil, nil)
	perms.SetPermissionMode(permission.PermissionModeYolo)

	// Any prompt would land here. Nothing should.
	prompts := perms.Subscribe(t.Context())

	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, blockIfconfig())
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "test-session")

	// `ifconfig` is on the default dangerous list. In yolo it must run
	// without asking, and without being re-blocked at exec time either.
	// Run it off the test goroutine so a regression that reinstates the
	// prompt fails here in a few seconds rather than hanging until the
	// whole package times out.
	content := make(chan string, 1)
	go func() {
		resp := runBashTool(t, tool, ctx, BashParams{
			Description: "dangerous command in yolo mode",
			Command:     "ifconfig --help",
		})
		content <- resp.Content
	}()

	var got string
	select {
	case got = <-content:
	case <-time.After(30 * time.Second):
		t.Fatal("yolo mode blocked waiting for a permission prompt that should never have been raised")
	}

	require.NotContains(t, got, "is a dangerous command",
		"a dangerous command must not be blocked in yolo mode")

	select {
	case p := <-prompts:
		t.Fatalf("yolo mode must not prompt, but got a request for %q", p.Payload.Description)
	default:
	}
}

// TestBashTool_NormalModeStillPromptsForDangerousCommands is the other half of
// the rule: manual mode keeps asking.
func TestBashTool_NormalModeStillPromptsForDangerousCommands(t *testing.T) {
	workingDir := t.TempDir()
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  true,
		mode:   permission.PermissionModeNormal,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, blockIfconfig())
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "test-session")

	runBashTool(t, tool, ctx, BashParams{
		Description: "dangerous command in normal mode",
		Command:     "ifconfig --help",
	})

	require.Equal(t, 1, perms.requestCount, "normal mode must still ask")
	require.Equal(t, "ifconfig", perms.lastDanger,
		"the prompt must name what tripped the check")
}

// TestBlockedCommandErrorNamesTheCommand checks that a command stopped at
// exec time names the command that was recognised. The static check and the
// run-time check have to agree about that, or the same command is described
// two different ways depending on which one caught it.
func TestBlockedCommandErrorNamesTheCommand(t *testing.T) {
	workingDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workingDir, "ifconfig"), []byte("#!/bin/sh\n"), 0o755))
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  true,
		mode:   permission.PermissionModeYolo,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, blockIfconfig())
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "indirect dangerous command",
		Command:     "ifconf?g --help",
	})

	require.Contains(t, resp.Content, "ifconfig is a dangerous command",
		"the error must name the command, not the spelling that reached it")
}
