package main

import (
	"fmt"
	"io"
	"regexp"
	"strings"
)

// mdRenderer turns Markdown into ANSI-styled terminal output, line by line, so
// it works with streamed text. Each completed line is rendered as it arrives;
// the trailing partial line is rendered on Flush. It is intentionally a small
// approximation (no third-party Markdown engine): headers, bold, italic, inline
// code, fenced code blocks, blockquotes, bullet lists and table separators.
type mdRenderer struct {
	w      io.Writer
	buf    strings.Builder // current partial line
	inCode bool            // inside a ``` fence
}

func newMdRenderer(w io.Writer) *mdRenderer {
	return &mdRenderer{w: w}
}

// Write feeds streamed text; complete lines are flushed immediately.
func (m *mdRenderer) Write(s string) {
	for _, r := range s {
		if r == '\n' {
			fmt.Fprintln(m.w, m.renderLine(m.buf.String()))
			m.buf.Reset()
		} else {
			m.buf.WriteRune(r)
		}
	}
}

// Flush renders any remaining partial line.
func (m *mdRenderer) Flush() {
	if m.buf.Len() > 0 {
		fmt.Fprintln(m.w, m.renderLine(m.buf.String()))
		m.buf.Reset()
	}
}

var (
	reCodeSpan    = regexp.MustCompile("`([^`]+)`")
	reBold        = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reItalicStar  = regexp.MustCompile(`\*([^*]+)\*`)
	reItalicUnder = regexp.MustCompile(`_([^_]+)_`)
)

func (m *mdRenderer) renderLine(line string) string {
	trimmed := strings.TrimSpace(line)

	// Fenced code block: toggle on ``` and render contents verbatim (colored).
	if strings.HasPrefix(trimmed, "```") {
		m.inCode = !m.inCode
		return cDim + line + cReset
	}
	if m.inCode {
		return cCyan + line + cReset
	}
	if trimmed == "" {
		return ""
	}

	// Headers: # .. ###### → bold cyan, hashes stripped.
	if n := headerPrefix(trimmed); n > 0 {
		return cBold + cCyan + strings.TrimSpace(trimmed[n:]) + cReset
	}

	// Blockquote.
	if rest, ok := strings.CutPrefix(trimmed, "> "); ok {
		return cDim + "▏ " + cReset + styleInline(rest)
	}

	// Table separator row like |---|:--:| → dimmed.
	if isTableSeparator(trimmed) {
		return cDim + line + cReset
	}

	// Bullet list: -, *, + → •, preserving indentation.
	indent, rest := leadingSpace(line)
	if b, ok := bulletRest(rest); ok {
		return indent + cCyan + "•" + cReset + " " + styleInline(b)
	}

	return styleInline(line)
}

// styleInline applies bold/italic/inline-code styling within a line. Code spans
// are protected first so their contents aren't re-styled.
func styleInline(s string) string {
	var codes []string
	s = reCodeSpan.ReplaceAllStringFunc(s, func(match string) string {
		codes = append(codes, cCyan+match[1:len(match)-1]+cReset)
		return fmt.Sprintf("\x00%d\x00", len(codes)-1)
	})
	s = reBold.ReplaceAllString(s, cBold+"$1"+cReset)
	s = reItalicStar.ReplaceAllString(s, cItalic+"$1"+cReset)
	s = reItalicUnder.ReplaceAllString(s, cItalic+"$1"+cReset)
	for i, c := range codes {
		s = strings.ReplaceAll(s, fmt.Sprintf("\x00%d\x00", i), c)
	}
	return s
}

func headerPrefix(s string) int {
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	if n >= 1 && n <= 6 && n < len(s) && s[n] == ' ' {
		return n
	}
	return 0
}

func bulletRest(s string) (string, bool) {
	for _, p := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(s, p) {
			return s[len(p):], true
		}
	}
	return "", false
}

func leadingSpace(s string) (string, string) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[:i], s[i:]
}

func isTableSeparator(s string) bool {
	if !strings.Contains(s, "-") || !strings.Contains(s, "|") {
		return false
	}
	for _, r := range s {
		switch r {
		case '|', '-', ':', ' ':
		default:
			return false
		}
	}
	return true
}
