package astrobwt

import (
	stdsha256 "crypto/sha256"
	"math/rand"
	"testing"
)

// TestPairDifferentialVsSingle pins the multi-buffer SHA kernel itself
// against crypto/sha256 over varied lengths, including block boundaries and
// unequal pairs. The end-to-end gates (KAT, TestHashPairMatchesHash) only
// feed the kernel the miner's own sizes; the Rust port learned that a wrong
// two-stream kernel can slip past gates that never vary the block count.
func TestPairDifferentialVsSingle(t *testing.T) {
	t.Logf("pairHashPossible=%v pairHashAvailable=%v", pairHashPossible, pairHashAvailable())
	rnd := rand.New(rand.NewSource(42))
	lengths := []int{
		1, 2, 31, 32, 47, 48, 55, 56, 57, 63, 64, 65,
		119, 120, 127, 128, 129, 191, 192, 255, 1024, 4096, 283 * 1024,
	}
	buf := make([]byte, 283*1024)
	rnd.Read(buf)
	for _, la := range lengths {
		for _, lb := range lengths {
			a := buf[:la]
			b := buf[len(buf)-lb:]
			ga, gb := sha256Sum256Pair(a, b)
			if wa := stdsha256.Sum256(a); ga != wa {
				t.Fatalf("len(a)=%d len(b)=%d: lane A digest mismatch\ngot  %x\nwant %x", la, lb, ga, wa)
			}
			if wb := stdsha256.Sum256(b); gb != wb {
				t.Fatalf("len(a)=%d len(b)=%d: lane B digest mismatch\ngot  %x\nwant %x", la, lb, gb, wb)
			}
		}
	}
}

// HashPair must be byte-identical to two independent Hash calls, on both
// backends and across varied input lengths.
func TestHashPairMatchesHash(t *testing.T) {
	for _, backend := range []Backend{BackendV114, BackendSAIS} {
		rnd := rand.New(rand.NewSource(7))
		hp := NewWithBackend(backend)
		hs := NewWithBackend(backend)
		buf := make([]byte, 1024)
		lengths := []int{48, 48, 48, 1, 31, 49, 255, 1024}
		iters := 200
		if testing.Short() {
			iters = 30
		}
		for i := 0; i < iters; i++ {
			na := lengths[i%len(lengths)]
			nb := lengths[(i+3)%len(lengths)]
			a := make([]byte, na)
			bb := make([]byte, nb)
			rnd.Read(a)
			rnd.Read(bb)
			_ = buf
			ga, gb := hp.HashPair(a, bb)
			wa := hs.Hash(a)
			wb := hs.Hash(bb)
			if ga != wa || gb != wb {
				t.Fatalf("backend %v iter %d: pair mismatch\n a: got %x want %x\n b: got %x want %x",
					backend, i, ga, wa, gb, wb)
			}
		}
	}
}

func TestHashPairZeroAllocsAfterWarmup(t *testing.T) {
	h := NewWithBackend(BackendV114)
	var a, b [48]byte
	rand.Read(a[:])
	rand.Read(b[:])
	h.HashPair(a[:], b[:]) // warm scratch2 + v114 growth paths
	allocs := testing.AllocsPerRun(100, func() {
		a[0]++
		b[0]++
		_, _ = h.HashPair(a[:], b[:])
	})
	if allocs != 0 {
		t.Fatalf("HashPair allocates %v times per run, want 0", allocs)
	}
}

func BenchmarkHashPairV114(b *testing.B) {
	b.ReportAllocs()
	h := NewWithBackend(BackendV114)
	var wa, wb [48]byte
	rand.Read(wa[:])
	rand.Read(wb[:])
	b.ResetTimer()
	for i := 0; i < b.N; i++ { // one iteration = TWO hashes
		wa[47] = byte(i)
		wb[46] = byte(i)
		_, _ = h.HashPair(wa[:], wb[:])
	}
}
