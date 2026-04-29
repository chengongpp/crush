package wecom

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseInboundMessageTextAndQuote(t *testing.T) {
	t.Parallel()

	body := map[string]any{
		"msgid":    "m1",
		"chatid":   "chat-1",
		"chattype": "group",
		"from": map[string]any{
			"userid": "alice",
		},
		"msgtype": "text",
		"text": map[string]any{
			"content": "hello",
		},
		"quote": map[string]any{
			"msgtype": "text",
			"text": map[string]any{
				"content": "quoted",
			},
		},
	}

	raw, err := json.Marshal(body)
	require.NoError(t, err)

	msg, err := parseInboundMessage(frame{
		Cmd:     cmdCallback,
		Headers: frameHeaders{ReqID: "req-1"},
		Body:    raw,
	})
	require.NoError(t, err)
	require.Equal(t, "group:chat-1", msg.ChatKey)
	require.Equal(t, "alice", msg.SenderID)
	require.Equal(t, "hello\n\n[引用消息]\nquoted", msg.Prompt)
}

func TestParseInboundMessageMixedVoiceAndUnsupported(t *testing.T) {
	t.Parallel()

	t.Run("mixed", func(t *testing.T) {
		t.Parallel()

		body := map[string]any{
			"msgid":    "m2",
			"chattype": "single",
			"from": map[string]any{
				"userid": "bob",
			},
			"msgtype": "mixed",
			"mixed": map[string]any{
				"msg_item": []map[string]any{
					{"msgtype": "text", "text": map[string]any{"content": "one"}},
					{"msgtype": "image"},
					{"msgtype": "text", "text": map[string]any{"content": "two"}},
				},
			},
		}

		raw, err := json.Marshal(body)
		require.NoError(t, err)

		msg, err := parseInboundMessage(frame{Cmd: cmdCallback, Headers: frameHeaders{ReqID: "req-2"}, Body: raw})
		require.NoError(t, err)
		require.Equal(t, "single:bob", msg.ChatKey)
		require.Equal(t, "one\n\ntwo", msg.Prompt)
	})

	t.Run("voice", func(t *testing.T) {
		t.Parallel()

		body := map[string]any{
			"msgid":    "m3",
			"chattype": "single",
			"from": map[string]any{
				"userid": "carol",
			},
			"msgtype": "voice",
			"voice": map[string]any{
				"content": "voice text",
			},
		}

		raw, err := json.Marshal(body)
		require.NoError(t, err)

		msg, err := parseInboundMessage(frame{Cmd: cmdCallback, Headers: frameHeaders{ReqID: "req-3"}, Body: raw})
		require.NoError(t, err)
		require.Equal(t, "voice text", msg.Prompt)
	})

	t.Run("unsupported", func(t *testing.T) {
		t.Parallel()

		body := map[string]any{
			"msgid":    "m4",
			"chattype": "single",
			"from": map[string]any{
				"userid": "dave",
			},
			"msgtype": "image",
		}

		raw, err := json.Marshal(body)
		require.NoError(t, err)

		msg, err := parseInboundMessage(frame{Cmd: cmdCallback, Headers: frameHeaders{ReqID: "req-4"}, Body: raw})
		require.NoError(t, err)
		require.Equal(t, "当前版本仅支持文本、混排文本和语音转写消息。", msg.UnsupportedReason)
	})
}
