// Package llm provides a provider-agnostic chat interface with concrete
// implementations for OpenAI-compatible and Anthropic-compatible endpoints.
// All implementations are dependency-free (standard library only).
package llm

import (
	"context"
	"encoding/json"
)

// Role constants for neutral messages.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// ToolCall is one tool invocation. Arguments is the JSON-encoded argument
// object as a string (the lowest common denominator across providers).
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// Message is a provider-neutral chat entry. Fields are populated per role:
// system/user/assistant carry Content; assistant may carry ToolCalls; a tool
// result uses RoleTool with ToolCallID + Content.
type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

// Tool is a provider-neutral tool definition.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// Reply is the model's complete response for one turn, assembled from the
// stream once it finishes.
type Reply struct {
	Text      string
	ToolCalls []ToolCall
	Truncated bool // output stopped because it hit the max_tokens limit
}

// StreamFunc receives assistant text as it arrives. Tool-call arguments are not
// streamed to it; they are accumulated and returned in the final Reply.
type StreamFunc func(textDelta string)

// Provider is the minimal surface the agent needs from any LLM backend.
// Stream emits text deltas via onText and returns the assembled Reply.
type Provider interface {
	Stream(ctx context.Context, messages []Message, tools []Tool, onText StreamFunc) (*Reply, error)
	Model() string
}
