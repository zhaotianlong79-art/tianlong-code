// Package agent wires the model, tools and shell into a ReAct-style loop.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"

	"tianlong-agent/internal/llm"
	"tianlong-agent/internal/shell"
	"tianlong-agent/internal/tools"
)

// maxToolIterations bounds a single user turn so a confused model can't loop
// forever calling tools.
const maxToolIterations = 25

// Printer reports progress to the UI layer.
type Printer interface {
	AssistantDelta(text string)                  // a chunk of streamed assistant text
	AssistantDone()                              // the streamed text turn finished
	ToolStart(name, command string)              // a tool about to run
	ToolEnd(status, output string, isError bool) // a tool finished
	Notice(text string)                          // an out-of-band status note
}

// Agent holds conversation state across turns.
type Agent struct {
	client    llm.Provider
	exec      *shell.Executor
	approve   tools.Approver
	printer   Printer
	tools     []llm.Tool
	messages  []llm.Message
	tokBudget int // estimated input-token ceiling for history (0 = unbounded)
}

// New constructs an agent. approve gates command execution; printer renders
// output. contextWindow and maxOutputTokens bound history so that input plus
// reserved output stays within the model's context (0 contextWindow disables
// trimming).
func New(client llm.Provider, exec *shell.Executor, approve tools.Approver, printer Printer, contextWindow, maxOutputTokens int) *Agent {
	var defs []llm.Tool
	for _, t := range tools.Definitions() {
		defs = append(defs, llm.Tool{
			Name:        t.GetName(),
			Description: t.GetDescription(),
			Parameters:  t.GetSchema(),
		})
	}
	budget := 0
	if contextWindow > 0 {
		budget = contextWindow - maxOutputTokens - trimMargin
	}
	return &Agent{
		client:    client,
		exec:      exec,
		approve:   approve,
		printer:   printer,
		tools:     defs,
		tokBudget: budget,
		messages: []llm.Message{
			{Role: llm.RoleSystem, Content: buildSystemPrompt(exec)},
		},
	}
}

func buildSystemPrompt(exec *shell.Executor) string {
	return fmt.Sprintf(`You are an experienced software engineer working alongside the user from inside their terminal. You can run shell commands and read/write files on their machine.

Environment:
- %s
- Host OS family: %s

How you work:
- Match effort to the task. For small or unambiguous requests, just do them. For larger or ambiguous ones, work like a careful engineer: state your plan in a sentence or two before you start, then carry it out — keep going unless the user steps in.
- For vague or broad requests ("optimize this", "refactor it", "clean it up"), do NOT dive into sweeping changes. First state a concrete, narrowly-scoped interpretation — what specifically you will do and how much you will touch — then proceed on that. Prefer the smallest useful change; never undertake a large refactor unless explicitly asked.
- Explore before you change. Read the relevant files and follow existing conventions; match the surrounding style. Never guess at names, paths or APIs — verify them first.
- After any code change, verify it: run the project's native build and tests (for Go, `+"`go build ./... && go test ./...`"+`), and add or update tests for non-trivial logic. If anything breaks, fix it; if you cannot fix it after a couple of attempts, stop and report honestly — never leave the code failing to build or tests broken.
- You do not manage version control. Never run git commit, push or similar — leave that to the user; you may note when the work looks ready to commit.

Tool use:
- Use run_shell to inspect and run things, in the host shell's native syntax.
- To create, overwrite or edit files, ALWAYS use write_file / edit_file / read_file — never shell here-docs (cat <<EOF), echo redirection or sed, which cause quoting/escaping errors, especially with non-ASCII text.
- Take one concrete step at a time and read each result before the next. Keep commands minimal; avoid destructive operations unless clearly asked.
- When done, summarize what you did in plain language without more tool calls.`,
		exec.Describe(), runtime.GOOS)
}

// Reset clears the conversation, keeping only the system prompt.
func (a *Agent) Reset() {
	if len(a.messages) > 0 && a.messages[0].Role == llm.RoleSystem {
		a.messages = a.messages[:1]
	} else {
		a.messages = nil
	}
}

// Run handles a single user turn, looping over tool calls until the model
// produces a final answer or the iteration cap is hit.
func (a *Agent) Run(ctx context.Context, userInput string) error {
	a.messages = append(a.messages, llm.Message{Role: llm.RoleUser, Content: userInput})

	for i := 0; i < maxToolIterations; i++ {
		// Keep input + reserved output within the context window by dropping
		// the oldest complete turns when the history grows too large.
		if a.tokBudget > 0 {
			if trimmed, dropped := trimHistory(a.messages, a.tokBudget); dropped > 0 {
				a.messages = trimmed
				a.printer.Notice(fmt.Sprintf("context trimmed: dropped %d oldest message(s) to stay within the token budget", dropped))
			}
		}

		reply, err := a.client.Stream(ctx, a.messages, a.tools, a.printer.AssistantDelta)
		if err != nil {
			return err
		}
		a.printer.AssistantDone()

		// Record the assistant turn verbatim so tool_call ids round-trip.
		a.messages = append(a.messages, llm.Message{
			Role:      llm.RoleAssistant,
			Content:   reply.Text,
			ToolCalls: reply.ToolCalls,
		})

		if reply.Truncated {
			a.printer.Notice("model output hit max_tokens and was truncated; consider raising -max-tokens")
		}

		if len(reply.ToolCalls) == 0 {
			return nil // model is done with this turn
		}

		for _, tc := range reply.ToolCalls {
			args := tc.Arguments
			if args == "" {
				args = "{}"
			}
			a.printer.ToolStart(tc.Name, displayCommand(args))
			res := tools.Dispatch(ctx, a.exec, a.approve, tc.Name, json.RawMessage(args))
			a.printer.ToolEnd(res.Status, res.Output, res.IsError)
			a.messages = append(a.messages, llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Content:    res.ModelText,
			})
		}
	}
	return fmt.Errorf("reached max tool iterations (%d) without finishing", maxToolIterations)
}

// displayCommand extracts a human-friendly summary from a tool's JSON
// arguments: the command for run_shell, or the path for file tools, falling
// back to the raw JSON.
func displayCommand(args string) string {
	var in struct {
		Command string `json:"command"`
		Path    string `json:"path"`
	}
	if json.Unmarshal([]byte(args), &in) == nil {
		if in.Command != "" {
			return in.Command
		}
		if in.Path != "" {
			return in.Path
		}
	}
	return args
}
