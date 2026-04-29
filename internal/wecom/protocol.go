package wecom

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	cmdSubscribe     = "aibot_subscribe"
	cmdHeartbeat     = "ping"
	cmdRespond       = "aibot_respond_msg"
	cmdCallback      = "aibot_msg_callback"
	cmdEventCallback = "aibot_event_callback"
)

type frame struct {
	Cmd     string          `json:"cmd,omitempty"`
	Headers frameHeaders    `json:"headers"`
	Body    json.RawMessage `json:"body,omitempty"`
	ErrCode int             `json:"errcode,omitempty"`
	ErrMsg  string          `json:"errmsg,omitempty"`
}

type frameHeaders struct {
	ReqID string `json:"req_id"`
}

type callbackBody struct {
	MsgID    string `json:"msgid"`
	AIBotID  string `json:"aibotid"`
	ChatID   string `json:"chatid,omitempty"`
	ChatType string `json:"chattype"`
	From     struct {
		UserID string `json:"userid"`
		CorpID string `json:"corpid,omitempty"`
		ChatID string `json:"chat_id,omitempty"`
	} `json:"from"`
	MsgType string `json:"msgtype"`
	Text    *struct {
		Content string `json:"content"`
	} `json:"text,omitempty"`
	Voice *struct {
		Content string `json:"content"`
	} `json:"voice,omitempty"`
	Mixed *struct {
		Items []struct {
			MsgType string `json:"msgtype"`
			Text    *struct {
				Content string `json:"content"`
			} `json:"text,omitempty"`
		} `json:"msg_item"`
	} `json:"mixed,omitempty"`
	Quote *struct {
		MsgType string `json:"msgtype"`
		Text    *struct {
			Content string `json:"content"`
		} `json:"text,omitempty"`
		Voice *struct {
			Content string `json:"content"`
		} `json:"voice,omitempty"`
	} `json:"quote,omitempty"`
	Event *struct {
		EventType string `json:"eventtype,omitempty"`
	} `json:"event,omitempty"`
}

type inboundMessage struct {
	ReqID             string
	MsgID             string
	ChatKey           string
	ChatID            string
	ChatType          string
	SenderID          string
	Prompt            string
	UnsupportedReason string
	IsEvent           bool
	EventType         string
}

func parseInboundMessage(f frame) (inboundMessage, error) {
	if f.Cmd != cmdCallback && f.Cmd != cmdEventCallback {
		return inboundMessage{}, fmt.Errorf("unsupported frame command %q", f.Cmd)
	}

	var body callbackBody
	if err := json.Unmarshal(f.Body, &body); err != nil {
		return inboundMessage{}, fmt.Errorf("failed to decode callback body: %w", err)
	}

	chatID := strings.TrimSpace(body.ChatID)
	if chatID == "" {
		chatID = strings.TrimSpace(body.From.ChatID)
	}
	if chatID == "" {
		chatID = strings.TrimSpace(body.From.UserID)
	}
	chatType := strings.TrimSpace(body.ChatType)
	if chatType == "" {
		chatType = "single"
	}

	msg := inboundMessage{
		ReqID:    f.Headers.ReqID,
		MsgID:    body.MsgID,
		ChatID:   chatID,
		ChatType: chatType,
		SenderID: strings.TrimSpace(body.From.UserID),
		IsEvent:  f.Cmd == cmdEventCallback || body.MsgType == "event",
	}
	if body.Event != nil {
		msg.EventType = strings.TrimSpace(body.Event.EventType)
	}
	msg.ChatKey = buildChatKey(msg.ChatType, msg.ChatID)

	if msg.IsEvent {
		return msg, nil
	}

	var parts []string
	switch body.MsgType {
	case "text":
		if body.Text != nil {
			parts = append(parts, strings.TrimSpace(body.Text.Content))
		}
	case "voice":
		if body.Voice != nil {
			parts = append(parts, strings.TrimSpace(body.Voice.Content))
		}
	case "mixed":
		if body.Mixed != nil {
			for _, item := range body.Mixed.Items {
				if item.MsgType == "text" && item.Text != nil {
					parts = append(parts, strings.TrimSpace(item.Text.Content))
				}
			}
		}
	default:
		msg.UnsupportedReason = "当前版本仅支持文本、混排文本和语音转写消息。"
		return msg, nil
	}

	if body.Quote != nil {
		quoted := quotedText(body.Quote)
		if quoted != "" {
			parts = append(parts, "[引用消息]\n"+quoted)
		}
	}

	msg.Prompt = strings.TrimSpace(strings.Join(filterEmpty(parts), "\n\n"))
	if msg.Prompt == "" && msg.UnsupportedReason == "" {
		msg.UnsupportedReason = "当前消息没有可供处理的文本内容。"
	}
	return msg, nil
}

func quotedText(quote *struct {
	MsgType string `json:"msgtype"`
	Text    *struct {
		Content string `json:"content"`
	} `json:"text,omitempty"`
	Voice *struct {
		Content string `json:"content"`
	} `json:"voice,omitempty"`
}) string {
	switch quote.MsgType {
	case "text":
		if quote.Text != nil {
			return strings.TrimSpace(quote.Text.Content)
		}
	case "voice":
		if quote.Voice != nil {
			return strings.TrimSpace(quote.Voice.Content)
		}
	}
	return ""
}

func buildChatKey(chatType, chatID string) string {
	return fmt.Sprintf("%s:%s", strings.TrimSpace(chatType), strings.TrimSpace(chatID))
}

func newReqID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func filterEmpty(parts []string) []string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			filtered = append(filtered, part)
		}
	}
	return filtered
}
