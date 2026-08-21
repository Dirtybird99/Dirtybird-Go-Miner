package astrobwt

import (
	stdsha256 "crypto/sha256"
	"encoding/binary"
	"math/rand"
	"testing"
)

// TestPairKernelIsLiveOrSaysSo exists because every other test in this file
// degrades silently. HashPair falls back to two Hash calls and
// sha256Sum256Pair falls back to crypto/sha256 when the batched kernel is
// unavailable, so on such a host the pair suite compares crypto/sha256
// against itself, passes, and proves nothing about the kernel. This test
// makes that state impossible to mistake for coverage: it skips with an
// explicit message instead of quietly passing. Note a plain `go test` prints
// neither the skip reason nor the log line without -v; the arm64 CI leg gets
// its `pairHashAvailable=true` assertion from TestPairDifferentialVsSingle,
// which it runs with -test.v, and no amd64 leg asserts it at all.
func TestPairKernelIsLiveOrSaysSo(t *testing.T) {
	t.Logf("pairHashPossible=%v pairHashAvailable=%v", pairHashPossible, pairHashAvailable())
	if !pairHashAvailable() {
		t.Skip("the batched kernel is NOT available on this host: every pair test here " +
			"degrades to crypto/sha256 and proves nothing about the two-lane path")
	}
}

// TestHashPairProductionShape feeds HashPair the exact input the miner
// produces and no other test does: two 48-byte blobs identical except for the
// big-endian nonce at bytes 43..46. That shape matters because the two lanes
// share one v114 scratch (see Hasher.HashPair); a stale carry-over between
// lanes is likeliest to hide when the inputs are near-identical, since both
// lanes would then return the same wrong digest and any lane-vs-lane check
// would still agree. Comparing each lane against an independent Hash, and
// requiring the two lanes to differ, catches both halves of that.
func TestHashPairProductionShape(t *testing.T) {
	if !pairHashAvailable() {
		t.Skip("batched kernel unavailable: HashPair would degrade to two Hash calls")
	}
	for _, backend := range []Backend{BackendV114, BackendSAIS} {
		hp := NewWithBackend(backend)
		hs := NewWithBackend(backend)
		var a, b [48]byte
		rand.New(rand.NewSource(11)).Read(a[:])
		a[0] = 0x41 // miniblock version nibble, as the daemon sends it
		a[47] = 7   // thread id, as the worker stamps it
		b = a
		for nonce := uint32(1); nonce <= 64; nonce += 2 {
			binary.BigEndian.PutUint32(a[43:47], nonce)
			binary.BigEndian.PutUint32(b[43:47], nonce+1)
			ga, gb := hp.HashPair(a[:], b[:])
			if wa := hs.Hash(a[:]); ga != wa {
				t.Fatalf("backend %v nonce %d: lane A %x != Hash %x", backend, nonce, ga, wa)
			}
			if wb := hs.Hash(b[:]); gb != wb {
				t.Fatalf("backend %v nonce %d: lane B %x != Hash %x", backend, nonce+1, gb, wb)
			}
			if ga == gb {
				t.Fatalf("backend %v nonce %d: both lanes returned %x for different nonces", backend, nonce, ga)
			}
			if a[47] != 7 || b[47] != 7 {
				t.Fatalf("nonce write clobbered the thread id byte: a[47]=%d b[47]=%d", a[47], b[47])
			}
		}
	}
}

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
