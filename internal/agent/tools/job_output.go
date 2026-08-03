package tools

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/shell"
)

const (
	JobOutputToolName = "job_output"

	// defaultJobOutputTimeout bounds how long a wait=true call blocks before
	// returning with whatever output is available, so the agent loop is never
	// stuck behind a long-running background shell.
	defaultJobOutputTimeout = 30 * time.Second
)

//go:embed job_output.md
var jobOutputDescription string

type JobOutputParams struct {
	ShellID string `json:"shell_id" description:"The ID of the background shell to retrieve output from"`
	Wait    bool   `json:"wait" description:"If true, block until the background shell completes before returning output"`
	// TimeoutSeconds bounds how long a wait=true call blocks before
	// returning with the output available so far. Defaults to 30.
	TimeoutSeconds *int `json:"timeout_seconds,omitempty" description:"Maximum seconds to wait for output when wait is true. Defaults to 30."`
}

type JobOutputResponseMetadata struct {
	ShellID          string `json:"shell_id"`
	Command          string `json:"command"`
	Description      string `json:"description"`
	Done             bool   `json:"done"`
	WorkingDirectory string `json:"working_directory"`
}

func NewJobOutputTool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
		JobOutputToolName,
		jobOutputDescription,
		func(ctx context.Context, params JobOutputParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.ShellID == "" {
				return fantasy.NewTextErrorResponse("missing shell_id"), nil
			}

			bgManager := shell.GetBackgroundShellManager()
			bgShell, ok := bgManager.Get(params.ShellID)
			if !ok {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("background shell not found: %s", params.ShellID)), nil
			}

			var timedOutNote string
			if params.Wait {
				waitCtx := ctx
				timeout := defaultJobOutputTimeout
				if params.TimeoutSeconds != nil && *params.TimeoutSeconds > 0 {
					timeout = time.Duration(*params.TimeoutSeconds) * time.Second
				}
				var cancel context.CancelFunc
				waitCtx, cancel = context.WithTimeout(ctx, timeout)
				waited := bgShell.WaitContext(waitCtx)
				cancel()
				if !waited && errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
					timedOutNote = fmt.Sprintf("Timed out waiting for the command to finish after %d seconds", int(timeout.Seconds()))
				}
			}

			stdout, stderr, done, err := bgShell.GetOutput()

			var outputParts []string
			if stdout != "" {
				outputParts = append(outputParts, stdout)
			}
			if stderr != "" {
				outputParts = append(outputParts, stderr)
			}
			if timedOutNote != "" {
				outputParts = append(outputParts, timedOutNote)
			}

			status := "running"
			if done {
				status = "completed"
				if err != nil {
					exitCode := shell.ExitCode(err)
					if exitCode != 0 {
						outputParts = append(outputParts, fmt.Sprintf("Exit code %d", exitCode))
					}
				}
			}

			output := strings.Join(outputParts, "\n")
			output = TruncateOutput(output)

			metadata := JobOutputResponseMetadata{
				ShellID:          params.ShellID,
				Command:          bgShell.Command,
				Description:      bgShell.Description,
				Done:             done,
				WorkingDirectory: bgShell.WorkingDir,
			}

			if output == "" {
				output = BashNoOutput
			}

			result := fmt.Sprintf("Status: %s\n\n%s", status, output)
			return fantasy.WithResponseMetadata(fantasy.NewTextResponse(result), metadata), nil
		},
	)
}
