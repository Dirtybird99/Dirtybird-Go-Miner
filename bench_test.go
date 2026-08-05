package main

import (
	"encoding/hex"
	"testing"
)

func TestBenchmarkWorkMatchesZigSeed(t *testing.T) {
	work := benchmarkWork(0)
	const want = "68a5f8de828a948da002677953f97734698ddbe6fca2ca15d06d0cc25388ef2cd99c039cff3fff43873233724a8bc100"
	if got := hex.EncodeToString(work[:]); got != want {
		t.Fatalf("benchmark blob = %s, want %s", got, want)
	}
}

func TestRunStatBenchRejectsNonpositiveDuration(t *testing.T) {
	for _, secs := range []int{0, -1} {
		if got := runStatBench(nil, 1, secs, nil); got != 1 {
			t.Errorf("runStatBench(secs=%d) = %d, want 1", secs, got)
		}
	}
}
