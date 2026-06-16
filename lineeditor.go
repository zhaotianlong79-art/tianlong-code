package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// lineEditor reads input lines. On a real terminal it switches to raw mode for
// the duration of each read and runs its own editing loop so it can handle
// double-width (CJK) characters correctly — golang.org/x/term's own Terminal
// assumes every rune is one column wide, which corrupts the display when
// editing lines containing 中文. We only borrow its reliable cross-platform
// MakeRaw/Restore. Raw mode is entered only while reading and restored
// immediately after, so the agent runs in cooked mode (Ctrl-C interrupts,
// streamed "\n" renders correctly). Piped/non-terminal input falls back to a
// plain buffered read.
type lineEditor struct {
	fd      int
	isTTY   bool
	buf     *bufio.Reader
	history []string
}

func newLineEditor() *lineEditor {
	fd := int(os.Stdin.Fd())
	return &lineEditor{
		fd:    fd,
		isTTY: term.IsTerminal(fd),
		buf:   bufio.NewReader(os.Stdin),
	}
}

// AddHistory records a submitted line for up/down arrow recall, skipping blanks
// and consecutive duplicates.
func (le *lineEditor) AddHistory(s string) {
	if s == "" {
		return
	}
	if n := len(le.history); n > 0 && le.history[n-1] == s {
		return
	}
	le.history = append(le.history, s)
}

// ReadLine prints prompt and returns one line without the trailing newline.
// It returns io.EOF when the user signals end-of-input (Ctrl-D on an empty
// line). Ctrl-C abandons the current line and returns an empty string.
func (le *lineEditor) ReadLine(prompt string) (string, error) {
	if !le.isTTY {
		fmt.Print(prompt)
		line, err := le.buf.ReadString('\n')
		return strings.TrimRight(line, "\r\n"), err
	}

	oldState, err := term.MakeRaw(le.fd)
	if err != nil {
		// Could not enter raw mode; degrade to a plain buffered read.
		fmt.Print(prompt)
		line, err := le.buf.ReadString('\n')
		return strings.TrimRight(line, "\r\n"), err
	}
	defer term.Restore(le.fd, oldState)

	return le.edit(prompt)
}

// editKey is a decoded special key from an ANSI escape sequence.
type editKey int

const (
	keyNone editKey = iota
	keyLeft
	keyRight
	keyHome
	keyEnd
	keyUp
	keyDown
	keyDelete
)

// edit runs the interactive editing loop in raw mode.
func (le *lineEditor) edit(prompt string) (string, error) {
	var line []rune
	pos := 0                   // cursor index within line
	histPos := len(le.history) // index into history; len == the live draft
	var draft []rune           // the in-progress line saved when browsing history

	render := func() {
		var b strings.Builder
		b.WriteString("\r")         // column 0
		b.WriteString(prompt)       // reprint prompt
		b.WriteString(string(line)) // current contents
		b.WriteString("\x1b[K")     // erase any leftover to the right
		if trail := width(line[pos:]); trail > 0 {
			fmt.Fprintf(&b, "\x1b[%dD", trail) // move cursor back to pos
		}
		os.Stdout.WriteString(b.String())
	}

	setLine := func(s string) {
		line = []rune(s)
		pos = len(line)
		render()
	}

	render()
	for {
		r, _, err := le.buf.ReadRune()
		if err != nil {
			os.Stdout.WriteString("\r\n")
			return string(line), err
		}

		switch r {
		case '\r', '\n':
			os.Stdout.WriteString("\r\n")
			return string(line), nil

		case 0x03: // Ctrl-C: abandon the line
			os.Stdout.WriteString("^C\r\n")
			return "", nil

		case 0x04: // Ctrl-D: EOF only when the line is empty
			if len(line) == 0 {
				os.Stdout.WriteString("\r\n")
				return "", io.EOF
			}

		case 0x7f, 0x08: // Backspace / Ctrl-H: delete rune before cursor
			if pos > 0 {
				line = append(line[:pos-1], line[pos:]...)
				pos--
				render()
			}

		case 0x01: // Ctrl-A: start of line
			pos = 0
			render()

		case 0x05: // Ctrl-E: end of line
			pos = len(line)
			render()

		case 0x15: // Ctrl-U: clear to start of line
			line = append([]rune{}, line[pos:]...)
			pos = 0
			render()

		case 0x0b: // Ctrl-K: clear to end of line
			line = line[:pos]
			render()

		case 0x1b: // escape sequence (arrows, Home/End, Delete)
			switch le.readEscape() {
			case keyLeft:
				if pos > 0 {
					pos--
					render()
				}
			case keyRight:
				if pos < len(line) {
					pos++
					render()
				}
			case keyHome:
				pos = 0
				render()
			case keyEnd:
				pos = len(line)
				render()
			case keyDelete:
				if pos < len(line) {
					line = append(line[:pos], line[pos+1:]...)
					render()
				}
			case keyUp:
				if histPos > 0 {
					if histPos == len(le.history) {
						draft = append([]rune{}, line...) // save the live draft
					}
					histPos--
					setLine(le.history[histPos])
				}
			case keyDown:
				if histPos < len(le.history) {
					histPos++
					if histPos == len(le.history) {
						line = append([]rune{}, draft...)
						pos = len(line)
						render()
					} else {
						setLine(le.history[histPos])
					}
				}
			}

		default:
			if r >= 0x20 { // printable rune
				line = append(line, 0)
				copy(line[pos+1:], line[pos:])
				line[pos] = r
				pos++
				render()
			}
		}
	}
}

// readEscape consumes the remainder of an ANSI escape sequence (the ESC byte
// has already been read) and returns the decoded key.
func (le *lineEditor) readEscape() editKey {
	r, _, err := le.buf.ReadRune()
	if err != nil || (r != '[' && r != 'O') {
		return keyNone
	}
	r2, _, err := le.buf.ReadRune()
	if err != nil {
		return keyNone
	}
	switch r2 {
	case 'A':
		return keyUp
	case 'B':
		return keyDown
	case 'C':
		return keyRight
	case 'D':
		return keyLeft
	case 'H':
		return keyHome
	case 'F':
		return keyEnd
	}
	if r2 >= '0' && r2 <= '9' {
		// Extended sequence like ESC[3~ (Delete), ESC[1~ (Home), ESC[4~ (End).
		code := string(r2)
		for {
			n, _, e := le.buf.ReadRune()
			if e != nil || n == '~' {
				break
			}
			code += string(n)
		}
		switch code {
		case "3":
			return keyDelete
		case "1", "7":
			return keyHome
		case "4", "8":
			return keyEnd
		}
	}
	return keyNone
}

// width returns the display column count of a rune slice, treating East Asian
// wide / fullwidth characters as 2 columns and combining marks as 0.
func width(rs []rune) int {
	w := 0
	for _, r := range rs {
		w += runeWidth(r)
	}
	return w
}

func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case (r >= 0x0300 && r <= 0x036F), // combining diacritical marks
		(r >= 0x1AB0 && r <= 0x1AFF),
		(r >= 0x1DC0 && r <= 0x1DFF),
		(r >= 0x20D0 && r <= 0x20FF),
		(r >= 0xFE20 && r <= 0xFE2F):
		return 0
	case isWide(r):
		return 2
	default:
		return 1
	}
}

// isWide reports whether r occupies two terminal columns (CJK, Kana, Hangul,
// fullwidth forms, most emoji). This is an approximation of Unicode East Asian
// Width that covers the common cases.
func isWide(r rune) bool {
	return (r >= 0x1100 && r <= 0x115F) || // Hangul Jamo
		(r >= 0x2E80 && r <= 0x303E) || // CJK radicals, Kangxi, punctuation
		(r >= 0x3041 && r <= 0x33FF) || // Hiragana, Katakana, CJK symbols
		(r >= 0x3400 && r <= 0x4DBF) || // CJK Extension A
		(r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0xA000 && r <= 0xA4CF) || // Yi
		(r >= 0xAC00 && r <= 0xD7A3) || // Hangul Syllables
		(r >= 0xF900 && r <= 0xFAFF) || // CJK Compatibility Ideographs
		(r >= 0xFE30 && r <= 0xFE4F) || // CJK Compatibility Forms
		(r >= 0xFF00 && r <= 0xFF60) || // Fullwidth Forms
		(r >= 0xFFE0 && r <= 0xFFE6) || // Fullwidth signs
		(r >= 0x1F300 && r <= 0x1FAFF) || // emoji & pictographs
		(r >= 0x20000 && r <= 0x3FFFD) // CJK Extension B+
}
