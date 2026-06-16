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

// OpenAIClient targets an OpenAI-compatible Chat Completions endpoint.
type OpenAIClient struct {
	apiKey     string
	baseURL    string
	model      string
	maxTokens  int
	httpClient *http.Client
}

// NewOpenAI builds an OpenAI-compatible provider. The "/chat/completions"
// suffix is appended to baseURL automatically.
func NewOpenAI(baseURL, apiKey, model string, maxTokens int) *OpenAIClient {
	if maxTokens == 0 {
		maxTokens = 4096
	}
	return &OpenAIClient{
		apiKey:     apiKey,
		baseURL:    strings.TrimRight(baseURL, "/"),
		model:      model,
		maxTokens:  maxTokens,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
	}
}

func (c *OpenAIClient) Model() string { return c.model }

// --- wire types ---

type oaFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaToolCall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function oaFunctionCall `json:"function"`
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    string       `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type oaTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type oaRequest struct {
	Model     string      `json:"model"`
	Messages  []oaMessage `json:"messages"`
	Tools     []oaTool    `json:"tools,omitempty"`
	MaxTokens int         `json:"max_tokens,omitempty"`
	Stream    bool        `json:"stream"`
}

// oaStreamChunk is one SSE delta from a streaming completion.
type oaStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Stream implements Provider.
func (c *OpenAIClient) Stream(ctx context.Context, messages []Message, tools []Tool, onText StreamFunc) (*Reply, error) {
	body, err := json.Marshal(oaRequest{
		Model:     c.model,
		Messages:  toOAMessages(messages),
		Tools:     toOATools(tools),
		MaxTokens: c.maxTokens,
		Stream:    true,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	var text strings.Builder
	// Tool calls arrive fragmented across chunks; accumulate by index.
	type acc struct {
		id, name string
		args     strings.Builder
	}
	var calls []*acc
	truncated := false

	err = streamSSE(c.httpClient, req, func(data []byte) error {
		var chunk oaStreamChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return fmt.Errorf("decode chunk: %w", err)
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return fmt.Errorf("api error: %s", chunk.Error.Message)
		}
		if len(chunk.Choices) == 0 {
			return nil
		}
		if chunk.Choices[0].FinishReason == "length" {
			truncated = true
		}
		delta := chunk.Choices[0].Delta
		if delta.Content != "" {
			text.WriteString(delta.Content)
			onText(delta.Content)
		}
		for _, tc := range delta.ToolCalls {
			for len(calls) <= tc.Index {
				calls = append(calls, &acc{})
			}
			a := calls[tc.Index]
			if tc.ID != "" {
				a.id = tc.ID
			}
			if tc.Function.Name != "" {
				a.name = tc.Function.Name
			}
			a.args.WriteString(tc.Function.Arguments)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	reply := &Reply{Text: text.String(), Truncated: truncated}
	for _, a := range calls {
		reply.ToolCalls = append(reply.ToolCalls, ToolCall{
			ID:        a.id,
			Name:      a.name,
			Arguments: a.args.String(),
		})
	}
	return reply, nil
}

func toOAMessages(messages []Message) []oaMessage {
	out := make([]oaMessage, 0, len(messages))
	for _, m := range messages {
		om := oaMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			om.ToolCalls = append(om.ToolCalls, oaToolCall{
				ID:       tc.ID,
				Type:     "function",
				Function: oaFunctionCall{Name: tc.Name, Arguments: tc.Arguments},
			})
		}
		out = append(out, om)
	}
	return out
}

func toOATools(tools []Tool) []oaTool {
	out := make([]oaTool, 0, len(tools))
	for _, t := range tools {
		var ot oaTool
		ot.Type = "function"
		ot.Function.Name = t.Name
		ot.Function.Description = t.Description
		ot.Function.Parameters = t.Parameters
		out = append(out, ot)
	}
	return out
}
