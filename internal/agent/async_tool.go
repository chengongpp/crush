package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/message"
)

// AsyncToolThreshold is how long a tool call may run before it is detached
// from the turn. Calls that finish sooner behave exactly as before; slower
// ones return a synthetic "running in the background" result so the turn can
// reach end-of-stream, and their real result is pushed back into the session
// when it arrives. It is a variable so tests can shorten it.
var AsyncToolThreshold = time.Second

// asyncToolWake starts (or queues) a turn carrying a detached tool's result.
// The completion is delivered as a hidden user message: it is model context,
// not something the user typed.
func (c *coordinator) asyncToolWake(ctx context.Context, sessionID, prompt string) {
	if _, err := c.run(message.WithHiddenUserMessage(ctx), nil, nil, sessionID, prompt); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("Failed to wake session for async tool result",
			"session_id", sessionID, "error", err)
	}
}

// asyncTool decorates a fantasy.AgentTool so a slow call does not block the
// turn. It is installed at run time, after channel/MCP filtering, so the
// wrapped tool's identity checks still see the original tools.
type asyncTool struct {
	inner     fantasy.AgentTool
	owner     *sessionAgent
	sessionID string
}

func (t *asyncTool) Info() fantasy.ToolInfo {
	return t.inner.Info()
}

func (t *asyncTool) ProviderOptions() fantasy.ProviderOptions {
	return t.inner.ProviderOptions()
}

func (t *asyncTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	t.inner.SetProviderOptions(opts)
}

func (t *asyncTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return t.owner.runToolMaybeAsync(ctx, t.sessionID, t.inner, call)
}

// wrapToolsForAsync decorates tools with the async detach behavior. It is a
// no-op for sub-agents and for agents with no wake callback (tests, embedded
// agents), which keeps their behavior synchronous.
func (a *sessionAgent) wrapToolsForAsync(tools []fantasy.AgentTool, sessionID string) []fantasy.AgentTool {
	if a.isSubAgent || a.asyncWake == nil || len(tools) == 0 {
		return tools
	}
	wrapped := make([]fantasy.AgentTool, len(tools))
	for i, tool := range tools {
		wrapped[i] = &asyncTool{inner: tool, owner: a, sessionID: sessionID}
	}
	return wrapped
}

type asyncToolOutcome struct {
	response fantasy.ToolResponse
	err      error
}

// pendingAsyncTool is one detached call. cancel stops the tool; it is only
// wired to the session's explicit Cancel, never to the end of the turn, which
// is what lets a detached call outlive the end-of-stream it was detached
// from.
type pendingAsyncTool struct {
	cancel context.CancelFunc
}

func (a *sessionAgent) runToolMaybeAsync(ctx context.Context, sessionID string, inner fantasy.AgentTool, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	// Detach from the turn context but keep its values (session, message,
	// tool context). The turn's cancel must not kill a call we deliberately
	// let outlive it; only an explicit Cancel of the session does that.
	toolCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan asyncToolOutcome, 1)
	go func() {
		var outcome asyncToolOutcome
		defer func() {
			if r := recover(); r != nil {
				outcome = asyncToolOutcome{err: fmt.Errorf("tool panicked: %v", r)}
			}
			done <- outcome
		}()
		outcome.response, outcome.err = inner.Run(toolCtx, call)
	}()

	timer := time.NewTimer(AsyncToolThreshold)
	defer timer.Stop()

	select {
	case outcome := <-done:
		cancel()
		return outcome.response, outcome.err
	case <-ctx.Done():
		cancel()
		return fantasy.ToolResponse{}, ctx.Err()
	case <-timer.C:
		if ctx.Err() != nil {
			cancel()
			return fantasy.ToolResponse{}, ctx.Err()
		}
		pending := &pendingAsyncTool{cancel: cancel}
		a.trackAsyncTool(sessionID, call.ID, pending)
		slog.Info("Detached slow tool call to run asynchronously",
			"session_id", sessionID,
			"tool", call.Name,
			"tool_call_id", call.ID)
		go a.finishAsyncTool(toolCtx, sessionID, call, pending, done)
		return pendingAsyncToolResponse(call), nil
	}
}

// finishAsyncTool waits for a detached call and pushes its result back into
// the session. The push runs as a new turn (or is queued behind the active
// one), which is what wakes a turn that already reached end-of-stream.
func (a *sessionAgent) finishAsyncTool(ctx context.Context, sessionID string, call fantasy.ToolCall, pending *pendingAsyncTool, done <-chan asyncToolOutcome) {
	// Release the detached context once the result has been handed off (or
	// dropped). Cancel may already have done this; it is idempotent.
	defer pending.cancel()
	var outcome asyncToolOutcome
	select {
	case outcome = <-done:
	case <-ctx.Done():
		a.untrackAsyncTool(sessionID, call.ID, pending)
		return
	}
	a.untrackAsyncTool(sessionID, call.ID, pending)
	if ctx.Err() != nil {
		return
	}

	prompt := formatAsyncToolResult(call, outcome)
	slog.Info("Pushing async tool result into session",
		"session_id", sessionID,
		"tool", call.Name,
		"tool_call_id", call.ID,
		"is_error", outcome.err != nil || outcome.response.IsError)
	if a.asyncWake == nil {
		return
	}
	a.asyncWake(ctx, sessionID, prompt)
}

// pendingAsyncToolResponse is what the model sees instead of the real
// result. It must be clear that the call is not finished so the model does
// not treat the absence of output as a failure.
func pendingAsyncToolResponse(call fantasy.ToolCall) fantasy.ToolResponse {
	return fantasy.NewTextResponse(fmt.Sprintf(
		"The tool call %q (id: %s) is taking longer than %s, so it has been detached and is now running in the background. "+
			"Do not call it again and do not wait for it. A new message containing its result will arrive in this conversation when it finishes; "+
			"continue with other work, or end your turn and you will be woken when the result is ready.",
		call.Name, call.ID, AsyncToolThreshold,
	))
}

// formatAsyncToolResult renders the pushed completion message. It is wrapped
// in a system reminder because it is system-generated context, not user
// input.
func formatAsyncToolResult(call fantasy.ToolCall, outcome asyncToolOutcome) string {
	var b strings.Builder
	b.WriteString("<system-reminder>\n")
	fmt.Fprintf(&b, "The detached tool call %q (id: %s) has finished.\n\n", call.Name, call.ID)
	b.WriteString("Result:\n")
	switch {
	case outcome.err != nil:
		fmt.Fprintf(&b, "Error: %s", outcome.err.Error())
	case outcome.response.IsError:
		b.WriteString(outcome.response.Content)
	default:
		if outcome.response.Content != "" {
			b.WriteString(outcome.response.Content)
		}
		if len(outcome.response.Data) > 0 {
			fmt.Fprintf(&b, "\n[The tool also returned %s data (%d bytes) that cannot be forwarded as text.]",
				outcome.response.MediaType, len(outcome.response.Data))
		}
		if outcome.response.Content == "" && len(outcome.response.Data) == 0 {
			b.WriteString("(no output)")
		}
	}
	b.WriteString("\n</system-reminder>")
	return b.String()
}

func (a *sessionAgent) trackAsyncTool(sessionID, callID string, pending *pendingAsyncTool) {
	a.asyncMu.Lock()
	defer a.asyncMu.Unlock()
	if a.asyncPending == nil {
		a.asyncPending = make(map[string]map[string]*pendingAsyncTool)
	}
	calls, ok := a.asyncPending[sessionID]
	if !ok {
		calls = make(map[string]*pendingAsyncTool)
		a.asyncPending[sessionID] = calls
	}
	calls[callID] = pending
}

func (a *sessionAgent) untrackAsyncTool(sessionID, callID string, pending *pendingAsyncTool) {
	a.asyncMu.Lock()
	defer a.asyncMu.Unlock()
	calls, ok := a.asyncPending[sessionID]
	if !ok {
		return
	}
	if current, ok := calls[callID]; ok && current == pending {
		delete(calls, callID)
	}
	if len(calls) == 0 {
		delete(a.asyncPending, sessionID)
	}
}

// cancelAsyncTools stops every detached call for a session. The turn that
// spawned them may already have ended, so this is the only way a user cancel
// reaches them.
func (a *sessionAgent) cancelAsyncTools(sessionID string) {
	a.asyncMu.Lock()
	calls := a.asyncPending[sessionID]
	delete(a.asyncPending, sessionID)
	a.asyncMu.Unlock()
	for _, pending := range calls {
		pending.cancel()
	}
}

func (a *sessionAgent) cancelAllAsyncTools() {
	a.asyncMu.Lock()
	pending := a.asyncPending
	a.asyncPending = make(map[string]map[string]*pendingAsyncTool)
	a.asyncMu.Unlock()
	for _, calls := range pending {
		for _, p := range calls {
			p.cancel()
		}
	}
}
