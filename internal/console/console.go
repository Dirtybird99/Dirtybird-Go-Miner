// Package console provides the family-style output: timestamped log lines on
// stderr plus a single self-rewriting status line (VT escapes) when stderr is
// a terminal, degrading to plain lines when piped.
package console

import (
	"fmt"
	"os"
	"sync"
	"time"
)

type Console struct {
	mu          sync.Mutex
	vt          bool // stderr is a terminal with VT processing enabled
	forceStatus bool // emit plain status records when stderr is redirected
}

func New() *Console {
	// GOMINER_FORCE_STATUS=1 emits the status line even when stderr is not a
	// terminal (HiveOS and raw-capture verification).
	return &Console{
		vt:          enableVT(),
		forceStatus: os.Getenv("GOMINER_FORCE_STATUS") == "1",
	}
}

// TerminalWidth returns stderr's current terminal width. Zero means stderr is
// redirected; 40 is the safe fallback when an interactive terminal cannot
// report its size.
func (c *Console) TerminalWidth() int {
	if !c.vt {
		return 0
	}
	if width, ok := terminalWidth(); ok {
		return width
	}
	return 40
}

const timeLayout = "02/01 15:04:05.000"

// Logf prints a permanent, timestamped line, clearing any status line first.
func (c *Console) Logf(level, format string, args ...interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.vt {
		fmt.Fprint(os.Stderr, "\r\x1b[K")
	}
	fmt.Fprintf(os.Stderr, "%s  %-5s %s\n", time.Now().Format(timeLayout), level, fmt.Sprintf(format, args...))
}

// Status rewrites the transient terminal line. Forced redirected output uses
// a separate plain, newline-terminated record so Hive logs contain no VT data.
func (c *Console) Status(terminalLine, plainLine string) {
	if !c.vt && !c.forceStatus {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.vt {
		fmt.Fprintf(os.Stderr, "\r\x1b[K%s", terminalLine)
		return
	}
	fmt.Fprintln(os.Stderr, plainLine)
}
