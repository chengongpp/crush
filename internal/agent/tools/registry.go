package tools

import (
	"context"

	"charm.land/fantasy"
)

// SafetyClass describes the safety properties of a tool, inspired by
// claude-code's isReadOnly / isConcurrencySafe / isDestructive flags.
type SafetyClass struct {
	// ReadOnly tools have no side effects — they only observe state. The
	// fantasy library runs these in parallel.
	ReadOnly bool

	// ConcurrencySafe tools may write, but to independent resources so
	// they can run concurrently with other concurrency-safe tools.
	ConcurrencySafe bool

	// Destructive tools perform irreversible operations (delete,
	// overwrite files, send data). Used for security classification.
	Destructive bool
}

// BuildTool creates a fantasy.AgentTool from a typed function, applying
// safety classification (inspired by claude-code's buildTool):
//
//   - Parallel is set to true when Safety.ReadOnly or
//     Safety.ConcurrencySafe is set.
//   - The function receives context.Context (carrying *ToolContext) for
//     clean dependency injection.
func BuildTool[T any](
	name, description string,
	safety SafetyClass,
	fn func(ctx context.Context, call fantasy.ToolCall, input T) (fantasy.ToolResponse, error),
) fantasy.AgentTool {
	return BuildToolFromDef(ToolDef{
		Name:        name,
		Description: description,
		Safety:      safety,
	}, fn)
}

// BuildToolFromDef creates a fantasy.AgentTool from a ToolDef with optional
// ValidateInput / CheckPermissions hooks, safety classification, and
// metadata (aliases, search hint).
func BuildToolFromDef[T any](
	def ToolDef,
	fn func(ctx context.Context, call fantasy.ToolCall, input T) (fantasy.ToolResponse, error),
) fantasy.AgentTool {
	// Extract typed validators.
	var validateFn func(ctx context.Context, tc *ToolContext, call fantasy.ToolCall, input T) error
	if v, ok := def.ValidateInput.(func(ctx context.Context, tc *ToolContext, call fantasy.ToolCall, input T) error); ok {
		validateFn = v
	}

	run := func(ctx context.Context, input T, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
		// Run input validation if configured and ToolContext is available.
		if validateFn != nil {
			if tc := GetToolContext(ctx); tc != nil {
				if err := validateFn(ctx, tc, call, input); err != nil {
					return fantasy.NewTextErrorResponse(err.Error()), nil
				}
			}
		}

		return fn(ctx, call, input)
	}

	parallel := def.Safety.ReadOnly || def.Safety.ConcurrencySafe
	if parallel {
		return fantasy.NewParallelAgentTool(def.Name, def.Description, run)
	}
	return fantasy.NewAgentTool(def.Name, def.Description, run)
}

// ValidateInputFn is called before tool execution to validate inputs.
// Returns a user-facing error message or "" if valid.
type ValidateInputFn[T any] func(ctx context.Context, tc *ToolContext, call fantasy.ToolCall, input T) error

// CheckPermissionsFn is called after validation to apply tool-specific
// permission logic. Returns allow/deny/ask with optional reason and
// modified input.
type CheckPermissionsFn[T any] func(ctx context.Context, tc *ToolContext, call fantasy.ToolCall, input T) PermissionResult

// PermissionResult is the outcome of a permission check.
type PermissionResult struct {
	Behavior     string // "allow", "deny", "ask", "passthrough"
	Reason       string
	UpdatedInput string // JSON-encoded updated tool input (for deny rules to patch)
}

// ToolDef is a complete tool definition passed to BuildTool. It carries
// metadata (aliases, search hint) that lives alongside the tool but
// outside the fantasy AgentTool interface.
type ToolDef struct {
	Name             string
	Description      string
	Aliases          []string
	SearchHint       string
	Safety           SafetyClass
	ValidateInput    any // func(ctx, tc, call, T) error — called before permissions
	CheckPermissions any // func(ctx, tc, call, T) PermissionResult — tool-specific rules
}

// ToolRegistry collects all tools known to the agent. It provides
// discovery, lookup, and safety classification, inspired by
// claude-code's getAllBaseTools() + assembleToolPool().
type ToolRegistry struct {
	tools   []fantasy.AgentTool
	aliases map[string]string // alias → canonical name
}

// NewToolRegistry creates a registry from the given tools.
func NewToolRegistry(tools []fantasy.AgentTool) *ToolRegistry {
	return &ToolRegistry{
		tools:   tools,
		aliases: make(map[string]string),
	}
}

// RegisterAlias records an alias for a tool name.
func (r *ToolRegistry) RegisterAlias(name, alias string) {
	r.aliases[alias] = name
}

// RegisterAliases records multiple aliases for a tool name.
func (r *ToolRegistry) RegisterAliases(name string, aliases []string) {
	for _, a := range aliases {
		r.aliases[a] = name
	}
}

// All returns all registered tools.
func (r *ToolRegistry) All() []fantasy.AgentTool {
	return r.tools
}

// Find returns the tool with the given name (or alias), or nil.
func (r *ToolRegistry) Find(name string) fantasy.AgentTool {
	// Resolve alias first.
	canon := name
	if n, ok := r.aliases[name]; ok {
		canon = n
	}
	for _, t := range r.tools {
		if t.Info().Name == canon {
			return t
		}
	}
	return nil
}

// Names returns the names of all registered tools.
func (r *ToolRegistry) Names() []string {
	names := make([]string, len(r.tools))
	for i, t := range r.tools {
		names[i] = t.Info().Name
	}
	return names
}

// Add appends tools to the registry.
func (r *ToolRegistry) Add(tools ...fantasy.AgentTool) {
	r.tools = append(r.tools, tools...)
}

// FilterByNames returns tools whose names appear in allowed. If allowed is
// empty, all tools are returned.
func (r *ToolRegistry) FilterByNames(allowed []string) []fantasy.AgentTool {
	if len(allowed) == 0 {
		return r.tools
	}
	allow := make(map[string]bool, len(allowed))
	for _, n := range allowed {
		allow[n] = true
	}
	var out []fantasy.AgentTool
	for _, t := range r.tools {
		if allow[t.Info().Name] {
			out = append(out, t)
		}
	}
	return out
}

// RejectByNames returns tools whose names are NOT in rejected.
func (r *ToolRegistry) RejectByNames(rejected []string) []fantasy.AgentTool {
	if len(rejected) == 0 {
		return r.tools
	}
	rej := make(map[string]bool, len(rejected))
	for _, n := range rejected {
		rej[n] = true
	}
	var out []fantasy.AgentTool
	for _, t := range r.tools {
		if !rej[t.Info().Name] {
			out = append(out, t)
		}
	}
	return out
}
