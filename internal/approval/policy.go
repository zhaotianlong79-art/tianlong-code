// Package approval decides whether a shell command may run automatically or
// needs the user's confirmation.
package approval

import "strings"

// Mode controls how aggressive auto-approval is.
type Mode int

const (
	// ModeAsk confirms anything that is not clearly read-only (default).
	ModeAsk Mode = iota
	// ModeAutoRead auto-runs read-only commands, asks for the rest.
	// (Same behaviour as ModeAsk today; kept distinct for future tuning.)
	ModeAutoRead
	// ModeYolo runs everything without asking. Use with care.
	ModeYolo
)

// String returns the canonical flag name for a Mode.
func (m Mode) String() string {
	switch m {
	case ModeYolo:
		return "yolo"
	case ModeAutoRead:
		return "read"
	default:
		return "ask"
	}
}

// ParseMode maps a string flag to a Mode.
func ParseMode(s string) Mode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "yolo", "auto", "all":
		return ModeYolo
	case "read", "auto-read":
		return ModeAutoRead
	default:
		return ModeAsk
	}
}

// readOnly is the set of first tokens we consider side-effect-free.
var readOnly = map[string]bool{
	"ls": true, "cat": true, "pwd": true, "echo": true, "grep": true,
	"find": true, "head": true, "tail": true, "wc": true, "which": true,
	"whoami": true, "date": true, "env": true, "printenv": true, "tree": true,
	"stat": true, "file": true, "du": true, "df": true, "ps": true, "uname": true,
	"go": true, "node": true, "python": true, "python3": true, // see gated subcommands below
}

// gitReadSubcommands are the git subcommands that don't mutate state.
var gitReadSubcommands = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true,
	"branch": true, "remote": true, "config": true, "rev-parse": true,
}

// NeedsConfirmation reports whether command must be confirmed under mode.
func NeedsConfirmation(mode Mode, command string) bool {
	switch mode {
	case ModeYolo:
		return false
	case ModeAsk, ModeAutoRead:
		return !isReadOnly(command)
	}
	return true
}

func isReadOnly(command string) bool {
	s := strings.TrimSpace(command)
	// Anything chaining or redirecting is treated as potentially mutating.
	if strings.ContainsAny(s, "|;&><`$(") || strings.Contains(s, "\n") {
		return false
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return false
	}
	cmd := fields[0]

	if cmd == "git" {
		return len(fields) >= 2 && gitReadSubcommands[fields[1]]
	}
	// Version/help probes are safe regardless of binary.
	if len(fields) >= 2 && (fields[1] == "--version" || fields[1] == "version" || fields[1] == "--help") {
		return true
	}
	// For interpreters, bare invocation reveals little; only allowlist read tools.
	switch cmd {
	case "go":
		return len(fields) >= 2 && (fields[1] == "version" || fields[1] == "env" || fields[1] == "vet" || fields[1] == "list")
	case "node", "python", "python3":
		return len(fields) >= 2 && (fields[1] == "--version" || fields[1] == "-V")
	}
	return readOnly[cmd]
}
