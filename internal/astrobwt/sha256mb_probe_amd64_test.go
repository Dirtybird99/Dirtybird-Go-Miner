//go:build amd64 && shaprobe

// Probe only. Compiled solely under -tags shaprobe, never shipped, and
// deliberately test-only so no production file carries probe code.

package astrobwt

import (
	"slices"
	"testing"
)

func probeRdtscp() uint64

// probeBlocks is the block count per timed call: large enough that call
// overhead and the RDTSCP pair are noise, small enough to stay in L2.
const probeBlocks = 512

// medianU64 returns the median of s, sorting s in place.
func medianU64(s []uint64) uint64 {
	slices.Sort(s)
	return s[len(s)/2]
}

// cyclesPerCall runs f reps times and returns the median elapsed count.
//
// The unit is invariant-TSC ticks, not core cycles: the TSC advances at a
// fixed base rate while the core clock turbos well above it. A ratio between
// two counts taken close together is unit-free and is what this is for; a
// lone absolute means cycles only after scaling by the core:TSC clock ratio.
func cyclesPerCall(reps int, f func()) uint64 {
	samples := make([]uint64, reps)
	for i := range samples {
		start := probeRdtscp()
		f()
		samples[i] = probeRdtscp() - start
	}
	return medianU64(samples)
}

// thueMorseLegs is the leg order ABBABAAB, arm A as 0 and arm B as 1.
//
// Timing arm A to completion and then arm B hands any frequency ramp between
// the two blocks entirely to the second arm, which manufactures a speedup
// that will not repeat. Interleaving at leg granularity makes the drift
// common-mode across both arms, and the Thue-Morse order cancels it to
// second order rather than first.
var thueMorseLegs = [8]int{0, 1, 1, 0, 1, 0, 0, 1}

// legsPerArm is how many of the eight legs each arm gets. Thue-Morse is
// balanced by construction.
const legsPerArm = len(thueMorseLegs) / 2

// pairedTicks times a and b in Thue-Morse leg order, reps times, and returns
// the per-rep TSC-tick totals for each arm. Both arms see the same core
// frequency inside a rep, so the per-rep ratio reproduces across runs even
// though the absolutes track whatever the core clock happened to be doing.
func pairedTicks(reps int, a, b func()) (ta, tb []uint64) {
	ta = make([]uint64, reps)
	tb = make([]uint64, reps)
	for i := 0; i < reps; i++ {
		for _, leg := range thueMorseLegs {
			f, dst := a, &ta[i]
			if leg == 1 {
				f, dst = b, &tb[i]
			}
			start := probeRdtscp()
			f()
			*dst += probeRdtscp() - start
		}
	}
	return ta, tb
}

func TestProbeCyclesPerBlock(t *testing.T) {
	if !useSHANI {
		t.Skip("no SHA-NI on this host")
	}
	bufA := make([]byte, probeBlocks*64)
	bufB := make([]byte, probeBlocks*64)
	for i := range bufA {
		bufA[i] = byte(i * 131)
		bufB[i] = byte(i*17 + 5)
	}
	st0, st1 := sha256IV, sha256IV

	one := func() { sha256BlocksNI(&st0, &bufA[0], probeBlocks) }
	two := func() { sha256Blocks2NI(&st0, &bufA[0], &st1, &bufB[0], probeBlocks) }

	// Warm the caches and the branch predictors before the first timed leg.
	one()
	two()

	const reps = 201
	ta, tb := pairedTicks(reps, one, two)

	// Per rep each arm ran legsPerArm calls, and one 2-way call covers two
	// blocks to the 1-way call's one, so the leg counts cancel and the
	// per-block ratio is just twice the per-rep tick ratio.
	ratios := make([]float64, reps)
	for i := range ratios {
		ratios[i] = 2 * float64(ta[i]) / float64(tb[i])
	}
	slices.Sort(ratios)
	speedup := ratios[len(ratios)/2]

	oneTPB := float64(medianU64(ta)) / float64(legsPerArm*probeBlocks)
	pairTPB := float64(medianU64(tb)) / float64(legsPerArm*probeBlocks)
	twoTPB := pairTPB / 2

	t.Logf("1-way: %.1f TSC ticks/block", oneTPB)
	t.Logf("2-way: %.1f TSC ticks/block (%.1f ticks per block-pair)", twoTPB, pairTPB)
	// Quartiles, not min/max: a rep that caught an interrupt in one leg puts
	// the extremes an order of magnitude out, which says nothing about the
	// resolution the instrument actually offers.
	t.Logf("interleave speedup: %.2fx (paired median of %d reps, IQR %.2fx-%.2fx)",
		speedup, reps, ratios[len(ratios)/4], ratios[3*len(ratios)/4])
	t.Log("units: RDTSCP counts invariant-TSC ticks, which advance at a fixed base " +
		"rate while the cores turbo well above it, so these absolutes are not core " +
		"cycles. Converting needs the core:TSC ratio, which this instrument does " +
		"not measure (on a Raptor Cove P-core it runs near 1.8-1.9). The " +
		"SHA256RNDS2 dependency chain is 32 serial instructions per block.")
	t.Log("the absolutes move with the core clock and do not reproduce across runs; " +
		"the paired speedup does, and is the number to compare against")

	if speedup < 1.0 {
		t.Fatalf("2-way slower than 1-way (%.2fx) - instrument or kernel is broken", speedup)
	}
}
