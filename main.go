// Command tianlong-agent is a lightweight, dependency-free coding agent that
// drives a Claude model and runs shell commands across macOS, Linux and Windows.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"tianlong-agent/internal/agent"
	"tianlong-agent/internal/approval"
	"tianlong-agent/internal/config"
	"tianlong-agent/internal/llm"
	"tianlong-agent/internal/shell"
	"tianlong-agent/internal/tools"

	"golang.org/x/term"
)

func main() {
	envFile := flag.String("env", ".env", "path to a .env file (ignored if missing)")
	modelFlag := flag.String("model", "", "model id (overrides MODEL_ID)")
	modeFlag := flag.String("approval", env("TIANLONG_APPROVAL", "ask"), "approval mode: ask | read | yolo")
	maxTokens := flag.Int("max-tokens", 4096, "max output tokens per response")
	contextWindow := flag.Int("context-window", envInt("TIANLONG_CONTEXT_WINDOW", 32768), "model context window in tokens; history is trimmed to fit (0 disables)")
	flag.Parse()

	config.LoadDotEnv(*envFile)

	apiKey := os.Getenv("API_KEY")
	model := firstNonEmpty(*modelFlag, os.Getenv("MODEL_ID"))
	anthropicURL := os.Getenv("Anthropic_BASE_URL")
	openaiURL := os.Getenv("OPENAI_BASE_URL")

	if apiKey == "" || model == "" || (anthropicURL == "" && openaiURL == "") {
		fmt.Fprintln(os.Stderr, "error: set API_KEY, MODEL_ID and one of Anthropic_BASE_URL / OPENAI_BASE_URL (via .env or environment)")
		os.Exit(1)
	}

	// Pick the protocol by which base URL is configured; Anthropic wins if both.
	var client llm.Provider
	var protocol string
	switch {
	case anthropicURL != "":
		client = llm.NewAnthropic(anthropicURL, apiKey, model, *maxTokens)
		protocol = "anthropic"
	default:
		client = llm.NewOpenAI(openaiURL, apiKey, model, *maxTokens)
		protocol = "openai"
	}

	exec, err := shell.New("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	mode := approval.ParseMode(*modeFlag)

	editor := newLineEditor()
	printer := newConsolePrinter()
	approve := makeApprover(&mode, editor)

	ag := agent.New(client, exec, approve, printer, *contextWindow, *maxTokens)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	info := collectSysInfo(exec.Shell(), exec.Cwd())
	printer.Banner([][2]string{
		{"Model", fmt.Sprintf("%s (%s)", client.Model(), protocol)},
		{"Approval", *modeFlag},
		{"OS", info.OS},
		{"CPU", info.CPU},
		{"Memory", info.Mem},
		{"Shell", info.Shell},
		{"Dir", info.Dir},
		{"User", info.User},
	})

	for {
		line, err := editor.ReadLine("you> ")
		if err != nil { // EOF (Ctrl-D) or read error
			fmt.Println()
			return
		}
		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}
		editor.AddHistory(input)

		if strings.HasPrefix(input, "/") {
			if handleCommand(input, &mode, ag, printer) {
				return
			}
			continue
		}

		if err := ag.Run(ctx, input); err != nil {
			if ctx.Err() != nil { // interrupted
				return
			}
			fmt.Fprintf(os.Stderr, "\n[error] %v\n\n", err)
			continue
		}
		fmt.Println()
	}
}

// handleCommand runs a slash command and reports whether the REPL should quit.
func handleCommand(input string, mode *approval.Mode, ag *agent.Agent, p *consolePrinter) (quit bool) {
	fields := strings.Fields(input)
	switch fields[0] {
	case "/exit", "/quit":
		return true
	case "/help":
		p.Help()
	case "/approval", "/mode":
		if len(fields) < 2 {
			p.Notice("approval mode: " + mode.String() + "  (usage: /approval ask|read|yolo)")
		} else {
			*mode = approval.ParseMode(fields[1])
			p.Notice("approval mode set to: " + mode.String())
		}
	case "/clear":
		ag.Reset()
		p.Notice("conversation history cleared")
	default:
		p.Notice("unknown command: " + fields[0] + "  (try /help)")
	}
	return false
}

// makeApprover returns an Approver that auto-allows safe commands and prompts
// the user for anything that needs confirmation. mode is read through a pointer
// so /approval can change it at runtime.
func makeApprover(mode *approval.Mode, editor *lineEditor) tools.Approver {
	return func(command string) (bool, string) {
		if !approval.NeedsConfirmation(*mode, command) {
			return true, ""
		}
		fmt.Printf("\n  proposed command:\n    %s\n", command)
		ans, err := editor.ReadLine("  run it? [y/N] ")
		if err != nil {
			return false, "no input"
		}
		switch strings.ToLower(strings.TrimSpace(ans)) {
		case "y", "yes":
			return true, ""
		default:
			return false, "User declined to run this command."
		}
	}
}

// ANSI colors, used only when stdout is a terminal.
const (
	cReset  = "\x1b[0m"
	cBold   = "\x1b[1m"
	cDim    = "\x1b[2m"
	cItalic = "\x1b[3m"
	cRed    = "\x1b[31m"
	cGreen  = "\x1b[32m"
	cCyan   = "\x1b[36m"
	cWhite  = "\x1b[97m"
)

// consolePrinter renders agent activity to stdout in a Claude-style layout:
// the assistant's text streams in, each tool call shows its command, and the
// result is marked with a colored status dot (green=ok, red=failed) followed
// by the output under a white dot.
type consolePrinter struct {
	streaming bool
	color     bool
	md        *mdRenderer
}

func newConsolePrinter() *consolePrinter {
	return &consolePrinter{color: term.IsTerminal(int(os.Stdout.Fd()))}
}

// paint wraps s in an ANSI color when output is a terminal.
func (p *consolePrinter) paint(code, s string) string {
	if !p.color {
		return s
	}
	return code + s + cReset
}

func (p *consolePrinter) AssistantDelta(text string) {
	if !p.streaming {
		fmt.Println() // blank line before the assistant's reply
		p.streaming = true
		if p.color {
			p.md = newMdRenderer(os.Stdout)
		}
	}
	if p.md != nil {
		p.md.Write(text) // render Markdown line by line
	} else {
		fmt.Print(text) // piped output stays as raw text
	}
}

func (p *consolePrinter) AssistantDone() {
	if !p.streaming {
		return
	}
	if p.md != nil {
		p.md.Flush()
		p.md = nil
	}
	fmt.Println()
	p.streaming = false
}

// Banner prints the startup header with aligned, dimmed labels.
func (p *consolePrinter) Banner(fields [][2]string) {
	fmt.Println(p.paint(cBold, "tianlong-agent"))
	fmt.Println()
	for _, f := range fields {
		fmt.Printf("  %s %s\n", p.paint(cDim, fmt.Sprintf("%-8s", f[0])), f[1])
	}
	fmt.Printf("\n%s\n\n", p.paint(cDim, "Type your request, or /exit to quit."))
}

func (p *consolePrinter) Notice(text string) {
	fmt.Printf("%s\n", p.paint(cDim, "» "+text))
}

func (p *consolePrinter) Help() {
	for _, l := range []string{
		"/help              show this help",
		"/approval <mode>   set approval mode: ask | read | yolo",
		"/clear             clear the conversation history",
		"/exit, /quit       quit",
	} {
		fmt.Printf("  %s\n", p.paint(cDim, l))
	}
}

func (p *consolePrinter) ToolStart(name, command string) {
	title := fmt.Sprintf("%s(%s)", toolLabel(name), oneLine(command, 120))
	fmt.Printf("\n%s %s\n", p.paint(cBold, "⏺"), p.paint(cBold, title))
}

func (p *consolePrinter) ToolEnd(exitCode int, output string, isError bool) {
	// Status dot: green for success, red for failure.
	dotColor := cGreen
	if isError {
		dotColor = cRed
	}
	fmt.Printf("  %s %s\n", p.paint(dotColor, "●"), p.paint(cDim, fmt.Sprintf("exit %d", exitCode)))

	output = strings.TrimRight(output, "\n")
	if output == "" {
		return
	}
	lines := strings.Split(output, "\n")
	count := len(lines)

	const maxLines = 50
	truncated := count > maxLines
	if truncated {
		lines = lines[:maxLines]
	}

	// Output under a white dot, headed by a line count.
	fmt.Printf("  %s %s\n", p.paint(cWhite, "●"), p.paint(cDim, plural(count, "line")))
	for _, l := range lines {
		fmt.Printf("    %s\n", p.paint(cDim, oneLine(l, 500)))
	}
	if truncated {
		fmt.Printf("    %s\n", p.paint(cDim, fmt.Sprintf("… +%d more lines", count-maxLines)))
	}
}

// toolLabel maps internal tool names to Claude-style display labels.
func toolLabel(name string) string {
	if name == "run_shell" {
		return "Bash"
	}
	return name
}

// oneLine collapses newlines and truncates to max runes for single-line display.
func oneLine(s string, max int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
