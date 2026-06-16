// Package tools defines the tools exposed to the model and dispatches calls.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tianlong-agent/internal/shell"
)

// Definitions returns the tool schemas advertised to the model.
func Definitions() []llmTool {
	return []llmTool{
		{
			Name: "run_shell",
			Description: "Run a shell command on the user's machine and return its " +
				"stdout, stderr and exit code. Commands run in a persistent working " +
				"directory. Prefer the host's native conventions (the OS and shell are " +
				"given in the system prompt). Combine steps with the shell's own " +
				"operators when you need them in one working directory.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"command": {
						"type": "string",
						"description": "The command line to execute."
					},
					"timeout_seconds": {
						"type": "integer",
						"description": "Optional max seconds before the command is killed (default 60)."
					}
				},
				"required": ["command"]
			}`),
		},
	}
}

// llmTool mirrors llm.Tool without importing it, to avoid a cycle; the agent
// converts these into llm.Tool values.
type llmTool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// Name/Description/Schema accessors so the agent package can map these over.
func (t llmTool) GetName() string            { return t.Name }
func (t llmTool) GetDescription() string     { return t.Description }
func (t llmTool) GetSchema() json.RawMessage { return t.InputSchema }

type runShellInput struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

// Approver decides if a command needs user confirmation and obtains it.
type Approver func(command string) (allowed bool, reason string)

// Result is the outcome of a tool call, carrying both the text fed back to the
// model and structured fields the UI uses to render status and output.
type Result struct {
	ModelText string // content returned to the model as the tool result
	ExitCode  int    // shell exit code (non-zero for any failure)
	Output    string // combined stdout/stderr for display
	IsError   bool   // whether this represents a failure
}

func errResult(msg string) Result {
	return Result{ModelText: msg, ExitCode: 1, Output: msg, IsError: true}
}

// Dispatch executes a tool call by name and returns its Result.
func Dispatch(ctx context.Context, exec *shell.Executor, approve Approver, name string, input json.RawMessage) Result {
	switch name {
	case "run_shell":
		var in runShellInput
		if err := json.Unmarshal(input, &in); err != nil {
			return errResult(fmt.Sprintf("invalid tool input: %v", err))
		}
		if in.Command == "" {
			return errResult("empty command")
		}
		if allowed, reason := approve(in.Command); !allowed {
			return errResult(fmt.Sprintf("Command rejected by user. %s", reason))
		}
		res := exec.Run(ctx, in.Command, time.Duration(in.TimeoutSeconds)*time.Second)
		return Result{
			ModelText: formatResult(res),
			ExitCode:  res.ExitCode,
			Output:    displayOutput(res),
			IsError:   res.ExitCode != 0,
		}
	default:
		return errResult(fmt.Sprintf("unknown tool: %s", name))
	}
}

// displayOutput merges stdout and stderr for on-screen rendering.
func displayOutput(res shell.Result) string {
	parts := make([]string, 0, 2)
	if s := strings.TrimRight(res.Stdout, "\n"); s != "" {
		parts = append(parts, s)
	}
	if s := strings.TrimRight(res.Stderr, "\n"); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, "\n")
}

func formatResult(res shell.Result) string {
	out := fmt.Sprintf("exit_code: %d\n", res.ExitCode)
	if res.TimedOut {
		out += "timed_out: true\n"
	}
	if res.Stdout != "" {
		out += "stdout:\n" + res.Stdout + "\n"
	}
	if res.Stderr != "" {
		out += "stderr:\n" + res.Stderr + "\n"
	}
	if res.Stdout == "" && res.Stderr == "" {
		out += "(no output)\n"
	}
	return out
}
