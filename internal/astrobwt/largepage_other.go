//go:build !windows

package astrobwt

// Large-page backing is Windows-only for now; other platforms use ordinary
// heap allocation (Linux would take transparent huge pages implicitly).
func largePageAlloc(size uintptr) []byte { return nil }

// LargePagesActive reports whether at least one scratch allocation landed on
// large pages this process.
func LargePagesActive() bool { return false }
