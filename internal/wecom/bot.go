package wecom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/gorilla/websocket"
)

var errServerDisconnected = errors.New("wecom server disconnected this connection")

type Bot struct {
	ws       *workspace.AppWorkspace
	cfg      config.WeComConfig
	logger   *slog.Logger
	reporter *Reporter
	sessions *sessionStore

	locksMu   sync.Mutex
	chatLocks map[string]*sync.Mutex
}

func NewBot(ws *workspace.AppWorkspace, cfg config.WeComConfig, statePath string, reporter *Reporter) (*Bot, error) {
	sessions, err := loadSessionStore(statePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load WeCom bot state: %w", err)
	}
	if reporter == nil {
		reporter = NewReporter(io.Discard, false)
	}

	return &Bot{
		ws:        ws,
		cfg:       cfg,
		logger:    slog.Default().With("component", "wecom"),
		reporter:  reporter,
		sessions:  sessions,
		chatLocks: make(map[string]*sync.Mutex),
	}, nil
}

func (b *Bot) Run(ctx context.Context) error {
	client := &wsClient{cfg: b.cfg, logger: b.logger, reporter: b.reporter}
	client.onFrame = func(f frame) {
		go b.handleFrame(ctx, client, f)
	}
	return client.Run(ctx)
}

func (b *Bot) handleFrame(ctx context.Context, client *wsClient, f frame) {
	msg, err := parseInboundMessage(f)
	if err != nil {
		b.logger.Error("Failed to parse WeCom callback", "error", err)
		b.reporter.Error("Failed to parse WeCom callback", "error", err)
		return
	}
	if msg.IsEvent {
		b.logger.Debug("Ignoring WeCom event callback", "event_type", msg.EventType, "chat_id", msg.ChatID)
		b.reporter.IgnoredEvent(msg)
		return
	}
	b.reporter.Incoming(msg)
	b.logger.Info("Received WeCom message",
		"chat_id", msg.ChatID,
		"chat_type", msg.ChatType,
		"sender", msg.SenderID,
		"msg_id", msg.MsgID,
		"prompt_chars", len(msg.Prompt),
	)

	lock := b.chatLock(msg.ChatKey)
	lock.Lock()
	defer lock.Unlock()

	streamID := newReqID("stream")
	if thinking := strings.TrimSpace(b.cfg.ThinkingMessage); thinking != "" {
		b.reporter.Thinking(msg)
		if err := client.replyStream(msg.ReqID, streamID, thinking, false); err != nil {
			b.logger.Warn("Failed to send WeCom thinking reply", "error", err, "chat_id", msg.ChatID)
			b.reporter.Warn("Failed to send thinking reply", "chat", humanChatLabel(msg), "error", err)
		} else {
			b.reporter.ReplySent(msg, "thinking", len(thinking))
		}
	}

	replyText := msg.UnsupportedReason
	if replyText != "" {
		b.logger.Info("WeCom message is unsupported for this bridge version",
			"chat_id", msg.ChatID,
			"chat_type", msg.ChatType,
			"sender", msg.SenderID,
			"reason", replyText,
		)
		b.reporter.Warn("Unsupported message fallback",
			"chat", humanChatLabel(msg),
			"reason", replyText,
		)
	}
	if replyText == "" {
		replyText = b.runPrompt(ctx, msg)
	}
	if strings.TrimSpace(replyText) == "" {
		replyText = "本轮处理完成，但没有生成可发送的文本回复。"
	}

	if err := client.replyStream(msg.ReqID, streamID, replyText, true); err != nil {
		b.logger.Error("Failed to send WeCom final reply", "error", err, "chat_id", msg.ChatID)
		b.reporter.Error("Failed to send final reply", "chat", humanChatLabel(msg), "error", err)
		return
	}
	b.reporter.ReplySent(msg, "final", len(replyText))
	b.logger.Info("Sent WeCom reply",
		"chat_id", msg.ChatID,
		"chat_type", msg.ChatType,
		"sender", msg.SenderID,
		"reply_chars", len(replyText),
	)
}

func (b *Bot) runPrompt(ctx context.Context, msg inboundMessage) string {
	sessionID, created, err := b.ensureSession(ctx, msg)
	if err != nil {
		b.logger.Error("Failed to resolve WeCom session", "error", err, "chat_id", msg.ChatID)
		b.reporter.Error("Failed to resolve session", "chat", humanChatLabel(msg), "error", err)
		return "会话初始化失败，请稍后重试。"
	}
	b.reporter.SessionMapped(sessionID, created, msg)

	before, err := b.ws.ListMessages(ctx, sessionID)
	if err != nil {
		b.logger.Error("Failed to read existing session messages", "error", err, "session_id", sessionID)
		return "读取会话历史失败，请稍后重试。"
	}
	existingAssistantIDs := assistantMessageIDs(before)

	prompt := buildPrompt(msg)
	b.reporter.AgentStarted(sessionID, msg)
	runStart := time.Now()
	monitorCtx, stopMonitor := context.WithCancel(ctx)
	tracker := newActivityTracker()
	go b.watchSessionActivity(monitorCtx, sessionID, tracker)
	defer stopMonitor()

	if err := b.ws.AgentRun(ctx, sessionID, prompt); err != nil {
		b.logger.Error("Failed to run Crush agent for WeCom message", "error", err, "session_id", sessionID)
		b.reporter.Error("Agent run failed", "session", shortID(sessionID), "error", err)
		return "处理消息时发生错误，请稍后重试。"
	}

	after, err := b.ws.ListMessages(ctx, sessionID)
	if err != nil {
		b.logger.Error("Failed to read updated session messages", "error", err, "session_id", sessionID)
		return "处理完成，但读取回复失败。"
	}

	replyText := latestAssistantText(after, existingAssistantIDs)
	if replyText != "" {
		b.reporter.AgentFinished(sessionID, time.Since(runStart), len(replyText))
		b.logger.Info("Finished WeCom agent run",
			"session_id", sessionID,
			"chat_id", msg.ChatID,
			"sender", msg.SenderID,
			"duration", time.Since(runStart),
			"tools_used", tracker.startedCount(),
			"reply_chars", len(replyText),
		)
		return replyText
	}
	replyText = latestAssistantText(after, nil)
	if replyText != "" {
		b.reporter.AgentFinished(sessionID, time.Since(runStart), len(replyText))
		b.logger.Info("Finished WeCom agent run",
			"session_id", sessionID,
			"chat_id", msg.ChatID,
			"sender", msg.SenderID,
			"duration", time.Since(runStart),
			"tools_used", tracker.startedCount(),
			"reply_chars", len(replyText),
		)
		return replyText
	}
	b.reporter.AgentFinished(sessionID, time.Since(runStart), 0)
	return "处理完成，但没有生成可发送的文本回复。"
}

func (b *Bot) ensureSession(ctx context.Context, msg inboundMessage) (string, bool, error) {
	if sessionID, ok := b.sessions.sessionID(msg.ChatKey); ok {
		if _, err := b.ws.GetSession(ctx, sessionID); err == nil {
			return sessionID, false, nil
		}
	}

	sess, err := b.ws.CreateSession(ctx, sessionTitle(msg))
	if err != nil {
		return "", false, err
	}
	if err := b.sessions.setSessionID(msg.ChatKey, sess.ID); err != nil {
		return "", false, err
	}
	return sess.ID, true, nil
}

func (b *Bot) chatLock(chatKey string) *sync.Mutex {
	b.locksMu.Lock()
	defer b.locksMu.Unlock()

	lock, ok := b.chatLocks[chatKey]
	if !ok {
		lock = &sync.Mutex{}
		b.chatLocks[chatKey] = lock
	}
	return lock
}

func sessionTitle(msg inboundMessage) string {
	switch msg.ChatType {
	case "group":
		return fmt.Sprintf("WeCom group %s", msg.ChatID)
	default:
		return fmt.Sprintf("WeCom user %s", msg.ChatID)
	}
}

func buildPrompt(msg inboundMessage) string {
	switch msg.ChatType {
	case "group":
		return fmt.Sprintf("[来自企业微信群聊 %s，发送者 %s]\n%s", msg.ChatID, msg.SenderID, msg.Prompt)
	default:
		return fmt.Sprintf("[来自企业微信用户 %s]\n%s", msg.SenderID, msg.Prompt)
	}
}

func assistantMessageIDs(messages []message.Message) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, msg := range messages {
		if msg.Role == message.Assistant {
			ids[msg.ID] = struct{}{}
		}
	}
	return ids
}

func latestAssistantText(messages []message.Message, ignore map[string]struct{}) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != message.Assistant {
			continue
		}
		if ignore != nil {
			if _, ok := ignore[msg.ID]; ok {
				continue
			}
		}
		if text := strings.TrimSpace(msg.Content().Text); text != "" {
			return text
		}
	}
	return ""
}

func (b *Bot) watchSessionActivity(ctx context.Context, sessionID string, tracker *activityTracker) {
	events := b.ws.App().Messages.Subscribe(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			msg := ev.Payload
			if msg.SessionID != sessionID || msg.Role != message.Assistant {
				continue
			}
			for _, toolCall := range msg.ToolCalls() {
				if tracker.markStarted(toolCall.ID) {
					b.reporter.ToolStarted(sessionID, toolCall.Name)
					b.logger.Info("WeCom tool started",
						"session_id", sessionID,
						"tool_call_id", toolCall.ID,
						"tool", toolCall.Name,
						"event_type", ev.Type,
					)
				}
				if toolCall.Finished && tracker.markFinished(toolCall.ID) {
					b.reporter.ToolFinished(sessionID, toolCall.Name)
					b.logger.Info("WeCom tool finished",
						"session_id", sessionID,
						"tool_call_id", toolCall.ID,
						"tool", toolCall.Name,
						"event_type", ev.Type,
					)
				}
			}
			for _, toolResult := range msg.ToolResults() {
				if toolResult.IsError && tracker.markErrored(toolResult.ToolCallID) {
					b.reporter.ToolErrored(sessionID, toolResult.Name)
					b.logger.Warn("WeCom tool returned error",
						"session_id", sessionID,
						"tool_call_id", toolResult.ToolCallID,
						"tool", toolResult.Name,
						"event_type", ev.Type,
					)
				}
			}
		}
	}
}

type activityTracker struct {
	mu       sync.Mutex
	started  map[string]struct{}
	finished map[string]struct{}
	errored  map[string]struct{}
}

func newActivityTracker() *activityTracker {
	return &activityTracker{
		started:  make(map[string]struct{}),
		finished: make(map[string]struct{}),
		errored:  make(map[string]struct{}),
	}
}

func (t *activityTracker) markStarted(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.started[id]; ok {
		return false
	}
	t.started[id] = struct{}{}
	return true
}

func (t *activityTracker) markFinished(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.finished[id]; ok {
		return false
	}
	t.finished[id] = struct{}{}
	return true
}

func (t *activityTracker) markErrored(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.errored[id]; ok {
		return false
	}
	t.errored[id] = struct{}{}
	return true
}

func (t *activityTracker) startedCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.started)
}

type wsClient struct {
	cfg      config.WeComConfig
	logger   *slog.Logger
	reporter *Reporter
	onFrame  func(frame)

	writeMu sync.Mutex
	connMu  sync.RWMutex
	conn    *websocket.Conn
}

func (c *wsClient) Run(ctx context.Context) error {
	var (
		authFailures int
		reconnects   int
	)

	for {
		runErr := c.runOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(runErr, errServerDisconnected) {
			return runErr
		}

		authFailure := false
		if runErr != nil {
			var authErr *authFailureError
			authFailure = errors.As(runErr, &authErr)
		}

		if authFailure {
			authFailures++
			if c.cfg.MaxAuthFailureAttempts >= 0 && authFailures > c.cfg.MaxAuthFailureAttempts {
				return fmt.Errorf("wecom authentication failed too many times: %w", runErr)
			}
			delay := backoffDelay(c.cfg.ReconnectIntervalSeconds, authFailures)
			c.logger.Warn("WeCom authentication failed, reconnecting", "delay", delay, "attempt", authFailures)
			c.reporter.AuthRetry(delay, authFailures, runErr)
			if err := waitForRetry(ctx, delay); err != nil {
				return nil
			}
			continue
		}

		authFailures = 0
		reconnects++
		if c.cfg.MaxReconnectAttempts >= 0 && reconnects > c.cfg.MaxReconnectAttempts {
			if runErr == nil {
				runErr = errors.New("wecom websocket disconnected")
			}
			return fmt.Errorf("wecom websocket reconnect attempts exhausted: %w", runErr)
		}
		delay := backoffDelay(c.cfg.ReconnectIntervalSeconds, reconnects)
		c.logger.Warn("WeCom connection lost, reconnecting", "delay", delay, "attempt", reconnects, "error", runErr)
		c.reporter.Reconnecting(delay, reconnects, runErr)
		if err := waitForRetry(ctx, delay); err != nil {
			return nil
		}
	}
}

func (c *wsClient) runOnce(ctx context.Context) error {
	c.reporter.Connecting(c.cfg.WebSocketURL)
	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 15 * time.Second,
	}
	conn, _, err := dialer.DialContext(ctx, c.cfg.WebSocketURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	c.reporter.Connected()
	c.logger.Info("Connected to WeCom WebSocket", "ws_url", c.cfg.WebSocketURL)

	c.setConn(conn)
	defer c.setConn(nil)

	if err := c.sendAuth(); err != nil {
		return err
	}

	frames := make(chan frame)
	readErrs := make(chan error, 1)
	go c.readLoop(conn, frames, readErrs)

	ticker := time.NewTicker(time.Duration(c.cfg.HeartbeatIntervalSeconds) * time.Second)
	defer ticker.Stop()

	missedPong := 0
	authenticated := false

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readErrs:
			return err
		case <-ticker.C:
			if missedPong >= 2 {
				return errors.New("wecom heartbeat acknowledgements timed out")
			}
			missedPong++
			if err := c.sendHeartbeat(); err != nil {
				return err
			}
		case f, ok := <-frames:
			if !ok {
				return errors.New("wecom websocket reader stopped")
			}
			switch {
			case f.Cmd == cmdCallback || f.Cmd == cmdEventCallback:
				if isDisconnectedEvent(f.Body) {
					c.reporter.ServerDisconnected()
					c.logger.Warn("WeCom server disconnected this client because another connection took over")
					return errServerDisconnected
				}
				if authenticated && c.onFrame != nil {
					c.onFrame(f)
				}
			case strings.HasPrefix(f.Headers.ReqID, cmdSubscribe):
				if f.ErrCode != 0 {
					return &authFailureError{err: fmt.Errorf("%s (code %d)", f.ErrMsg, f.ErrCode)}
				}
				authenticated = true
				missedPong = 0
				c.logger.Info("WeCom WebSocket authenticated")
				c.reporter.Authenticated()
			case strings.HasPrefix(f.Headers.ReqID, cmdHeartbeat):
				if f.ErrCode == 0 {
					missedPong = 0
				}
			default:
				if f.ErrCode != 0 {
					c.logger.Warn("WeCom acknowledged frame with error", "req_id", f.Headers.ReqID, "errcode", f.ErrCode, "errmsg", f.ErrMsg)
				}
			}
		}
	}
}

func (c *wsClient) readLoop(conn *websocket.Conn, frames chan<- frame, errs chan<- error) {
	defer close(frames)
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			errs <- err
			return
		}
		var f frame
		if err := json.Unmarshal(data, &f); err != nil {
			c.logger.Warn("Failed to decode WeCom frame", "error", err)
			continue
		}
		frames <- f
	}
}

func (c *wsClient) sendAuth() error {
	authBody := map[string]any{
		"bot_id": c.cfg.BotID,
		"secret": c.cfg.Secret,
	}
	return c.writeFrame(frame{
		Cmd:     cmdSubscribe,
		Headers: frameHeaders{ReqID: newReqID(cmdSubscribe)},
		Body:    mustMarshal(authBody),
	})
}

func (c *wsClient) sendHeartbeat() error {
	return c.writeFrame(frame{
		Cmd:     cmdHeartbeat,
		Headers: frameHeaders{ReqID: newReqID(cmdHeartbeat)},
	})
}

func (c *wsClient) replyStream(reqID, streamID, content string, finish bool) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}

	body := map[string]any{
		"msgtype": "stream",
		"stream": map[string]any{
			"id":      streamID,
			"finish":  finish,
			"content": content,
		},
	}
	return c.writeFrame(frame{
		Cmd:     cmdRespond,
		Headers: frameHeaders{ReqID: reqID},
		Body:    mustMarshal(body),
	})
}

func (c *wsClient) writeFrame(f frame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.connMu.RLock()
	defer c.connMu.RUnlock()
	if c.conn == nil {
		return errors.New("wecom websocket is not connected")
	}
	return c.conn.WriteJSON(f)
}

func (c *wsClient) setConn(conn *websocket.Conn) {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	c.conn = conn
}

func isDisconnectedEvent(raw json.RawMessage) bool {
	var body callbackBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return false
	}
	return body.MsgType == "event" && body.Event != nil && body.Event.EventType == "disconnected_event"
}

func mustMarshal(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func backoffDelay(baseSeconds, attempt int) time.Duration {
	if baseSeconds <= 0 {
		baseSeconds = 1
	}
	delay := time.Duration(baseSeconds) * time.Second
	for range attempt - 1 {
		if delay >= 30*time.Second {
			return 30 * time.Second
		}
		delay *= 2
	}
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type authFailureError struct {
	err error
}

func (e *authFailureError) Error() string {
	return "wecom authentication failed: " + e.err.Error()
}

func (e *authFailureError) Unwrap() error {
	return e.err
}
