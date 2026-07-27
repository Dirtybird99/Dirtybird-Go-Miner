// Package console provides the family-style output: timestamped log lines on
// stderr plus a single self-rewriting status line (VT escapes) when stderr is
// a terminal, degrading to plain newline-terminated records when piped.
package console

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

type Console struct {
	mu sync.Mutex
	vt bool // stderr is a terminal with VT processing enabled
}

func New() *Console {
	return &Console{vt: enableVT()}
}

// VT reports whether stderr is an interactive terminal, i.e. in-place
// repaint and color are available.
func (c *Console) VT() bool { return c.vt }

// TerminalWidth returns stderr's current terminal width. Zero means stderr
// is redirected. On a terminal, DIRTYBIRD_COLS overrides the probe (the
// family's testing hook), and 80 is the fallback when the terminal cannot
// report its size — Git Bash/mintty pty handles fail the query, and assuming
// narrow would silently discard status fields nobody asked to lose.
func (c *Console) TerminalWidth() int {
	if !c.vt {
		return 0
	}
	if v, ok := parseColsOverride(os.Getenv("DIRTYBIRD_COLS")); ok {
		return v
	}
	if width, ok := terminalWidth(); ok {
		return width
	}
	return 80
}

// parseColsOverride validates a DIRTYBIRD_COLS value: 0 < v < 10000.
func parseColsOverride(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	v, err := strconv.Atoi(s)
	if err != nil || v <= 0 || v >= 10000 {
		return 0, false
	}
	return v, true
}

const timeLayout = "02/01 15:04:05.000"

// formatRecord renders one timestamped log record: DD/MM HH:MM:SS.mmm, two
// spaces, the level padded to five columns, one space, message. The %-5s
// means INFO/WARN get two spaces before the message and ERROR gets one.
func formatRecord(now time.Time, level, msg string) string {
	return fmt.Sprintf("%s  %-5s %s\n", now.Format(timeLayout), level, msg)
}

// Logf prints a permanent, timestamped line, clearing any status line first.
func (c *Console) Logf(level, format string, args ...interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.vt {
		fmt.Fprint(os.Stderr, "\r\x1b[K")
	}
	fmt.Fprint(os.Stderr, formatRecord(time.Now(), level, fmt.Sprintf(format, args...)))
}

// Status rewrites the transient terminal line in place. On a redirected
// stream it appends plainLine as a newline-terminated record instead —
// family parity: the C and Rust miners emit one plain record per tick when
// redirected, and HiveOS h-run.sh consumes exactly that (its
// GOMINER_FORCE_STATUS=1 export is accepted but no longer needed). The
// caller guarantees plainLine carries no escapes.
func (c *Console) Status(terminalLine, plainLine string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.vt {
		fmt.Fprintf(os.Stderr, "\r\x1b[K%s", terminalLine)
		return
	}
	fmt.Fprintln(os.Stderr, plainLine)
}
