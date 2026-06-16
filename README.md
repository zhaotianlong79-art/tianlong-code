# Tianlong Agent

> **English** | [中文文档](docs/README.md)

A lightweight, **dependency-free** (Go standard library only) CLI coding agent that drives a Claude model and executes shell commands across **macOS**, **Linux**, and **Windows**.

> Inspired by the [Codex](https://openai.com/index/codex) style of agentic coding — think ReAct (Reason + Act), tool use via LLM, and safe shell execution with human-in-the-loop approval.

---

## ✨ Features

| Feature | Description |
|---------|-------------|
| **Zero 3rd-party deps** | Uses only the Go standard library (plus `golang.org/x/term`) |
| **Dual LLM backend** | Anthropic Messages API & OpenAI Chat Completions API (auto-selected by config) |
| **SSE streaming** | Server-Sent Events for real-time text + tool-call streaming |
| **Cross-platform shells** | Auto-detects `bash`/`sh` (macOS/Linux) or `pwsh`/`powershell` (Windows) |
| **Human-in-the-loop approval** | Three modes: `ask` (default), `read`, or `yolo` (no questions) |
| **Persistent shell cwd** | `cd <dir>` carries over across agent iterations |
| **Built-in Markdown renderer** | Streaming ANSI-colored Markdown output in a terminal |
| **Slash commands** | `/help`, `/approval <mode>`, `/clear`, `/exit` |
| **Context window management** | Automatic history trimming to fit within a configurable token budget |
| **Command timeout & truncation** | 60 s per command by default; output capped at 16 KB |

---

## 📐 Architecture

```
tianlong-agent/
├── main.go                     # CLI / REPL entry, provider selection, printer
├── lineeditor.go               # Line editor with history (up / down arrows)
├── markdown.go                 # Minimal ANSI Markdown renderer
├── sysinfo.go                  # OS / CPU / shell / directory detection
├── go.mod                      # Module definition (Go 1.26)
│
└── internal/
    ├── agent/agent.go          # ReAct main loop & conversation state
    ├── approval/policy.go      # Command safety classification & approval modes
    ├── config/dotenv.go        # Minimal .env loader (stdlib only)
    ├── llm/
    │   ├── types.go            # Provider-agnostic Message / Tool / Provider interface
    │   ├── anthropic.go        # Anthropic Messages API implementation
    │   ├── openai.go           # OpenAI Chat Completions API implementation
    │   └── http.go             # Shared SSE streaming HTTP client
    ├── shell/executor.go       # Cross-platform shell execution engine
    └── tools/tools.go          # Tool definitions (run_shell) & dispatch
```

### ReAct Loop (simplified)

```
User input
  → Append user message to history
  → Loop (up to 25 iterations):
      → Stream LLM response (text + tool calls)
      → Append assistant message to history
      → If no tool calls → done
      → For each tool call:
          → Approve (if not yolo)
          → Execute via shell.Executor
          → Append tool result to history
  → Max iterations reached → error
```

---

## 🚀 Quick Start

### Prerequisites

- **Go 1.26+**
- An API key for an Anthropic-compatible or OpenAI-compatible endpoint
- A terminal that supports ANSI colors

### 1. Clone & configure

```bash
git clone <your-repo-url>
cd tianlong-agent
cp .env.example .env
```

Edit `.env` and fill in your credentials:

```ini
# Choose ONE protocol by setting its base URL.
Anthropic_BASE_URL=https://your-endpoint.example.com/anthropic
# OPENAI_BASE_URL=https://your-endpoint.example.com/v2

API_KEY=your-api-key-here
MODEL_ID=your-model-id
```

> **⚠️ `.env` is gitignored — never commit real keys.**

### 2. Run

```bash
go run .
```

Or set overrides via flags:

```bash
go run . -model claude-sonnet-4-20250514 -approval yolo
```

### 3. Interact

You'll see a banner with your configuration, then the REPL prompt:

```
tianlong-agent

  Model      claude-sonnet-4-20250514 (anthropic)
  Approval   ask
  OS         darwin arm64
  ...

Type your request, or /exit to quit.

you> 
```

Type a natural-language request — the agent will think, call tools (run shell commands), and respond:

```
you> list the top 5 files by size in /tmp

⏺ Bash(list the top 5 files by size in /tmp)
  ● exit 0
  ● 5 lines

Here are the 5 largest files in /tmp ...
```

### Slash Commands

| Command | Description |
|---------|-------------|
| `/help` | Show this help message |
| `/approval ask \| read \| yolo` | Change approval mode |
| `/clear` | Clear conversation history |
| `/exit`, `/quit` | Quit the agent |

---

## ⚙️ Configuration

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `API_KEY` | ✅ | — | Your LLM provider API key |
| `MODEL_ID` | ✅ | — | Model identifier to use |
| `Anthropic_BASE_URL` | ⚠️* | — | Anthropic-compatible base URL |
| `OPENAI_BASE_URL` | ⚠️* | — | OpenAI-compatible base URL |
| `TIANLONG_CONTEXT_WINDOW` | ❌ | `32768` | Max context window in tokens (0 = unlimited) |
| `TIANLONG_APPROVAL` | ❌ | `ask` | Default approval mode |

*\* Set at least one. If both are set, Anthropic takes priority.*

### Command-line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-env` | `.env` | Path to the `.env` file |
| `-model` | (from env) | Model ID override |
| `-approval` | `ask` | Approval mode: `ask`, `read`, or `yolo` |
| `-max-tokens` | `4096` | Max output tokens per LLM response |
| `-context-window` | `32768` | Context window size in tokens |

---

## 🛡️ Safety

| Protection | Details |
|------------|---------|
| **Approval mechanism** | Non-read-only commands prompt for user confirmation (3 modes) |
| **Command timeout** | 60 seconds per command by default |
| **Output truncation** | 16 KB cap per command to protect context window |
| **Parameter validation** | Invalid tool JSON is replaced with an empty object |
| **cwd scoping** | `cd` only takes effect on standalone commands (not chained/piped) |

### Approval Modes

| Mode | Behavior |
|------|----------|
| `ask` (default) | Auto-runs read-only commands; asks for anything else |
| `read` | Same as `ask` (reserved for future fine-tuning) |
| `yolo` | Runs everything without asking — use with caution |

### Read-Only Heuristics

The agent classifies a command as read-only (and thus auto-approves it) if:

- It starts with a whitelisted command: `ls`, `cat`, `pwd`, `grep`, `find`, `head`, `tail`, `wc`, `which`, etc.
- It's a read-only Git subcommand: `status`, `log`, `diff`, `show`, `branch`, etc.
- It probes version/help: `--version`, `--help`, `version`

Commands containing pipes `|`, redirects `>`, chains `;&`, or backticks are always treated as potentially mutating.

---

## 🔌 Provider Compatibility

| Dimension | Anthropic | OpenAI |
|-----------|-----------|--------|
| Path suffix | `/v1/messages` | `/chat/completions` |
| Auth header | `x-api-key` | `Authorization: Bearer` |
| Tool types | `tool_use` / `tool_result` | `function` / `tool_call_id` |
| System messages | Top-level `system` field | `system` role message |
| Streaming events | `content_block_delta` | `choices[0].delta` |
| End signal | Natural end | `[DONE]` marker |

---

## 🗺️ Roadmap

- [ ] Prompt Caching support
- [ ] File read/write dedicated tools
- [ ] Session persistence / history
- [ ] Network error backoff & retry
- [ ] MCP (Model Context Protocol) integration
- [ ] Multi-turn conversation persistence to disk
- [ ] More LLM providers (Gemini, Ollama, etc.)

---

## 📄 License

This project is licensed under the terms of the [MIT License](LICENSE).

---

## 🙏 Acknowledgements

Inspired by [OpenAI Codex](https://openai.com/index/codex) and the broader agentic coding research community.
