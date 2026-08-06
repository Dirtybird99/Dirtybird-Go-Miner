package astrobwt

import (
	"math/rand"
	"sort"
	"testing"
)

// referenceOrder sorts group members with the production comparator — the
// oracle every constantRunOrder result must match exactly.
func referenceOrder(view *stage4View, positions []uint32) []uint32 {
	out := append([]uint32(nil), positions...)
	sort.Slice(out, func(i, j int) bool {
		return suffixLessAfterKey(view, out[i], out[j])
	})
	return out
}

func assertClosedFormMatches(t *testing.T, view *stage4View, members []uint32, label string) {
	t.Helper()
	got := append([]uint32(nil), members...)
	if !constantRunOrder(view, got) {
		t.Fatalf("%s: constantRunOrder declined a repeated-byte group", label)
	}
	want := referenceOrder(view, members)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: order mismatch at %d: got %v want %v", label, i, got, want)
		}
	}
}

// buildRunView makes a data buffer with a run of c at [start, end] inside
// non-c filler, padded for the 4-byte key loads.
func buildRunView(c, filler byte, start, end, logicalLen int) *stage4View {
	data := make([]byte, logicalLen+4)
	for i := 0; i < logicalLen; i++ {
		data[i] = filler
	}
	for i := start; i <= end && i < logicalLen; i++ {
		data[i] = c
	}
	return &stage4View{data: data, logicalLen: uint32(logicalLen)}
}

func runMembers(start, end int) []uint32 {
	// every position whose 3-byte key lies inside the run
	var m []uint32
	for p := start; p+2 <= end; p++ {
		m = append(m, uint32(p))
	}
	return m
}

func TestConstantRunOrderEdgeCases(t *testing.T) {
	// t > c: terminator byte above the run byte -> ascending by position
	v := buildRunView(0x40, 0x90, 100, 130, 400)
	assertClosedFormMatches(t, v, runMembers(100, 130), "t>c")

	// t < c: terminator below the run byte -> descending by position
	v = buildRunView(0x90, 0x40, 100, 130, 400)
	assertClosedFormMatches(t, v, runMembers(100, 130), "t<c")

	// run reaching logicalLen: shorter suffix wins -> descending
	v = buildRunView(0x55, 0x10, 380, 399, 400)
	assertClosedFormMatches(t, v, runMembers(380, 399), "run-to-end")

	// two runs of the same byte split by a gap: cross-run merge with real
	// compares; gap terminators on both sides of c
	v = buildRunView(0x60, 0x20, 60, 75, 400)
	for i := 200; i <= 215; i++ {
		v.data[i] = 0x60
	}
	v.data[216] = 0xF0 // second run's terminator above c
	members := append(runMembers(60, 75), runMembers(200, 215)...)
	assertClosedFormMatches(t, v, members, "split-runs")

	// minimal 17-member group at the threshold
	v = buildRunView(0x33, 0xCC, 50, 70, 400)
	m := runMembers(50, 70)
	if len(m) < 17 {
		t.Fatalf("bad fixture: %d members", len(m))
	}
	assertClosedFormMatches(t, v, m[:17], "count-17")

	// non-repeated-byte key must decline and leave the group untouched
	v = buildRunView(0x33, 0xCC, 50, 90, 400)
	v.data[51] = 0x34 // break the vvv key at the first member
	group := []uint32{50, 55, 60}
	saved := append([]uint32(nil), group...)
	if constantRunOrder(v, group) {
		t.Fatal("constantRunOrder accepted a non-repeated-byte key")
	}
	for i := range group {
		if group[i] != saved[i] {
			t.Fatal("declined group was modified")
		}
	}
}

// TestConstantRunOrderFuzz cross-checks the closed form against the
// production comparator over randomized run layouts, including runs that
// touch the end of the buffer and terminators on both sides of c.
func TestConstantRunOrderFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(114))
	for iter := 0; iter < 2000; iter++ {
		logicalLen := 300 + rng.Intn(200)
		c := byte(1 + rng.Intn(255))
		filler := byte(rng.Intn(256))
		for filler == c {
			filler = byte(rng.Intn(256))
		}
		v := buildRunView(c, filler, 0, -1, logicalLen) // filler only
		// scatter 1-3 runs of c, possibly touching logicalLen
		var members []uint32
		nRuns := 1 + rng.Intn(3)
		cursor := 5
		for r := 0; r < nRuns && cursor < logicalLen-5; r++ {
			runLen := 3 + rng.Intn(40)
			start := cursor + rng.Intn(20)
			end := start + runLen - 1
			if end >= logicalLen {
				end = logicalLen - 1
			}
			if start >= logicalLen-3 {
				break
			}
			for i := start; i <= end; i++ {
				v.data[i] = c
			}
			members = append(members, runMembers(start, end)...)
			cursor = end + 4 // keep a non-c gap between runs
		}
		if len(members) < 2 {
			continue
		}
		if len(members) > 32 {
			members = members[:32]
			// truncation can break run completeness only at the tail run's
			// end; the closed form still applies because the delta-1 chain
			// property and shared run-end scan hold for the kept members
		}
		// shuffle to simulate arbitrary arrival order
		rng.Shuffle(len(members), func(i, j int) { members[i], members[j] = members[j], members[i] })
		assertClosedFormMatches(t, v, members, "fuzz")
	}
}
