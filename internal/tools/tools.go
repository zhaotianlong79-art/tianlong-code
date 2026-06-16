// Package tools defines the tools exposed to the model and dispatches calls.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tianlong-agent/internal/approval"
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
		{
			Name: "read_file",
			Description: "Read a UTF-8 text file and return its contents. Use this " +
				"instead of `cat` when you need to inspect a file before editing it.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "File path (absolute, or relative to the working directory)."}
				},
				"required": ["path"]
			}`),
		},
		{
			Name: "write_file",
			Description: "Create or overwrite a file with the given content, creating " +
				"parent directories as needed. ALWAYS prefer this over shell here-docs " +
				"(cat <<EOF), echo redirection or sed for creating/replacing files — it " +
				"avoids all quoting and escaping problems, including with non-ASCII text.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "File path (absolute, or relative to the working directory)."},
					"content": {"type": "string", "description": "The full file content to write."}
				},
				"required": ["path", "content"]
			}`),
		},
		{
			Name: "edit_file",
			Description: "Replace an exact substring in a file. ALWAYS prefer this over " +
				"sed/awk for editing files — no escaping or dialect issues. old_string " +
				"must match exactly and be unique unless replace_all is true.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "File path (absolute, or relative to the working directory)."},
					"old_string": {"type": "string", "description": "Exact text to replace."},
					"new_string": {"type": "string", "description": "Replacement text."},
					"replace_all": {"type": "boolean", "description": "Replace every occurrence instead of requiring a unique match."}
				},
				"required": ["path", "old_string", "new_string"]
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

type readFileInput struct {
	Path string `json:"path"`
}

type writeFileInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type editFileInput struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

// Approver decides whether an action may run, prompting the user when needed.
// readOnly hints that the action has no side effects (and is safe to auto-run).
type Approver func(action string, readOnly bool) (allowed bool, reason string)

// Result is the outcome of a tool call, carrying both the text fed back to the
// model and structured fields the UI uses to render status and output.
type Result struct {
	ModelText string // content returned to the model as the tool result
	Status    string // short status shown beside the colored dot (e.g. "exit 0", "wrote 4 lines")
	Output    string // optional body shown under a white dot (shell output); empty for file tools
	IsError   bool   // whether this represents a failure
}

func errResult(msg string) Result {
	return Result{ModelText: msg, Status: msg, IsError: true}
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
		if allowed, reason := approve(in.Command, approval.IsReadOnly(in.Command)); !allowed {
			return errResult(fmt.Sprintf("Command rejected by user. %s", reason))
		}
		res := exec.Run(ctx, in.Command, time.Duration(in.TimeoutSeconds)*time.Second)
		return Result{
			ModelText: formatResult(res),
			Status:    fmt.Sprintf("exit %d", res.ExitCode),
			Output:    displayOutput(res),
			IsError:   res.ExitCode != 0,
		}

	case "read_file":
		var in readFileInput
		if err := json.Unmarshal(input, &in); err != nil {
			return errResult(fmt.Sprintf("invalid tool input: %v", err))
		}
		if in.Path == "" {
			return errResult("empty path")
		}
		if allowed, reason := approve("read "+in.Path, true); !allowed {
			return errResult(fmt.Sprintf("Read rejected by user. %s", reason))
		}
		return readFile(resolvePath(exec, in.Path))

	case "write_file":
		var in writeFileInput
		if err := json.Unmarshal(input, &in); err != nil {
			return errResult(fmt.Sprintf("invalid tool input: %v", err))
		}
		if in.Path == "" {
			return errResult("empty path")
		}
		if allowed, reason := approve("write "+in.Path, false); !allowed {
			return errResult(fmt.Sprintf("Write rejected by user. %s", reason))
		}
		return writeFile(resolvePath(exec, in.Path), in.Content)

	case "edit_file":
		var in editFileInput
		if err := json.Unmarshal(input, &in); err != nil {
			return errResult(fmt.Sprintf("invalid tool input: %v", err))
		}
		if in.Path == "" {
			return errResult("empty path")
		}
		if allowed, reason := approve("edit "+in.Path, false); !allowed {
			return errResult(fmt.Sprintf("Edit rejected by user. %s", reason))
		}
		return editFile(resolvePath(exec, in.Path), in.OldString, in.NewString, in.ReplaceAll)

	default:
		return errResult(fmt.Sprintf("unknown tool: %s", name))
	}
}

// maxReadBytes caps read_file output so a huge file can't blow up the context.
const maxReadBytes = 64 * 1024

// resolvePath makes a relative path absolute against the executor's cwd, so
// file tools operate in the same directory as shell commands.
func resolvePath(exec *shell.Executor, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(exec.Cwd(), path)
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// plural formats a count with a singular/plural unit ("1 line" / "3 lines").
func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func readFile(path string) Result {
	data, err := os.ReadFile(path)
	if err != nil {
		return errResult(fmt.Sprintf("read_file: %v", err))
	}
	content := string(data)
	model := content
	if len(content) > maxReadBytes {
		model = content[:maxReadBytes] + "\n... [truncated]"
	}
	return Result{ModelText: model, Status: fmt.Sprintf("read %s (%d bytes)", plural(countLines(content), "line"), len(data))}
}

func writeFile(path, content string) Result {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return errResult(fmt.Sprintf("write_file: %v", err))
		}
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return errResult(fmt.Sprintf("write_file: %v", err))
	}
	lines := countLines(content)
	return Result{
		ModelText: fmt.Sprintf("wrote %s (%d bytes, %d lines)", path, len(content), lines),
		Status:    fmt.Sprintf("wrote %s (%d bytes)", plural(lines, "line"), len(content)),
	}
}

func editFile(path, oldStr, newStr string, replaceAll bool) Result {
	data, err := os.ReadFile(path)
	if err != nil {
		return errResult(fmt.Sprintf("edit_file: %v", err))
	}
	content := string(data)
	n := strings.Count(content, oldStr)
	switch {
	case oldStr == "":
		return errResult("edit_file: old_string must not be empty")
	case n == 0:
		return errResult("edit_file: old_string not found in file")
	case n > 1 && !replaceAll:
		return errResult(fmt.Sprintf("edit_file: old_string is not unique (%d matches); set replace_all or add more context", n))
	}
	replaced := 1
	if replaceAll {
		content = strings.ReplaceAll(content, oldStr, newStr)
		replaced = n
	} else {
		content = strings.Replace(content, oldStr, newStr, 1)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return errResult(fmt.Sprintf("edit_file: %v", err))
	}
	return Result{
		ModelText: fmt.Sprintf("edited %s (%s)", path, plural(replaced, "replacement")),
		Status:    plural(replaced, "replacement"),
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
