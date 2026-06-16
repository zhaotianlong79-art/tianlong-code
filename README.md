# tianlong-agent

一个轻量化的 Codex 风格编码 agent，用 Go 实现，**仅依赖 `golang.org/x/term`**（Go 官方维护，用于行编辑），跨平台支持 macOS / Linux / Windows 的 shell 命令执行。

## 特性

- **单文件分发**：`go build` 出来就是一个静态二进制，用户无需安装任何运行时。
- **跨平台 shell**：自动探测 shell（macOS/Linux 用 `bash`/`sh`，Windows 优先 `pwsh` 退回 `powershell`），并把 OS/shell 信息注入 system prompt，让模型生成原生命令。
- **ReAct 主循环**：基于 Claude 的 tool-use API，模型调用 `run_shell` → 执行 → 回喂结果 → 循环直到完成。
- **行编辑输入**：真实终端下进入 raw 模式，支持 rune 级退格（正确删除中文等多字节字符）、左右方向键、**上下方向键回溯历史输入**；管道输入自动回退。
- **Markdown 渲染**：助手输出在终端按行渲染为 ANSI 样式（标题/粗体/斜体/行内代码/代码块/列表/引用/表格分隔行）；管道输出保持纯文本。
- **斜杠命令**：运行时通过 `/help`、`/approval <mode>`、`/clear` 等管理会话。
- **安全护栏**：只读命令自动放行，写/网络等操作默认询问；超时与输出截断防止失控。
- **持久工作目录**：`cd` 在多次命令间保持。

同时支持 **Anthropic 兼容**（Messages API）和 **OpenAI 兼容**（Chat Completions）两种协议，按 `.env` 里设置了哪个 base URL 自动选择。已在讯飞 MaaS + Qwen 上验证两种协议均可用。

## 快速开始

在项目根目录建一个 `.env`，二选一即可（注释掉另一个）：

```dotenv
# Anthropic 兼容（请求 {BASE}/v1/messages，认证 x-api-key）
Anthropic_BASE_URL=https://maas-api.cn-huabei-1.xf-yun.com/anthropic
# OpenAI 兼容（请求 {BASE}/chat/completions，认证 Authorization: Bearer）
# OPENAI_BASE_URL=https://maas-api.cn-huabei-1.xf-yun.com/v2
API_KEY=你的key
MODEL_ID=xopqwen36v35b
```

然后运行：

```bash
go run .            # 或 go build -o tianlong . && ./tianlong
```

> ⚠️ `.env` 含密钥，不要提交到版本库（已在 `.gitignore` 忽略）。

## 配置

| 变量 | 必填 | 说明 |
|------|------|------|
| `Anthropic_BASE_URL` | 二选一 | Anthropic 兼容服务地址（自动追加 `/v1/messages`，两者都设时优先用它） |
| `OPENAI_BASE_URL` | 二选一 | OpenAI 兼容服务地址（自动追加 `/chat/completions`） |
| `API_KEY` | 是 | 认证密钥（Anthropic 走 `x-api-key`，OpenAI 走 `Bearer`） |
| `MODEL_ID` | 是 | 模型 id |

## 命令行参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-env` | `.env` | `.env` 文件路径（不存在则忽略） |
| `-model` | — | 覆盖 `MODEL_ID` |
| `-approval` | `ask` | 批准模式：`ask`（只读放行其余询问）/ `read` / `yolo`（全自动） |
| `-max-tokens` | `4096` | 单次响应最大输出 token |
| `-context-window` | `32768` | 模型上下文窗口（token）；历史会自动裁剪以保证 `输入 + max-tokens + 余量 ≤ 窗口`，设 `0` 关闭。也可用环境变量 `TIANLONG_CONTEXT_WINDOW` |

### 斜杠命令（REPL 内）

| 命令 | 说明 |
|------|------|
| `/help` | 显示命令帮助 |
| `/approval <mode>` | 运行时切换审批模式（`ask`/`read`/`yolo`）；不带参数显示当前模式 |
| `/clear` | 清空对话历史（保留系统提示） |
| `/exit`、`/quit` | 退出（或 `Ctrl-D`） |

输入框支持 `↑`/`↓` 回溯历史、`←`/`→` 移动光标、`Ctrl-A/E/U/K`、Backspace（rune 级，正确处理中文）。

### 上下文管理

为避免 `input length and max_tokens exceed context limit` 类报错，每轮请求前会：

- **按轮次滑动窗口裁剪**：历史超过预算时丢弃最旧的完整轮次，只在真实用户输入边界切割（保证 tool_use / tool_result 配对不被破坏），并打印 `» context trimmed: ...`。
- **预留输出预算**：预算 = 上下文窗口 − `max-tokens` − 安全余量。
- **输出截断检测**：若模型响应因触达 `max_tokens` 被腰斩，会提示 `» model output hit max_tokens ...`，建议调大 `-max-tokens`。

> Token 数用轻量启发式估算（约 3 字节/token，偏保守），无需引入分词器依赖。把 `-context-window` 设为你所用模型的真实窗口即可。

## 跨平台编译

```bash
GOOS=windows GOARCH=amd64 go build -o tianlong.exe .
GOOS=linux   GOARCH=amd64 go build -o tianlong-linux .
GOOS=darwin  GOARCH=arm64 go build -o tianlong-mac .
```

## 结构

```
main.go                     CLI / REPL 入口、provider 选择、批准提示、输出渲染
internal/config/dotenv.go   极简 .env 加载器（纯标准库）
internal/llm/types.go       provider 无关的消息/工具类型 + Provider 接口
internal/llm/openai.go      OpenAI Chat Completions 实现
internal/llm/anthropic.go   Anthropic Messages 实现
internal/llm/http.go        共享 HTTP 请求/错误处理
internal/shell/executor.go  跨平台执行、超时、输出截断、cwd 持久化
internal/tools/tools.go     工具 schema 与分发
internal/approval/policy.go 命令安全分类与批准模式
internal/agent/agent.go     ReAct 主循环与对话状态
```

## 已知限制 / 后续可扩展

- 助手文本为 **SSE 流式输出**，边生成边显示；工具调用参数累积完整后再执行。
- `cd` 仅支持独立的 `cd <dir>`；链式 `cd a && cmd` 在子 shell 中执行，不影响持久 cwd。
- 只读命令分类是保守的前缀启发式，复杂管道一律走询问。
- 可扩展：prompt caching、文件读写专用工具、会话持久化、网络错误退避重试、MCP 接入。
