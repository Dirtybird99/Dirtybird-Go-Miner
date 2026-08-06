//go:build stagestats

package astrobwt

import "testing"

// TestStageStatsHashAccounting pins the per-hash normalization: HashPair
// hashes two streams so it must add two to the hash counter, Hash adds one.
// A mismatch silently skews every per-stage cyc/hash figure derived from
// StageSnapshot.
func TestStageStatsHashAccounting(t *testing.T) {
	h := NewWithBackend(BackendV114)
	var a, b [48]byte
	b[0] = 1

	_, _, _, _, before := StageSnapshot()
	const n = 5
	for i := 0; i < n; i++ {
		h.Hash(a[:])
	}
	_, _, _, _, afterSingles := StageSnapshot()
	if got := afterSingles - before; got != n {
		t.Errorf("hash count after %d Hash calls: got %d, want %d", n, got, n)
	}
	for i := 0; i < n; i++ {
		h.HashPair(a[:], b[:])
	}
	_, _, _, _, afterPairs := StageSnapshot()
	if got := afterPairs - afterSingles; got != 2*n {
		t.Errorf("hash count after %d HashPair calls: got %d, want %d", n, got, 2*n)
	}
}
