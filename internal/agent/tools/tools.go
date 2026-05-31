package tools

import (
	"bytes"
	"context"
	"html/template"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"charm.land/fantasy"
)

type (
	sessionIDContextKey string
	messageIDContextKey string
	supportsImagesKey   string
	modelNameKey        string
)

const (
	// SessionIDContextKey is the key for the session ID in the context.
	SessionIDContextKey sessionIDContextKey = "session_id"
	// MessageIDContextKey is the key for the message ID in the context.
	MessageIDContextKey messageIDContextKey = "message_id"
	// SupportsImagesContextKey is the key for the model's image support capability.
	SupportsImagesContextKey supportsImagesKey = "supports_images"
	// ModelNameContextKey is the key for the model name in the context.
	ModelNameContextKey modelNameKey = "model_name"
)

// getContextValue is a generic helper that retrieves a typed value from context.
// If the value is not found or has the wrong type, it returns the default value.
func getContextValue[T any](ctx context.Context, key any, defaultValue T) T {
	value := ctx.Value(key)
	if value == nil {
		return defaultValue
	}
	if typedValue, ok := value.(T); ok {
		return typedValue
	}
	return defaultValue
}

// GetSessionFromContext retrieves the session ID from the context.
func GetSessionFromContext(ctx context.Context) string {
	return getContextValue(ctx, SessionIDContextKey, "")
}

// GetMessageFromContext retrieves the message ID from the context.
func GetMessageFromContext(ctx context.Context) string {
	return getContextValue(ctx, MessageIDContextKey, "")
}

// GetSupportsImagesFromContext retrieves whether the model supports images from the context.
func GetSupportsImagesFromContext(ctx context.Context) bool {
	return getContextValue(ctx, SupportsImagesContextKey, false)
}

// GetModelNameFromContext retrieves the model name from the context.
func GetModelNameFromContext(ctx context.Context) string {
	return getContextValue(ctx, ModelNameContextKey, "")
}

// NewPermissionDeniedResponse returns a tool response indicating the user
// denied permission, with StopTurn set so the agent loop does not retry.
func NewPermissionDeniedResponse() fantasy.ToolResponse {
	resp := fantasy.NewTextErrorResponse("User denied permission")
	resp.StopTurn = true
	return resp
}

// WrapInSystemReminder wraps content in <system-reminder> tags, matching
// the claude-code convention for system-injected context that should not
// be confused with user input.
func WrapInSystemReminder(content string) string {
	return "<system-reminder>\n" + content + "\n</system-reminder>"
}

// SetParallel sets the Parallel flag on a tool if the underlying
// implementation supports it (i.e., was created with NewAgentTool or
// NewParallelAgentTool). Wrapped tools (e.g. hookedTool) delegate to
// their inner tool. This is a best-effort operation.
func SetParallel(tool fantasy.AgentTool, parallel bool) {
	type parallelSetter interface {
		SetParallel(bool)
	}
	if setter, ok := tool.(parallelSetter); ok {
		setter.SetParallel(parallel)
	}
}

// FirstLineDescription returns just the first non-empty line from the embedded
// markdown description. The full description can be used by setting
// CRUSH_SHORT_TOOL_DESCRIPTIONS=0.
func FirstLineDescription(content []byte) string {
	if !testing.Testing() {
		if v, err := strconv.ParseBool(os.Getenv("CRUSH_SHORT_TOOL_DESCRIPTIONS")); err == nil && !v {
			return strings.TrimSpace(string(content))
		}
	}
	firstLine, _, _ := bytes.Cut(content, []byte("\n"))
	return strings.TrimSpace(string(firstLine))
}

// ghAvailable indicates whether the `gh` CLI is available on PATH.
var ghAvailable = func() bool {
	if testing.Testing() {
		return false
	}
	_, err := exec.LookPath("gh")
	return err == nil
}()

// toolDescriptionData is the common data structure for tool description templates.
type toolDescriptionData struct {
	GhAvailable bool
}

// renderToolDescription renders a tool description template with the given data.
func renderToolDescription(tmpl *template.Template) string {
	data := toolDescriptionData{
		GhAvailable: ghAvailable,
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		panic("failed to execute tool description template: " + err.Error())
	}
	return out.String()
}

// renderTemplate renders a Go template with the given data.
func renderTemplate(tmpl *template.Template, data any) string {
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		panic("failed to execute tool description template: " + err.Error())
	}
	return out.String()
}
