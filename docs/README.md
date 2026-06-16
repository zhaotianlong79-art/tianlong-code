# Tianlong Agent

> [English](../README.md) | **中文文档**

一个轻量级、**零第三方依赖**（纯 Go 标准库）的命令行编码 Agent，驱动 Claude 模型并在 **macOS**、**Linux** 和 **Windows** 上执行 Shell 命令。

> 灵感来源于 [OpenAI Codex](https://openai.com/index/codex) 的 Agent 编程范式 — 基于 ReAct（Reason + Act）、通过 LLM 工具调用 API 让大模型自主执行 Shell 命令完成任务，并在人类监督下安全运行。

---

## ✨ 核心特性

| 特性 | 说明 |
|------|------|
| **零第三方依赖** | 仅使用 Go 标准库（加 `golang.org/x/term`） |
| **双 LLM 后端** | 支持 Anthropic Messages API 和 OpenAI Chat Completions API（按配置自动选择） |
| **SSE 流式输出** | Server-Sent Events 实现实时文本和工具调用流式传输 |
| **跨平台 Shell** | 自动检测 `bash`/`sh`（macOS/Linux）或 `pwsh`/`powershell`（Windows） |
| **人工审批机制** | 三种模式：`ask`（默认）、`read` 或 `yolo`（全自动） |
| **持久化工作目录** | `cd <dir>` 在 Agent 多次迭代中持久生效 |
| **内置 Markdown 渲染** | 终端内流式 ANSI 彩色 Markdown 输出 |
| **斜杠命令** | `/help`、`/approval <mode>`、`/clear`、`/exit` |
| **上下文窗口管理** | 自动裁剪对话历史以适配可配置的 Token 预算 |
| **命令超时与截断** | 默认每个命令 60 秒超时，输出上限 16 KB |

---

## 📐 项目架构

```
tianlong-agent/
├── main.go                     # CLI / REPL 入口、Provider 选择、输出渲染
├── lineeditor.go               # 带历史记录的行编辑器（上下箭头翻页）
├── markdown.go                 # 极简 ANSI Markdown 渲染器
├── sysinfo.go                  # OS / CPU / Shell / 目录信息收集
├── go.mod                      # 模块定义 (Go 1.26)
│
└── internal/
    ├── agent/agent.go          # ReAct 主循环与对话状态管理
    ├── approval/policy.go      # 命令安全分类与审批模式
    ├── config/dotenv.go        # 极简 .env 加载器（纯标准库）
    ├── llm/
    │   ├── types.go            # Provider 无关的消息/工具类型 + Provider 接口
    │   ├── anthropic.go        # Anthropic Messages API 实现
    │   ├── openai.go           # OpenAI Chat Completions API 实现
    │   └── http.go             # 共享 SSE 流式 HTTP 客户端
    ├── shell/executor.go       # 跨平台 Shell 执行引擎
    └── tools/tools.go          # 工具定义 (run_shell) 与分发
```

### ReAct 循环（简化版）

```
用户输入
  → 追加 user 消息到历史
  → 循环（最多 25 次）:
      → 流式获取 LLM 回复（文本 + 工具调用）
      → 追加 assistant 消息到历史
      → 若无工具调用 → 完成
      → 对每个工具调用:
          → 审批（非 yolo 模式）
          → 通过 shell.Executor 执行
          → 追加工具结果到历史
  → 达到最大迭代次数 → 报错
```

---

## 🚀 快速上手

### 前置要求

- **Go 1.26+**
- 一个 Anthropic 兼容或 OpenAI 兼容的 API 端点和密钥
- 支持 ANSI 颜色的终端

### 1. 克隆 & 配置

```bash
git clone <your-repo-url>
cd tianlong-agent
cp .env.example .env
```

编辑 `.env` 填入你的凭据：

```ini
# 通过设置 BASE URL 选择协议（注释掉另一个）。
Anthropic_BASE_URL=https://your-endpoint.example.com/anthropic
# OPENAI_BASE_URL=https://your-endpoint.example.com/v2

API_KEY=your-api-key-here
MODEL_ID=your-model-id
```

> **⚠️ `.env` 已被 gitignore — 切勿提交真实密钥。**

### 2. 运行

```bash
go run .
```

或通过命令行标志覆盖：

```bash
go run . -model claude-sonnet-4-20250514 -approval yolo
```

### 3. 交互

你会看到配置信息横幅，然后出现 REPL 提示符：

```
tianlong-agent

  Model      claude-sonnet-4-20250514 (anthropic)
  Approval   ask
  OS         darwin arm64
  ...

Type your request, or /exit to quit.

you> 
```

输入自然语言请求 — Agent 会思考、调用工具（执行 Shell 命令）、然后回复：

```
you> 列出 /tmp 下按大小排序的前 5 个文件

⏺ Bash(list the top 5 files by size in /tmp)
  ● exit 0
  ● 5 lines

以下是 /tmp 中最大的 5 个文件 ...
```

### 斜杠命令

| 命令 | 说明 |
|------|------|
| `/help` | 显示帮助信息 |
| `/approval ask \| read \| yolo` | 切换审批模式 |
| `/clear` | 清除对话历史 |
| `/exit`, `/quit` | 退出 Agent |

---

## ⚙️ 配置说明

### 环境变量

| 变量 | 必填 | 默认值 | 说明 |
|------|------|--------|------|
| `API_KEY` | ✅ | — | LLM 提供商 API 密钥 |
| `MODEL_ID` | ✅ | — | 要使用的模型标识 |
| `Anthropic_BASE_URL` | ⚠️* | — | Anthropic 兼容的基础 URL |
| `OPENAI_BASE_URL` | ⚠️* | — | OpenAI 兼容的基础 URL |
| `TIANLONG_CONTEXT_WINDOW` | ❌ | `32768` | 上下文窗口 Token 数上限（0 = 无限制） |
| `TIANLONG_APPROVAL` | ❌ | `ask` | 默认审批模式 |

*\* 至少设置一个。若两者都设置，Anthropic 优先。*

### 命令行标志

| 标志 | 默认值 | 说明 |
|------|--------|------|
| `-env` | `.env` | `.env` 文件路径 |
| `-model` | (来自环境变量) | 模型 ID 覆盖 |
| `-approval` | `ask` | 审批模式：`ask`、`read` 或 `yolo` |
| `-max-tokens` | `4096` | 每次 LLM 回复的最大输出 Token 数 |
| `-context-window` | `32768` | 上下文窗口大小（Token） |

---

## 🛡️ 安全设计

| 防护措施 | 说明 |
|----------|------|
| **审批机制** | 非只读命令默认询问用户（3 种模式可选） |
| **命令超时** | 默认每个命令 60 秒，防止进程挂起 |
| **输出截断** | 单命令输出上限 16 KB，保护上下文窗口 |
| **参数校验** | 无效工具 JSON 自动替换为空对象 |
| **cwd 安全** | `cd` 仅对独立命令生效，链式/管道命令在子 Shell 中执行 |

### 审批模式

| 模式 | 行为 |
|------|------|
| `ask`（默认） | 自动放行只读命令，其余询问 |
| `read` | 与 `ask` 相同（预留供未来微调） |
| `yolo` | 全自动执行，不询问 — 谨慎使用 |

### 只读判定规则

Agent 将命令分类为只读（自动放行）的条件：

- 以白名单命令开头：`ls`、`cat`、`pwd`、`grep`、`find`、`head`、`tail`、`wc`、`which` 等
- 是 Git 只读子命令：`status`、`log`、`diff`、`show`、`branch` 等
- 探测版本/帮助：`--version`、`--help`、`version`

包含管道 `|`、重定向 `>`、链式 `;&` 或反引号的命令始终被视为可能修改。

---

## 🔌 Provider 兼容性对比

| 维度 | Anthropic | OpenAI |
|------|-----------|--------|
| 路径后缀 | `/v1/messages` | `/chat/completions` |
| 认证头 | `x-api-key` | `Authorization: Bearer` |
| 工具类型 | `tool_use` / `tool_result` | `function` / `tool_call_id` |
| 系统消息 | 顶层 `system` 字段 | `system` 角色消息 |
| 流式事件 | `content_block_delta` | `choices[0].delta` |
| 结束信号 | 自然结束 | `[DONE]` 标记 |

---

## 🗺️ 开发计划

- [ ] Prompt Caching 支持
- [ ] 文件读写专用工具
- [ ] 会话持久化 / 历史记录
- [ ] 网络错误退避重试
- [ ] MCP（Model Context Protocol）接入
- [ ] 多轮对话持久化到磁盘
- [ ] 支持更多 LLM 提供商（Gemini、Ollama 等）

---

## 📄 许可证

本项目基于 [MIT 许可证](LICENSE) 条款提供。

---

## 🙏 致谢

灵感来源于 [OpenAI Codex](https://openai.com/index/codex) 以及更广泛的 Agent 编程研究领域。
