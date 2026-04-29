package wecom

import (
	"io"
	"strings"
	"time"

	"charm.land/log/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/x/term"
)

type Reporter struct {
	logger *log.Logger
}

func NewReporter(w io.Writer, debug bool) *Reporter {
	logger := log.NewWithOptions(w, log.Options{
		ReportTimestamp: true,
	})
	logger.SetPrefix("wecom")
	if debug {
		logger.SetLevel(log.DebugLevel)
	} else {
		logger.SetLevel(log.InfoLevel)
	}
	if f, ok := w.(term.File); !ok || !term.IsTerminal(f.Fd()) {
		logger.SetColorProfile(colorprofile.NoTTY)
	}
	return &Reporter{logger: logger}
}

func (r *Reporter) Startup(cfg config.WeComConfig, statePath, logPath string) {
	r.logger.Info("Starting WeCom bot",
		"bot_id", maskSecret(cfg.BotID),
		"ws_url", cfg.WebSocketURL,
	)
	r.logger.Info("Using local state", "path", statePath)
	r.logger.Info("Writing logs", "path", logPath)
}

func (r *Reporter) Connecting(wsURL string) {
	r.logger.Info("Connecting", "ws_url", wsURL)
}

func (r *Reporter) Connected() {
	r.logger.Info("Connected")
}

func (r *Reporter) Authenticated() {
	r.logger.Info("Authenticated")
}

func (r *Reporter) Reconnecting(delay time.Duration, attempt int, err error) {
	r.logger.Warn("Reconnecting",
		"attempt", attempt,
		"delay", delay,
		"error", err,
	)
}

func (r *Reporter) AuthRetry(delay time.Duration, attempt int, err error) {
	r.logger.Warn("Authentication failed, retrying",
		"attempt", attempt,
		"delay", delay,
		"error", err,
	)
}

func (r *Reporter) ServerDisconnected() {
	r.logger.Warn("Disconnected by WeCom because another connection took over")
}

func (r *Reporter) Incoming(msg inboundMessage) {
	r.logger.Info("Incoming message",
		"chat", humanChatLabel(msg),
		"sender", msg.SenderID,
		"msg_id", shortID(msg.MsgID),
	)
}

func (r *Reporter) IgnoredEvent(msg inboundMessage) {
	r.logger.Debug("Ignoring event callback",
		"event_type", msg.EventType,
		"chat", humanChatLabel(msg),
	)
}

func (r *Reporter) SessionMapped(sessionID string, created bool, msg inboundMessage) {
	action := "Reusing session"
	if created {
		action = "Created session"
	}
	r.logger.Info(action,
		"session", shortID(sessionID),
		"chat", humanChatLabel(msg),
	)
}

func (r *Reporter) Thinking(msg inboundMessage) {
	r.logger.Debug("Sending thinking reply", "chat", humanChatLabel(msg))
}

func (r *Reporter) ToolStarted(sessionID string, toolName string) {
	r.logger.Info("Tool started", "session", shortID(sessionID), "tool", toolName)
}

func (r *Reporter) ToolFinished(sessionID string, toolName string) {
	r.logger.Info("Tool finished", "session", shortID(sessionID), "tool", toolName)
}

func (r *Reporter) ToolErrored(sessionID string, toolName string) {
	r.logger.Warn("Tool returned error", "session", shortID(sessionID), "tool", toolName)
}

func (r *Reporter) AgentStarted(sessionID string, msg inboundMessage) {
	r.logger.Info("Running agent",
		"session", shortID(sessionID),
		"chat", humanChatLabel(msg),
	)
}

func (r *Reporter) AgentFinished(sessionID string, duration time.Duration, replyLen int) {
	r.logger.Info("Agent finished",
		"session", shortID(sessionID),
		"duration", duration.Round(time.Millisecond),
		"reply_chars", replyLen,
	)
}

func (r *Reporter) ReplySent(msg inboundMessage, phase string, replyLen int) {
	r.logger.Info("Reply sent",
		"chat", humanChatLabel(msg),
		"phase", phase,
		"chars", replyLen,
	)
}

func (r *Reporter) Warn(msg any, keyvals ...any) {
	r.logger.Warn(msg, keyvals...)
}

func (r *Reporter) Error(msg any, keyvals ...any) {
	r.logger.Error(msg, keyvals...)
}

func humanChatLabel(msg inboundMessage) string {
	switch msg.ChatType {
	case "group":
		return "group:" + msg.ChatID
	default:
		return "user:" + msg.ChatID
	}
}

func shortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func maskSecret(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 4 {
		return "****"
	}
	return "****" + value[len(value)-4:]
}
