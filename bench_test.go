package main

import (
	"encoding/hex"
	"math"
	"testing"
)

// Golden vectors independently derived from the xoshiro256++ reference
// (Blackman/Vigna) with Zig's Xoshiro256.fill byte order, seeds 12345+tid.
// Two thread ids are pinned so both the generator and the per-thread seeding
// are covered — with only tid 0, dropping `+tid` from the seed (making every
// thread grind an identical blob) would go undetected.
func TestBenchmarkWorkMatchesZigSeed(t *testing.T) {
	for _, tc := range []struct {
		tid  int
		want string
	}{
		{0, "68a5f8de828a948da002677953f97734698ddbe6fca2ca15d06d0cc25388ef2cd99c039cff3fff43873233724a8bc100"},
		{1, "54af2532d19f03021433fd9e5f2c7d2584300601dabc0463961f4466cdaf56251ed308d0712b2b092c736ddc82ae1a01"},
	} {
		work := benchmarkWork(tc.tid)
		if got := hex.EncodeToString(work[:]); got != tc.want {
			t.Errorf("benchmarkWork(%d) = %s, want %s", tc.tid, got, tc.want)
		}
	}
}

func TestRunStatBenchRejectsNonpositiveDuration(t *testing.T) {
	for _, secs := range []int{0, -1} {
		if got := runStatBench(nil, 1, secs, nil); got != 1 {
			t.Errorf("runStatBench(secs=%d) = %d, want 1", secs, got)
		}
	}
}

func TestRunSustainedRejectsBadDuration(t *testing.T) {
	// 0, negative, and values whose nanosecond conversion overflows int64 —
	// an overflowed window ends in milliseconds and reports a plausible rate
	for _, secs := range []int{0, -1, math.MaxInt64/int(1e9) + 1} {
		if got := runSustained(1, secs, false, 0, false); got != 1 {
			t.Errorf("runSustained(secs=%d) = %d, want 1", secs, got)
		}
	}
}
