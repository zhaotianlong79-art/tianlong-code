package agent

import "tianlong-agent/internal/llm"

// trimMargin is a safety buffer (in estimated tokens) kept free on top of the
// reserved output budget, to absorb estimation error.
const trimMargin = 1024

// estimateTokens is a cheap, dependency-free, deliberately conservative token
// estimate: ~3 UTF-8 bytes per token. ASCII text runs ~4 chars/token so this
// slightly overestimates (safe), while CJK (3 bytes/char) lands near 1
// token/char which matches reality. We overestimate on purpose so the trimmer
// stays under the real context limit.
func estimateTokens(s string) int {
	return len(s)/3 + 1
}

// estimateMessageTokens approximates the tokens one message contributes,
// including a small per-message overhead for role/formatting wrappers.
func estimateMessageTokens(m llm.Message) int {
	t := estimateTokens(m.Content) + 4
	for _, tc := range m.ToolCalls {
		t += estimateTokens(tc.Name) + estimateTokens(tc.Arguments) + 4
	}
	return t
}

// trimHistory bounds the conversation to fit budget estimated tokens. It always
// keeps the leading system message, then keeps the longest suffix of recent
// turns that fits, cutting only at real user-input boundaries. Cutting at user
// boundaries guarantees we never split a tool_use from its tool_result (which
// would make the request invalid), since all tool traffic lives between user
// turns. It returns the trimmed slice and how many messages were dropped.
func trimHistory(msgs []llm.Message, budget int) ([]llm.Message, int) {
	if len(msgs) == 0 {
		return msgs, 0
	}

	var system *llm.Message
	rest := msgs
	if msgs[0].Role == llm.RoleSystem {
		system = &msgs[0]
		rest = msgs[1:]
	}

	sysTokens := 0
	if system != nil {
		sysTokens = estimateMessageTokens(*system)
	}

	// Walk backwards, accumulating tokens. Record the smallest (oldest) user
	// boundary whose suffix still fits the budget — that keeps the most history.
	acc := 0
	keepFrom := -1
	for i := len(rest) - 1; i >= 0; i-- {
		acc += estimateMessageTokens(rest[i])
		if rest[i].Role == llm.RoleUser && sysTokens+acc <= budget {
			keepFrom = i
		}
	}

	// If not even the most recent user turn fits, keep from the last user
	// boundary anyway (best effort — better than dropping the active turn).
	if keepFrom == -1 {
		for i := len(rest) - 1; i >= 0; i-- {
			if rest[i].Role == llm.RoleUser {
				keepFrom = i
				break
			}
		}
		if keepFrom == -1 {
			keepFrom = 0
		}
	}

	dropped := keepFrom
	if keepFrom == 0 {
		return msgs, 0
	}

	trimmed := rest[keepFrom:]
	if system != nil {
		out := make([]llm.Message, 0, len(trimmed)+1)
		out = append(out, *system)
		out = append(out, trimmed...)
		return out, dropped
	}
	return trimmed, dropped
}
