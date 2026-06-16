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
	AssistantDelta(text string)                        // a chunk of streamed assistant text
	AssistantDone()                                    // the streamed text turn finished
	ToolStart(name, command string)                    // a tool about to run
	ToolEnd(exitCode int, output string, isError bool) // a tool finished
	Notice(text string)                                // an out-of-band status note
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
	return fmt.Sprintf(`You are a lightweight coding agent that helps the user by running shell commands on their machine.

Environment:
- %s
- Host OS family: %s

Guidelines:
- Use the run_shell tool to inspect and modify the system. Generate commands in the syntax native to the host shell shown above.
- To create, overwrite or edit files, ALWAYS use the write_file / edit_file / read_file tools. Never use shell here-docs (cat <<EOF), echo redirection or sed to write files — those cause quoting and escaping errors, especially with non-ASCII text.
- Take one concrete step at a time; read output before deciding the next action.
- Keep commands minimal and avoid destructive operations unless the user clearly asked for them.
- When the task is complete, summarize what you did in plain language without calling more tools.`,
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
			a.printer.ToolEnd(res.ExitCode, res.Output, res.IsError)
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
