//go:build goexperiment.simd && amd64

// Measures the real workload the merge comparator sees, so the probe's judged
// arm is a measurement rather than a guess. Adjacent suffix-array pairs that
// share a 3-byte key are exactly the pairs whose order the comparator had to
// decide; their common-prefix length after the key is what determines whether
// a 32-byte vector scan can ever pay.

package astrobwt

import (
	"math/rand"
	"testing"
)

func TestMeasureComparatorPrefixLengths(t *testing.T) {
	const hashes = 2000
	rng := rand.New(rand.NewSource(0xa11ce))
	h := NewWithBackend(BackendV114)

	var buckets [5]int64 // <8, 8-31, 32-63, 64-255, >=256
	var pairs, sum int64
	var work [48]byte

	for i := 0; i < hashes; i++ {
		rng.Read(work[:])
		n := astroBWTv3Stream(work[:], h.scratch)
		data := h.scratch.data[:n]
		sa := h.scratch.sa[:n]
		for j := 1; j < int(n); j++ {
			a, b := int(sa[j-1]), int(sa[j])
			if a+3 > int(n) || b+3 > int(n) {
				continue
			}
			if data[a] != data[b] || data[a+1] != data[b+1] || data[a+2] != data[b+2] {
				continue
			}
			lcp := 0
			for a+3+lcp < int(n) && b+3+lcp < int(n) && data[a+3+lcp] == data[b+3+lcp] {
				lcp++
			}
			pairs++
			sum += int64(lcp)
			switch {
			case lcp < 8:
				buckets[0]++
			case lcp < 32:
				buckets[1]++
			case lcp < 64:
				buckets[2]++
			case lcp < 256:
				buckets[3]++
			default:
				buckets[4]++
			}
		}
	}

	if pairs == 0 {
		t.Fatal("no equal-key adjacent pairs found; fixture is wrong")
	}
	names := [5]string{"<8 (BE-u64 resolves)", "8-31", "32-63", "64-255", ">=256"}
	t.Logf("%d hashes, %d equal-key adjacent pairs, mean prefix %.2f bytes",
		hashes, pairs, float64(sum)/float64(pairs))
	for i, n := range buckets {
		t.Logf("  prefix %-22s %10d  %6.2f%%", names[i], n, 100*float64(n)/float64(pairs))
	}
	vectorable := buckets[2] + buckets[3] + buckets[4]
	t.Logf("  reachable by a 32-byte scan (prefix >= 32): %.2f%%",
		100*float64(vectorable)/float64(pairs))
}
