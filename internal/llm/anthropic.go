package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const anthropicVersion = "2023-06-01"

// AnthropicClient targets an Anthropic-compatible Messages endpoint.
type AnthropicClient struct {
	apiKey     string
	baseURL    string
	model      string
	maxTokens  int
	httpClient *http.Client
}

// NewAnthropic builds an Anthropic-compatible provider. The "/v1/messages"
// suffix is appended to baseURL automatically.
func NewAnthropic(baseURL, apiKey, model string, maxTokens int) *AnthropicClient {
	if maxTokens == 0 {
		maxTokens = 4096
	}
	return &AnthropicClient{
		apiKey:     apiKey,
		baseURL:    strings.TrimRight(baseURL, "/"),
		model:      model,
		maxTokens:  maxTokens,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
	}
}

func (c *AnthropicClient) Model() string { return c.model }

// --- wire types ---

type antBlock struct {
	Type string `json:"type"`
	// text
	Text string `json:"text,omitempty"`
	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

type antMessage struct {
	Role    string     `json:"role"`
	Content []antBlock `json:"content"`
}

type antTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type antRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	System    string       `json:"system,omitempty"`
	Messages  []antMessage `json:"messages"`
	Tools     []antTool    `json:"tools,omitempty"`
	Stream    bool         `json:"stream"`
}

// antStreamEvent is one SSE event from the Messages stream.
type antStreamEvent struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Stream implements Provider.
func (c *AnthropicClient) Stream(ctx context.Context, messages []Message, tools []Tool, onText StreamFunc) (*Reply, error) {
	system, antMsgs := toAntMessages(messages)

	body, err := json.Marshal(antRequest{
		Model:     c.model,
		MaxTokens: c.maxTokens,
		System:    system,
		Messages:  antMsgs,
		Tools:     toAntTools(tools),
		Stream:    true,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("Content-Type", "application/json")

	var text strings.Builder
	// Each content block is tracked by its index; tool_use input arrives as
	// incremental JSON fragments that we concatenate.
	type block struct {
		isTool   bool
		id, name string
		args     strings.Builder
	}
	blocks := map[int]*block{}
	var order []int
	truncated := false

	err = streamSSE(c.httpClient, req, func(data []byte) error {
		var ev antStreamEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return fmt.Errorf("decode event: %w", err)
		}
		switch ev.Type {
		case "error":
			if ev.Error != nil {
				return fmt.Errorf("api error: %s", ev.Error.Message)
			}
		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason == "max_tokens" {
				truncated = true
			}
		case "content_block_start":
			b := &block{}
			if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
				b.isTool = true
				b.id = ev.ContentBlock.ID
				b.name = ev.ContentBlock.Name
			}
			blocks[ev.Index] = b
			order = append(order, ev.Index)
		case "content_block_delta":
			b := blocks[ev.Index]
			if b == nil || ev.Delta == nil {
				return nil
			}
			switch ev.Delta.Type {
			case "text_delta":
				text.WriteString(ev.Delta.Text)
				onText(ev.Delta.Text)
			case "input_json_delta":
				b.args.WriteString(ev.Delta.PartialJSON)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	reply := &Reply{Text: text.String(), Truncated: truncated}
	for _, idx := range order {
		b := blocks[idx]
		if b.isTool {
			args := b.args.String()
			if args == "" {
				args = "{}"
			}
			reply.ToolCalls = append(reply.ToolCalls, ToolCall{ID: b.id, Name: b.name, Arguments: args})
		}
	}
	return reply, nil
}

// toAntMessages extracts the system prompt and converts the neutral history.
// Consecutive tool results are merged into a single user message, as Anthropic
// requires all tool_result blocks for a turn to live in one user message.
func toAntMessages(messages []Message) (system string, out []antMessage) {
	var sys []string
	var pendingResults []antBlock

	flush := func() {
		if len(pendingResults) > 0 {
			out = append(out, antMessage{Role: RoleUser, Content: pendingResults})
			pendingResults = nil
		}
	}

	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			sys = append(sys, m.Content)
		case RoleTool:
			pendingResults = append(pendingResults, antBlock{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Content,
			})
		case RoleUser:
			flush()
			out = append(out, antMessage{
				Role:    RoleUser,
				Content: []antBlock{{Type: "text", Text: m.Content}},
			})
		case RoleAssistant:
			flush()
			var blocks []antBlock
			if m.Content != "" {
				blocks = append(blocks, antBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				input := json.RawMessage(tc.Arguments)
				// A model may emit truncated/invalid tool JSON. Never let it
				// into the request: json.RawMessage validates on marshal, so an
				// invalid value would crash every subsequent turn.
				if len(input) == 0 || !json.Valid(input) {
					input = json.RawMessage("{}")
				}
				blocks = append(blocks, antBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: input,
				})
			}
			out = append(out, antMessage{Role: RoleAssistant, Content: blocks})
		}
	}
	flush()
	return strings.Join(sys, "\n\n"), out
}

func toAntTools(tools []Tool) []antTool {
	out := make([]antTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, antTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
	}
	return out
}
