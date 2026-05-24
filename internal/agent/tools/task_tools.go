package tools

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/tasks"
)

const (
	TaskCreateToolName = "task_create"
	TaskGetToolName    = "task_get"
	TaskListToolName   = "task_list"
	TaskUpdateToolName = "task_update"
)

//go:embed task_create.md
var taskCreateDescription string

//go:embed task_get.md
var taskGetDescription string

//go:embed task_list.md
var taskListDescription string

//go:embed task_update.md
var taskUpdateDescription string

// taskStore returns the task store for the current session.
func taskStore(tc *ToolContext) (*tasks.Store, error) {
	if tc == nil {
		return nil, fmt.Errorf("ToolContext not available")
	}
	dataDir := tc.Config.Config().Options.DataDirectory
	if dataDir == "" {
		dataDir = ".crush"
	}
	dir := filepath.Join(dataDir, "tasks")
	return tasks.NewStore(dir)
}

// TaskCreateParams is the input for TaskCreateTool.
type TaskCreateParams struct {
	Subject     string                 `json:"subject" description:"A brief, actionable title in imperative form"`
	Description string                 `json:"description" description:"What needs to be done"`
	ActiveForm  string                 `json:"activeForm,omitempty" description:"Present continuous form shown in spinner when in_progress"`
	Metadata    map[string]interface{} `json:"metadata,omitempty" description:"Arbitrary metadata to attach to the task"`
}

// TaskCreateResponseMetadata is returned by the task_create tool.
type TaskCreateResponseMetadata struct {
	TaskID  string `json:"task_id"`
	Subject string `json:"subject"`
	Status  string `json:"status"`
}

// NewTaskCreateTool creates a tool for creating structured tasks.
func NewTaskCreateTool() fantasy.AgentTool {
	return BuildTool(TaskCreateToolName, taskCreateDescription, SafetyClass{},
		func(ctx context.Context, call fantasy.ToolCall, params TaskCreateParams) (fantasy.ToolResponse, error) {
			if params.Subject == "" {
				return fantasy.NewTextErrorResponse("subject is required"), nil
			}
			if params.Description == "" {
				return fantasy.NewTextErrorResponse("description is required"), nil
			}

			tc := GetToolContext(ctx)
			store, err := taskStore(tc)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to access task store: %v", err)), nil
			}

			task, err := store.Create(params.Subject, params.Description, params.ActiveForm, params.Metadata)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to create task: %v", err)), nil
			}

			return fantasy.WithResponseMetadata(
				fantasy.NewTextResponse(fmt.Sprintf("Task #%s created: %s", task.ID, task.Subject)),
				TaskCreateResponseMetadata{TaskID: task.ID, Subject: task.Subject, Status: string(task.Status)},
			), nil
		})
}

// TaskGetParams is the input for TaskGetTool.
type TaskGetParams struct {
	TaskID string `json:"task_id" description:"The ID of the task to retrieve"`
}

// NewTaskGetTool creates a tool for retrieving a task by ID.
func NewTaskGetTool() fantasy.AgentTool {
	return BuildTool(TaskGetToolName, taskGetDescription, SafetyClass{ReadOnly: true},
		func(ctx context.Context, call fantasy.ToolCall, params TaskGetParams) (fantasy.ToolResponse, error) {
			if params.TaskID == "" {
				return fantasy.NewTextErrorResponse("task_id is required"), nil
			}

			tc := GetToolContext(ctx)
			store, err := taskStore(tc)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to access task store: %v", err)), nil
			}

			task, err := store.Get(params.TaskID)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to get task: %v", err)), nil
			}
			if task == nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("task #%s not found", params.TaskID)), nil
			}

			return fantasy.NewTextResponse(fmt.Sprintf(
				"Task #%s: %s\nStatus: %s\nDescription: %s\nBlocks: %v\nBlockedBy: %v",
				task.ID, task.Subject, task.Status, task.Description, task.Blocks, task.BlockedBy,
			)), nil
		})
}

// TaskListParams is the input for TaskListTool (no required params).
type TaskListParams struct{}

// NewTaskListTool creates a tool for listing all tasks.
func NewTaskListTool() fantasy.AgentTool {
	return BuildTool(TaskListToolName, taskListDescription, SafetyClass{ReadOnly: true},
		func(ctx context.Context, call fantasy.ToolCall, _ TaskListParams) (fantasy.ToolResponse, error) {
			tc := GetToolContext(ctx)
			store, err := taskStore(tc)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to access task store: %v", err)), nil
			}

			all, err := store.List()
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to list tasks: %v", err)), nil
			}
			if len(all) == 0 {
				return fantasy.NewTextResponse("No tasks."), nil
			}

			var out string
			for _, t := range all {
				statusMark := " "
				switch t.Status {
				case tasks.StatusInProgress:
					statusMark = "▶"
				case tasks.StatusCompleted:
					statusMark = "✓"
				}
				out += fmt.Sprintf("[%s] #%s %s\n", statusMark, t.ID, t.Subject)
			}
			return fantasy.NewTextResponse(out), nil
		})
}

// TaskUpdateParams is the input for TaskUpdateTool.
type TaskUpdateParams struct {
	TaskID       string                 `json:"task_id" description:"The ID of the task to update"`
	Subject      string                 `json:"subject,omitempty" description:"New subject for the task"`
	Description  string                 `json:"description,omitempty" description:"New description for the task"`
	ActiveForm   string                 `json:"activeForm,omitempty" description:"Present continuous form shown in spinner when in_progress"`
	Status       string                 `json:"status,omitempty" description:"New status: pending, in_progress, completed"`
	Owner        string                 `json:"owner,omitempty" description:"New owner for the task"`
	AddBlocks    []string               `json:"addBlocks,omitempty" description:"Task IDs that this task blocks"`
	AddBlockedBy []string               `json:"addBlockedBy,omitempty" description:"Task IDs that block this task"`
	Metadata     map[string]interface{} `json:"metadata,omitempty" description:"Metadata keys to merge into the task (set a key to null to delete it)"`
}

// NewTaskUpdateTool creates a tool for updating task status and fields.
func NewTaskUpdateTool() fantasy.AgentTool {
	return BuildTool(TaskUpdateToolName, taskUpdateDescription, SafetyClass{},
		func(ctx context.Context, call fantasy.ToolCall, params TaskUpdateParams) (fantasy.ToolResponse, error) {
			if params.TaskID == "" {
				return fantasy.NewTextErrorResponse("task_id is required"), nil
			}

			tc := GetToolContext(ctx)
			store, err := taskStore(tc)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to access task store: %v", err)), nil
			}

			task, err := store.Update(params.TaskID, func(t *tasks.Task) {
				if params.Subject != "" {
					t.Subject = params.Subject
				}
				if params.Description != "" {
					t.Description = params.Description
				}
				if params.ActiveForm != "" {
					t.ActiveForm = params.ActiveForm
				}
				if params.Status != "" {
					t.Status = tasks.Status(params.Status)
				}
				if params.Owner != "" {
					t.Owner = params.Owner
				}
				for _, b := range params.AddBlocks {
					t.Blocks = appendIfAbsent(t.Blocks, b)
				}
				for _, b := range params.AddBlockedBy {
					t.BlockedBy = appendIfAbsent(t.BlockedBy, b)
				}
				if params.Metadata != nil {
					if t.Metadata == nil {
						t.Metadata = make(map[string]interface{})
					}
					for k, v := range params.Metadata {
						if v == nil {
							delete(t.Metadata, k)
						} else {
							t.Metadata[k] = v
						}
					}
				}
			})
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to update task: %v", err)), nil
			}

			return fantasy.NewTextResponse(fmt.Sprintf("Task #%s updated (status: %s)", task.ID, task.Status)), nil
		})
}

func appendIfAbsent(slice []string, s string) []string {
	for _, v := range slice {
		if v == s {
			return slice
		}
	}
	return append(slice, s)
}
