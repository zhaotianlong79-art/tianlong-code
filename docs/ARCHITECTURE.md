# tianlong-agent 代码架构文档

## 概述

**tianlong-agent** 是一个轻量级的 **Codex 风格编码 Agent**，使用 Go 实现，**零第三方依赖（纯标准库）**，跨平台支持 macOS / Linux / Windows 的 shell 命令执行。

它基于 **ReAct（Reasoning + Acting）** 范式，通过 LLM 工具调用（Tool Use）API 让大模型自主执行 shell 命令来完成任务。

## 技术栈

| 项目 | 说明 |
|------|------|
| 语言 | Go 1.26 |
| 依赖 | **零第三方依赖**（仅使用 Go 标准库） |
| LLM 协议 | Anthropic Messages API / OpenAI Chat Completions API |
| 通信方式 | SSE（Server-Sent Events）流式输出 |
| 运行环境 | macOS / Linux / Windows |

## 项目结构

```
tianlong-agent/
├── main.go                     # CLI / REPL 入口、provider 选择、批准提示、输出渲染
├── go.mod                      # 模块定义 (tianlong-agent, go 1.26)
├── ARCHITECTURE.md             # 本文档
│
└── internal/
    ├── agent/agent.go          # ReAct 主循环与对话状态管理
    ├── approval/policy.go      # 命令安全分类与批准模式
    ├── config/dotenv.go        # 极简 .env 加载器（纯标准库）
    ├── llm/
    │   ├── types.go            # provider 无关的消息/工具类型 + Provider 接口
    │   ├── anthropic.go        # Anthropic Messages API 实现
    │   ├── openai.go           # OpenAI Chat Completions API 实现
    │   └── http.go             # 共享 SSE 流式 HTTP 请求/错误处理
    ├── shell/executor.go       # 跨平台 shell 执行引擎
    └── tools/tools.go          # 工具定义 (run_shell) 与请求分发
```

## 核心模块详解

### 1. `main.go` — CLI / REPL 入口

**职责**：程序启动、参数解析、配置加载、LLM  Provider 选择、REPL 交互循环、输出渲染。

**关键流程**：

1. 解析命令行参数（`-env`, `-model`, `-approval`, `-max-tokens`）
2. 加载 `.env` 文件获取 `API_KEY`, `MODEL_ID`, `Anthropic_BASE_URL` / `OPENAI_BASE_URL`
3. **Provider 自动选择**：
   - 若设置了 `Anthropic_BASE_URL` → 使用 Anthropic 兼容客户端
   - 否则 → 使用 OpenAI 兼容客户端
4. 初始化 Shell 执行器、批准策略、标准输入读取器
5. 进入 REPL 循环：读取用户输入 → 调用 Agent 执行 → 渲染输出

**辅助组件**：
- `consolePrinter`：实现 `Printer` 接口，将 Agent 的流式输出渲染到终端
- `makeApprover`：根据批准模式决定是否自动放行命令

### 2. `internal/agent/agent.go` — ReAct 主循环

**职责**：维护对话状态、执行 ReAct 迭代循环（思考 → 工具调用 → 执行 → 观察）。

**核心数据结构**：
- `Agent`：持有 LLM 客户端、Shell 执行器、批准函数、渲染器、工具列表和历史消息
- `messages []llm.Message`：完整的对话历史，跨轮次持久化

**`Run(ctx, userInput)` 流程**：

```
用户输入
  → 追加 user 消息到历史
  → 循环 (最多 25 次):
      → 调用 LLM Stream 获取回复（含文本 + 工具调用）
      → 追加 assistant 消息到历史
      → 若无工具调用 → 完成
      → 对每个工具调用:
          → 通过 tools.Dispatch 执行命令
          → 追加 tool 结果消息到历史
  → 达到最大迭代次数 → 报错
```

**系统提示词**：动态生成，注入当前 OS/Shell/工作目录信息和操作指南。

### 3. `internal/llm/types.go` — Provider 无关的类型定义

**职责**：定义 LLM 交互的抽象类型，使上层代码不依赖具体 Provider。

**核心接口**：
```go
type Provider interface {
    Stream(ctx, messages, tools, onText) (*Reply, error)  // SSE 流式调用
    Model() string                                          // 当前模型名称
}
```

**核心类型**：
| 类型 | 说明 |
|------|------|
| `Message` | 一条消息（system/user/assistant/tool 四种角色） |
| `ToolCall` | 工具调用（ID + 名称 + JSON 参数） |
| `Tool` | 工具定义（名称 + 描述 + JSON Schema） |
| `Reply` | 模型完整回复（文本 + 工具调用列表） |
| `StreamFunc` | 文本 delta 流式回调 |

### 4. `internal/llm/anthropic.go` — Anthropic 实现

**职责**：将 Provider 无关的类型转换为 Anthropic Messages API 格式并发起 SSE 流式请求。

**关键处理**：
- 自动追加 `/v1/messages` 路径
- 使用 `x-api-key` 认证头
- 设置 `anthropic-version: 2023-06-01`
- 消息转换：合并连续的 tool_result 为单个 user 消息（Anthropic 要求）
- 验证 tool 参数的 JSON 合法性，防止无效数据污染后续对话

### 5. `internal/llm/openai.go` — OpenAI 实现

**职责**：将 Provider 无关的类型转换为 OpenAI Chat Completions API 格式。

**关键处理**：
- 自动追加 `/chat/completions` 路径
- 使用 `Authorization: Bearer {key}` 认证头
- 工具类型标记为 `function`
- 按索引累积分片传来的 tool_call 参数

### 6. `internal/llm/http.go` — 共享 HTTP 层

**职责**：统一的 SSE 流式 HTTP 请求处理，被 Anthropic 和 OpenAI 共享。

**核心函数**：`streamSSE(client, req, onData)`
- 逐行读取 `data:` 前缀的事件
- 处理 `[DONE]` 终止信号（OpenAI）和自然结束（Anthropic）
- 非 2xx 响应解析错误体并返回
- 支持大 payload（最大 4MB 行缓冲）

### 7. `internal/shell/executor.go` — 跨平台 Shell 执行引擎

**职责**：在本地机器上安全执行 shell 命令，屏蔽平台差异。

**关键特性**：
| 特性 | 说明 |
|------|------|
| **自动 Shell 探测** | Windows → `pwsh` / `powershell`；macOS/Linux → `bash` / `sh` |
| **持久化 cwd** | `cd <dir>` 后切换工作目录，后续命令在该目录下执行 |
| **超时控制** | 默认 60 秒/命令，可通过参数覆盖 |
| **输出截断** | 单命令输出上限 16KB，保留头尾，中间用 `[... truncated]` 标记 |
| **跨平台统一** | `Result` 结构体统一封装 stdout/stderr/exitCode/timedOut |

**`bareCD` 检测**：识别独立的 `cd <dir>` 命令（不含管道/链式），实现 cwd 持久化。

### 8. `internal/tools/tools.go` — 工具定义与分发

**职责**：定义 LLM 可用的工具 schema，并负责将工具调用分发到具体执行器。

**工具定义**：`run_shell`
- 参数：`command`（必选，命令字符串）、`timeout_seconds`（可选，超时秒数）
- 描述：详细说明使用方式

**`Dispatch` 流程**：
```
收到工具调用
  → 解析 JSON 输入
  → 调用 Approver 确认（非 Yolo 模式下）
  → 调用 shell.Executor.Run 执行
  → 格式化 Result 为文本返回
```

### 9. `internal/approval/policy.go` — 命令安全策略

**职责**：判断命令是否自动放行或需要用户确认。

**三种批准模式**：
| 模式 | 行为 |
|------|------|
| `ask` | 只读命令自动放行，其余询问 |
| `read` | 同 `ask`（保留接口，未来可能细化） |
| `yolo` | 全自动执行，不询问 |

**只读判定逻辑**：
- 包含管道 `|`、重定向 `>`、链式 `;&` 等 → 不视为只读
- 白名单命令：`ls`, `cat`, `pwd`, `grep`, `find`, `head`, `tail` 等
- Git 只读子命令：`status`, `log`, `diff`, `show` 等
- 版本探测：`--version`, `--help` 一律放行
- 解释器（go/node/python）：仅放行 `version`、`env`、`list` 等只读子命令

### 10. `internal/config/dotenv.go` — .env 加载器

**职责**：纯标准库实现的环境变量加载，不依赖 godotenv 等第三方包。

**特性**：
- 支持 `KEY=VALUE`、`export KEY=VALUE`
- 支持 `#` 注释和空行
- 支持单/双引号值
- **不覆盖**已存在的环境变量（允许命令行参数优先级更高）
- 文件不存在不报错

## 数据流图

```
┌─────────────┐     ┌──────────────┐     ┌──────────────┐
│  用户输入     │────▶│   Agent      │────▶│  LLM Provider│
│  (REPL)      │     │  (ReAct Loop)│     │  (Anthropic  │
│              │◀────│              │◀────│  / OpenAI)    │
└─────────────┘     └──────┬───────┘     └──────────────┘
                           │
                    ┌──────▼───────┐     ┌──────────────┐
                    │  Tools       │────▶│  Shell        │
                    │  Dispatch    │     │  Executor     │
                    └──────┬───────┘     └──────────────┘
                           │
                    ┌──────▼───────┐
                    │  Approver    │
                    │  (Policy)    │
                    └──────────────┘
                           │
                    ┌──────▼───────┐
                    │  Console     │
                    │  Printer     │
                    └──────────────┘
```

## 交互协议对比

| 维度 | Anthropic | OpenAI |
|------|-----------|--------|
| 路径后缀 | `/v1/messages` | `/chat/completions` |
| 认证头 | `x-api-key` | `Authorization: Bearer` |
| 工具类型 | `tool_use` / `tool_result` | `function` / `tool_call_id` |
| 系统消息 | `system` 顶层字段 | `system` 角色消息 |
| 流式事件 | `content_block_delta` | `choices[0].delta` |
| 结束信号 | 自然结束 | `[DONE]` 标记 |

## 启动流程

```
go run .
  │
  ├── 1. 解析命令行参数
  ├── 2. 加载 .env 文件
  ├── 3. 读取 API_KEY, MODEL_ID, BASE_URL
  ├── 4. 选择 LLM Provider (Anthropic 优先)
  ├── 5. 初始化 Shell Executor + Approval Policy
  ├── 6. 打印欢迎信息 + 配置摘要
  │
  └── 7. REPL 循环:
        用户输入 → Agent.Run() → 工具调用 → Shell 执行
                                     → LLM 回复 → 用户查看
```

## 安全设计

| 防护措施 | 说明 |
|----------|------|
| **批准机制** | 非只读命令默认询问用户（3 种模式可选） |
| **命令超时** | 默认 60 秒/命令，防止 hung process |
| **输出截断** | 16KB 上限，防止 context 被撑爆 |
| **参数校验** | tool 参数 JSON 无效时自动替换为空对象 |
| **cwd 安全** | `cd` 仅对独立命令生效，链式命令在子 shell 中执行 |

## 可扩展方向

- [ ] Prompt Caching 支持
- [ ] 文件读写专用工具
- [ ] 会话持久化 / 历史记录
- [ ] 网络错误退避重试
- [ ] MCP（Model Context Protocol）接入
- [ ] 多轮对话持久化到磁盘
- [ ] 支持更多 LLM Provider（Gemini、Ollama 等）
