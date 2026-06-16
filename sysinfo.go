package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// sysInfo is a best-effort snapshot of the host for the startup banner.
type sysInfo struct {
	OS, CPU, Mem, Shell, Dir, User string
}

func collectSysInfo(shell, dir string) sysInfo {
	return sysInfo{
		OS:    osDescription(),
		CPU:   cpuDescription(),
		Mem:   memDescription(),
		Shell: shell,
		Dir:   dir,
		User:  userDescription(),
	}
}

// runCmd runs a short command and returns its trimmed stdout, or "" on failure.
func runCmd(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func osDescription() string {
	base := fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
	switch runtime.GOOS {
	case "darwin":
		if v := runCmd("sw_vers", "-productVersion"); v != "" {
			return fmt.Sprintf("macOS %s (%s)", v, base)
		}
	case "linux":
		if name := osReleasePretty(); name != "" {
			return fmt.Sprintf("%s (%s)", name, base)
		}
	}
	return base
}

func cpuDescription() string {
	cores := runtime.NumCPU()
	var model string
	switch runtime.GOOS {
	case "darwin":
		model = runCmd("sysctl", "-n", "machdep.cpu.brand_string")
	case "linux":
		model = procCPUModel()
	case "windows":
		model = os.Getenv("PROCESSOR_IDENTIFIER")
	}
	if model != "" {
		return fmt.Sprintf("%s · %d cores", model, cores)
	}
	return fmt.Sprintf("%d cores", cores)
}

func memDescription() string {
	var bytes uint64
	switch runtime.GOOS {
	case "darwin":
		if s := runCmd("sysctl", "-n", "hw.memsize"); s != "" {
			bytes, _ = strconv.ParseUint(s, 10, 64)
		}
	case "linux":
		bytes = procMemTotal()
	}
	if bytes == 0 {
		return "unknown"
	}
	return fmt.Sprintf("%.0f GB", float64(bytes)/(1024*1024*1024))
}

func userDescription() string {
	name := os.Getenv("USER")
	if name == "" {
		name = os.Getenv("USERNAME") // Windows
	}
	host, _ := os.Hostname()
	switch {
	case name != "" && host != "":
		return name + "@" + host
	case host != "":
		return host
	default:
		return name
	}
}

// osReleasePretty returns PRETTY_NAME from /etc/os-release (Linux).
func osReleasePretty() string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

// procCPUModel returns the first "model name" from /proc/cpuinfo (Linux).
func procCPUModel() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "model name") {
			if _, v, ok := strings.Cut(sc.Text(), ":"); ok {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

// procMemTotal returns total RAM in bytes from /proc/meminfo (Linux).
func procMemTotal() uint64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kb, _ := strconv.ParseUint(fields[1], 10, 64)
			return kb * 1024
		}
	}
	return 0
}
