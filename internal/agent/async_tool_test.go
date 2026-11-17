package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/catwalk/v3/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

type fakeAsyncTool struct {
	name string
	run  func(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error)
}

func (t *fakeAsyncTool) Info() fantasy.ToolInfo {
	return fantasy.ToolInfo{
		Name:       t.name,
		Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func (t *fakeAsyncTool) ProviderOptions() fantasy.ProviderOptions { return nil }

func (t *fakeAsyncTool) SetProviderOptions(fantasy.ProviderOptions) {}

func (t *fakeAsyncTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return t.run(ctx, call)
}

// asyncTestAgent builds a minimal agent whose detached tool results are
// captured on wakeCh instead of starting a real turn.
func asyncTestAgent(t *testing.T) (*sessionAgent, chan string) {
	t.Helper()
	wakeCh := make(chan string, 4)
	a := NewSessionAgent(SessionAgentOptions{
		AsyncToolWake: func(_ context.Context, _ string, prompt string) {
			wakeCh <- prompt
		},
	}).(*sessionAgent)
	return a, wakeCh
}

func (a *sessionAgent) asyncPendingCount(sessionID string) int {
	a.asyncMu.Lock()
	defer a.asyncMu.Unlock()
	return len(a.asyncPending[sessionID])
}

func TestAsyncTool_FastCallStaysSynchronous(t *testing.T) {
	old := AsyncToolThreshold
	AsyncToolThreshold = 100 * time.Millisecond
	t.Cleanup(func() { AsyncToolThreshold = old })

	a, wakeCh := asyncTestAgent(t)
	tool := &fakeAsyncTool{
		name: "fast",
		run: func(context.Context, fantasy.ToolCall) (fantasy.ToolResponse, error) {
			return fantasy.NewTextResponse("done immediately"), nil
		},
	}

	resp, err := a.runToolMaybeAsync(t.Context(), "sid", tool, fantasy.ToolCall{ID: "call_1", Name: "fast"})
	require.NoError(t, err)
	require.Equal(t, "done immediately", resp.Content)

	select {
	case prompt := <-wakeCh:
		t.Fatalf("unexpected wake for a fast call: %s", prompt)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestAsyncTool_SlowCallDetachesAndPushesResult(t *testing.T) {
	old := AsyncToolThreshold
	AsyncToolThreshold = 20 * time.Millisecond
	t.Cleanup(func() { AsyncToolThreshold = old })

	a, wakeCh := asyncTestAgent(t)
	release := make(chan struct{})
	tool := &fakeAsyncTool{
		name: "slow",
		run: func(ctx context.Context, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			select {
			case <-release:
				return fantasy.NewTextResponse("the real result"), nil
			case <-ctx.Done():
				return fantasy.ToolResponse{}, ctx.Err()
			}
		},
	}

	resp, err := a.runToolMaybeAsync(t.Context(), "sid", tool, fantasy.ToolCall{ID: "call_2", Name: "slow"})
	require.NoError(t, err)
	require.Contains(t, resp.Content, "background")
	require.False(t, resp.IsError)
	require.Equal(t, 1, a.asyncPendingCount("sid"))

	close(release)
	select {
	case prompt := <-wakeCh:
		require.Contains(t, prompt, "the real result")
		require.Contains(t, prompt, "slow")
		require.Contains(t, prompt, "call_2")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the pushed async result")
	}
	require.Zero(t, a.asyncPendingCount("sid"))
}

func TestAsyncTool_CancelDropsResult(t *testing.T) {
	old := AsyncToolThreshold
	AsyncToolThreshold = 20 * time.Millisecond
	t.Cleanup(func() { AsyncToolThreshold = old })

	a, wakeCh := asyncTestAgent(t)
	release := make(chan struct{})
	tool := &fakeAsyncTool{
		name: "slow",
		run: func(ctx context.Context, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			<-release
			return fantasy.NewTextResponse("cancelled result"), nil
		},
	}

	_, err := a.runToolMaybeAsync(t.Context(), "sid", tool, fantasy.ToolCall{ID: "call_3", Name: "slow"})
	require.NoError(t, err)

	a.Cancel("sid")
	require.Zero(t, a.asyncPendingCount("sid"))
	close(release)

	select {
	case prompt := <-wakeCh:
		t.Fatalf("cancelled async call should not wake the session: %s", prompt)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestWrapToolsForAsync_GatesOnWakeAndSubAgent(t *testing.T) {
	tool := &fakeAsyncTool{name: "tool"}

	noWake := NewSessionAgent(SessionAgentOptions{}).(*sessionAgent)
	wrapped := noWake.wrapToolsForAsync([]fantasy.AgentTool{tool}, "sid")
	require.Len(t, wrapped, 1)
	require.Same(t, tool, wrapped[0])

	a, _ := asyncTestAgent(t)
	wrapped = a.wrapToolsForAsync([]fantasy.AgentTool{tool}, "sid")
	require.Len(t, wrapped, 1)
	require.NotSame(t, tool, wrapped[0])

	sub := NewSessionAgent(SessionAgentOptions{
		IsSubAgent:    true,
		AsyncToolWake: func(context.Context, string, string) {},
	}).(*sessionAgent)
	wrapped = sub.wrapToolsForAsync([]fantasy.AgentTool{tool}, "sid")
	require.Same(t, tool, wrapped[0])
}

func TestFormatAsyncToolResult_Error(t *testing.T) {
	got := formatAsyncToolResult(
		fantasy.ToolCall{ID: "call_4", Name: "bash"},
		asyncToolOutcome{err: context.DeadlineExceeded},
	)
	require.True(t, strings.Contains(got, "deadline exceeded"))
	require.True(t, strings.Contains(got, "call_4"))
	require.True(t, strings.Contains(got, "<system-reminder>"))
}

// TestAsyncTool_WakesSessionAfterEOS drives a real turn through fantasy: the
// model asks for a slow tool, the turn reaches end-of-stream with the call
// still pending, and the tool's eventual result starts a fresh turn.
func TestAsyncTool_WakesSessionAfterEOS(t *testing.T) {
	old := AsyncToolThreshold
	AsyncToolThreshold = 20 * time.Millisecond
	t.Cleanup(func() { AsyncToolThreshold = old })

	env := testEnv(t)
	sess, err := env.sessions.Create(t.Context(), "async wake")
	require.NoError(t, err)
	// Seed a user message so Run skips background title generation, which
	// would call the model and throw off the stream counter.
	_, err = env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "seed"}},
	})
	require.NoError(t, err)

	var streams atomic.Int32
	model := &fakeLanguageModel{}
	model.stream = func(yield func(fantasy.StreamPart) bool) {
		if streams.Add(1) == 1 {
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputStart, ID: "call_e2e", ToolCallName: "slow"})
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputDelta, ID: "call_e2e", Delta: "{}"})
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputEnd, ID: "call_e2e"})
			yield(fantasy.StreamPart{
				Type:          fantasy.StreamPartTypeToolCall,
				ID:            "call_e2e",
				ToolCallName:  "slow",
				ToolCallInput: "{}",
			})
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls})
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "text"})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "text", Delta: "resumed"})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "text"})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}
	large := Model{
		Model:      model,
		CatwalkCfg: catwalk.Model{Name: "fake", ContextWindow: 200000, DefaultMaxTokens: 10000},
		ModelCfg:   config.SelectedModel{Provider: "fake", Model: "fake-model"},
	}

	release := make(chan struct{})
	slow := &fakeAsyncTool{
		name: "slow",
		run: func(ctx context.Context, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			select {
			case <-release:
				return fantasy.NewTextResponse("the real result"), nil
			case <-ctx.Done():
				return fantasy.ToolResponse{}, ctx.Err()
			}
		},
	}

	wakeCh := make(chan string, 1)
	wakeDone := make(chan error, 1)
	var a *sessionAgent
	a = NewSessionAgent(SessionAgentOptions{
		Models:       fixedModels(large, large),
		SystemPrompt: "test",
		Sessions:     env.sessions,
		Messages:     env.messages,
		Permissions:  env.permissions,
		Tools:        []fantasy.AgentTool{slow},
		AsyncToolWake: func(_ context.Context, sessionID, prompt string) {
			wakeCh <- prompt
			_, err := a.Run(context.Background(), SessionAgentCall{
				SessionID:         sessionID,
				Prompt:            prompt,
				HiddenUserMessage: true,
			})
			wakeDone <- err
		},
	}).(*sessionAgent)

	_, err = a.Run(t.Context(), SessionAgentCall{
		SessionID:       sess.ID,
		Prompt:          "start",
		MaxOutputTokens: 10000,
	})
	require.NoError(t, err)

	// Step 1 asked for the tool, step 2 answered without it: the turn
	// reached end-of-stream while the call was still running.
	require.Equal(t, int32(2), streams.Load())
	require.Eventually(t, func() bool { return a.asyncPendingCount(sess.ID) == 1 }, time.Second, 5*time.Millisecond)

	close(release)
	select {
	case prompt := <-wakeCh:
		require.Contains(t, prompt, "the real result")
		require.Contains(t, prompt, "call_e2e")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the async wake")
	}
	require.NoError(t, <-wakeDone)
	require.Equal(t, int32(3), streams.Load())

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	var sawPending, sawPushed bool
	for _, msg := range msgs {
		for _, tr := range msg.ToolResults() {
			if strings.Contains(tr.Content, "background") {
				sawPending = true
			}
		}
		if msg.Role == message.User && strings.Contains(msg.Content().Text, "the real result") {
			require.True(t, msg.Content().Hidden)
			sawPushed = true
		}
	}
	require.True(t, sawPending, "the transcript should record the detached call")
	require.True(t, sawPushed, "the transcript should record the pushed result")
}
