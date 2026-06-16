// Package shell runs commands across macOS, Linux and Windows with a uniform
// result type, per-command timeouts, output truncation and a persistent cwd.
package shell

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// maxOutput caps captured output so a stray `cat bigfile` cannot blow up the
// model context. We keep the head and tail and elide the middle.
const maxOutput = 16 * 1024

// Result is the uniform outcome of a command, regardless of platform.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
}

// Executor runs shell commands and tracks the working directory between calls.
type Executor struct {
	cwd        string
	shellPath  string
	shellArgs  []string // flags that precede the command string, e.g. ["-c"]
	defaultTTL time.Duration
}

// New picks an appropriate shell for the host OS and starts in dir (or the
// process cwd when dir is empty).
func New(dir string) (*Executor, error) {
	if dir == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			return nil, err
		}
	}
	path, args := detectShell()
	return &Executor{
		cwd:        dir,
		shellPath:  path,
		shellArgs:  args,
		defaultTTL: 60 * time.Second,
	}, nil
}

// detectShell returns the shell binary and the args that introduce a command
// string. On Windows we prefer PowerShell Core (pwsh) and fall back to the
// bundled Windows PowerShell.
func detectShell() (string, []string) {
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath("pwsh"); err == nil {
			return p, []string{"-NoLogo", "-NonInteractive", "-Command"}
		}
		return "powershell.exe", []string{"-NoLogo", "-NonInteractive", "-Command"}
	}
	// Prefer bash for richer builtins, fall back to POSIX sh.
	if p, err := exec.LookPath("bash"); err == nil {
		return p, []string{"-lc"}
	}
	return "/bin/sh", []string{"-c"}
}

// Describe returns a short human-readable string for the system prompt.
func (e *Executor) Describe() string {
	return fmt.Sprintf("OS=%s/%s shell=%s cwd=%s", runtime.GOOS, runtime.GOARCH, e.shellPath, e.cwd)
}

// Cwd returns the current working directory.
func (e *Executor) Cwd() string { return e.cwd }

// Shell returns the path of the shell binary in use.
func (e *Executor) Shell() string { return e.shellPath }

// Run executes command with a timeout (0 uses the default). A bare `cd <dir>`
// is handled specially so directory changes persist across calls.
func (e *Executor) Run(ctx context.Context, command string, timeout time.Duration) Result {
	if newDir, ok := bareCD(command); ok {
		return e.changeDir(newDir)
	}
	if timeout <= 0 {
		timeout = e.defaultTTL
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := append(append([]string{}, e.shellArgs...), command)
	cmd := exec.CommandContext(ctx, e.shellPath, args...)
	cmd.Dir = e.cwd

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res := Result{
		Stdout: truncate(stdout.String()),
		Stderr: truncate(stderr.String()),
	}
	if ctx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		res.ExitCode = -1
		return res
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
		} else {
			res.ExitCode = -1
			res.Stderr = strings.TrimSpace(res.Stderr + "\n" + err.Error())
		}
	}
	return res
}

// changeDir resolves and validates a directory, updating cwd on success.
func (e *Executor) changeDir(dir string) Result {
	if dir == "" || dir == "~" {
		dir, _ = os.UserHomeDir()
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(e.cwd, dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return Result{Stderr: fmt.Sprintf("cd: %v", err), ExitCode: 1}
	}
	if !info.IsDir() {
		return Result{Stderr: fmt.Sprintf("cd: not a directory: %s", dir), ExitCode: 1}
	}
	e.cwd = filepath.Clean(dir)
	return Result{Stdout: e.cwd}
}

// bareCD detects a standalone `cd <dir>` (no pipes/chains) so we can persist it.
func bareCD(command string) (string, bool) {
	s := strings.TrimSpace(command)
	if strings.ContainsAny(s, "&|;\n") {
		return "", false
	}
	if !strings.HasPrefix(s, "cd ") && s != "cd" {
		return "", false
	}
	arg := strings.TrimSpace(strings.TrimPrefix(s, "cd"))
	arg = strings.Trim(arg, `"'`)
	return arg, true
}

// truncate keeps the head and tail of long output and notes how much was cut.
func truncate(s string) string {
	if len(s) <= maxOutput {
		return s
	}
	half := maxOutput / 2
	cut := len(s) - maxOutput
	return s[:half] + fmt.Sprintf("\n\n... [%d bytes truncated] ...\n\n", cut) + s[len(s)-half:]
}
