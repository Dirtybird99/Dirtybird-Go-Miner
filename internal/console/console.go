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
	mu sync.Mutex
	vt bool // stderr is a terminal with VT processing enabled
}

func New() *Console {
	// GOMINER_FORCE_STATUS=1 emits the status line even when stderr is not a
	// terminal (raw-capture verification; the zig miner always emits it).
	return &Console{vt: enableVT() || os.Getenv("GOMINER_FORCE_STATUS") == "1"}
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

// Status rewrites the transient status line (terminal only; no-op when piped
// so logs stay clean).
func (c *Console) Status(line string) {
	if !c.vt {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(os.Stderr, "\r\x1b[K%s", line)
}
