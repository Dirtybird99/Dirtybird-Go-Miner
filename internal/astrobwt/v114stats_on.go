//go:build v114stats

package astrobwt

import (
	"fmt"
	"io"
	"sync/atomic"
)

// V114StatsEnabled reports whether v114 descriptor counters are compiled in.
const V114StatsEnabled = true

const (
	v114Group1 = iota
	v114Group2
	v114Group3
	v114Group4
	v114Group5To8
	v114Group9To16
	v114Group17To25
	v114Group26Plus
	v114GroupBucketCount
)

const (
	v114Literal1 = iota
	v114Literal2To4
	v114Literal5To8
	v114Literal9To16
	v114Literal17To32
	v114LiteralBucketCount
)

var (
	v114GroupRuns           [v114GroupBucketCount]atomic.Uint64
	v114LiteralMergeGroups  [v114LiteralBucketCount]atomic.Uint64
	v114TwoRunMerges        atomic.Uint64
	v114LargeFallbackMerges atomic.Uint64
)

func v114StatsRecordGroup(groupCount uint32) {
	switch {
	case groupCount == 1:
		v114GroupRuns[v114Group1].Add(1)
	case groupCount == 2:
		v114GroupRuns[v114Group2].Add(1)
	case groupCount == 3:
		v114GroupRuns[v114Group3].Add(1)
	case groupCount == 4:
		v114GroupRuns[v114Group4].Add(1)
	case groupCount <= 8:
		v114GroupRuns[v114Group5To8].Add(1)
	case groupCount <= 16:
		v114GroupRuns[v114Group9To16].Add(1)
	case groupCount <= stage4ShortRunMax:
		v114GroupRuns[v114Group17To25].Add(1)
	default:
		v114GroupRuns[v114Group26Plus].Add(1)
	}
}

func v114StatsRecordLiteralGroup(count int) {
	switch {
	case count <= 1:
		v114LiteralMergeGroups[v114Literal1].Add(1)
	case count <= 4:
		v114LiteralMergeGroups[v114Literal2To4].Add(1)
	case count <= 8:
		v114LiteralMergeGroups[v114Literal5To8].Add(1)
	case count <= 16:
		v114LiteralMergeGroups[v114Literal9To16].Add(1)
	default:
		v114LiteralMergeGroups[v114Literal17To32].Add(1)
	}
}

func v114StatsRecordTwoRunMerge() {
	v114TwoRunMerges.Add(1)
}

func v114StatsRecordLargeFallbackMerge() {
	v114LargeFallbackMerges.Add(1)
}

// PrintV114Stats prints cumulative descriptor counters for benchmark runs.
func PrintV114Stats(w io.Writer) {
	groupLabels := [...]string{"1", "2", "3", "4", "5-8", "9-16", "17-25", "26+"}
	literalLabels := [...]string{"1", "2-4", "5-8", "9-16", "17-32"}

	fmt.Fprintln(w, "\nv114 descriptor stats:")
	fmt.Fprintln(w, "  full-group run counts:")
	for i, label := range groupLabels {
		fmt.Fprintf(w, "    %6s groups: %d\n", label, v114GroupRuns[i].Load())
	}
	fmt.Fprintln(w, "  equal-key merge fast paths:")
	for i, label := range literalLabels {
		fmt.Fprintf(w, "    literal %5s: %d\n", label, v114LiteralMergeGroups[i].Load())
	}
	fmt.Fprintf(w, "    two-run merge: %d\n", v114TwoRunMerges.Load())
	fmt.Fprintf(w, "    large fallback merge: %d\n", v114LargeFallbackMerges.Load())
	fmt.Fprintf(w, "    v114 fallback hashes: %d\n", V114Fallbacks())
}
