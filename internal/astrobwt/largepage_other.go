//go:build !windows || nolargepages

package astrobwt

// Large-page backing is Windows-only for now, and off under -tags
// nolargepages; everything else uses ordinary heap allocation (Linux takes
// transparent huge pages implicitly).
func largePageRegion(size int) ([]byte, func()) { return nil, nil }

// LargePagesActive reports whether at least one scratch region landed on
// large pages in this process.
func LargePagesActive() bool { return false }
