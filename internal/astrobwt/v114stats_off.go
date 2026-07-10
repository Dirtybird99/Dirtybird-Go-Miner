//go:build !v114stats

package astrobwt

import "io"

// V114StatsEnabled reports whether v114 descriptor counters are compiled in.
const V114StatsEnabled = false

func v114StatsRecordGroup(uint32)        {}
func v114StatsRecordLiteralGroup(int)    {}
func v114StatsRecordTwoRunMerge()        {}
func v114StatsRecordLargeFallbackMerge() {}

// PrintV114Stats is a no-op unless built with -tags v114stats.
func PrintV114Stats(io.Writer) {}
