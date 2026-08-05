//go:build v114stats

package astrobwt

import (
	"math/rand"
	"testing"
)

// TestFixupBranchCoverage asserts that a modest random corpus exercises every
// collision-fixup path in the stage-5 materializer at least a declared
// minimum number of times. A rare branch could otherwise never execute and
// still pass the differential gates, leaving its first real execution to
// production.
func TestFixupBranchCoverage(t *testing.T) {
	literalBefore := uint64(0)
	for i := range v114LiteralMergeGroups {
		literalBefore += v114LiteralMergeGroups[i].Load()
	}
	twoRunBefore := v114TwoRunMerges.Load()
	largeBefore := v114LargeFallbackMerges.Load()

	h := NewWithBackend(BackendV114)
	rng := rand.New(rand.NewSource(114))
	input := make([]byte, 48)
	const hashes = 200
	for i := 0; i < hashes; i++ {
		rng.Read(input)
		h.Hash(input)
	}

	literal := uint64(0)
	for i := range v114LiteralMergeGroups {
		literal += v114LiteralMergeGroups[i].Load()
	}
	literal -= literalBefore
	twoRun := v114TwoRunMerges.Load() - twoRunBefore
	large := v114LargeFallbackMerges.Load() - largeBefore

	// Sustained collection measured ~242 literal, ~79 two-run, and ~10
	// large-fallback groups per hash; require a small fraction of that so
	// the assertion is robust without being flaky.
	const min = uint64(hashes)
	if literal < min {
		t.Errorf("literal-group fixups: got %d, want >= %d", literal, min)
	}
	if twoRun < min {
		t.Errorf("two-run fixups: got %d, want >= %d", twoRun, min)
	}
	if large < min/10 {
		t.Errorf("large-fallback fixups: got %d, want >= %d", large, min/10)
	}
}
