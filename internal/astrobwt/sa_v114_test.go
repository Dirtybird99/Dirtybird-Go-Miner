package astrobwt

import (
	"fmt"
	"math/rand"
	"sync/atomic"
	"testing"
)

// v114 must produce byte-identical hashes to the SAIS reference path.
func TestV114DifferentialVsSAIS(t *testing.T) {
	iters := 5000
	if testing.Short() {
		iters = 300
	}
	rnd := rand.New(rand.NewSource(1234))
	hv := NewWithBackend(BackendV114)
	hs := NewWithBackend(BackendSAIS)

	var work [48]byte
	for i := 0; i < iters; i++ {
		rnd.Read(work[:])
		got := hv.Hash(work[:])
		want := hs.Hash(work[:])
		if got != want {
			t.Fatalf("iter %d: v114 mismatch on input %x:\n got %x\nwant %x", i, work, got, want)
		}
	}
}

// Same, over varied input lengths (protocol inputs are 48 bytes, but the KAT
// vectors and edge behavior use other lengths).
func TestV114DifferentialLengths(t *testing.T) {
	rnd := rand.New(rand.NewSource(99))
	hv := NewWithBackend(BackendV114)
	hs := NewWithBackend(BackendSAIS)
	buf := make([]byte, 1024)
	lengths := []int{1, 2, 3, 7, 16, 31, 47, 48, 49, 64, 255, 1024}
	iters := 60
	if testing.Short() {
		iters = 12
	}
	for i := 0; i < iters; i++ {
		n := lengths[i%len(lengths)]
		rnd.Read(buf[:n])
		if got, want := hv.Hash(buf[:n]), hs.Hash(buf[:n]); got != want {
			t.Fatalf("mismatch len=%d input %x: got %x want %x", n, buf[:n], got, want)
		}
	}
}

// KAT through the v114 backend.
func TestV114KAT(t *testing.T) {
	h := NewWithBackend(BackendV114)
	for _, g := range referencePowTests {
		if s := fmt.Sprintf("%x", h.Hash([]byte(g.in))); s != g.out {
			t.Fatalf("v114 pow(%q) = %s want %s", g.in, s, g.out)
		}
	}
}

func TestEmitFullGroupRunEqualColumns(t *testing.T) {
	for _, groups := range []uint32{3, 8, 26} {
		n := int(groups << 8)
		data := make([]byte, n+4)
		for i := 0; i < n; i++ {
			data[i] = 0x5a
		}
		view := stage4View{data: data, logicalLen: uint32(n)}
		v := newV114Scratch()
		if !emitFullGroupRunGeneric(&view, 0, groups, v) {
			t.Fatalf("emit failed for %d groups", groups)
		}
		got := make([]int32, n)
		if !writeFusedRunsToSA(&view, v, got) {
			t.Fatalf("write failed for %d groups", groups)
		}
		want := make([]int32, n)
		text_32_0alloc(data[:n], want, make([]int32, n/2))
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%d groups: SA[%d]=%d, want %d", groups, i, got[i], want[i])
			}
		}
	}
}

// countingSAIS wraps a hash pass and reports how often v114 declined. High
// fallback rates would silently erase the speedup.
func TestV114FallbackRate(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	rnd := rand.New(rand.NewSource(5))
	h := NewWithBackend(BackendV114)
	var work [48]byte
	const n = 500
	var fallbacks int
	for i := 0; i < n; i++ {
		rnd.Read(work[:])
		before := v114Fallbacks.Load()
		h.Hash(work[:])
		if v114Fallbacks.Load() != before {
			fallbacks++
		}
	}
	t.Logf("v114 fallback rate: %d/%d", fallbacks, n)
	if fallbacks > n/10 {
		t.Fatalf("v114 declines too often: %d/%d", fallbacks, n)
	}
}

var _ = atomic.Int64{} // keep import if counters move

func TestV114ZeroAllocsAfterWarmup(t *testing.T) {
	h := NewWithBackend(BackendV114)
	var work [48]byte
	rand.Read(work[:])
	h.Hash(work[:]) // warm the scratch growth paths
	fallbacksBefore := V114Fallbacks()
	allocs := testing.AllocsPerRun(100, func() {
		work[0]++
		_ = h.Hash(work[:])
	})
	if allocs != 0 {
		t.Fatalf("v114 Hash allocates %v times per run, want 0", allocs)
	}
	// The SAIS fallback path may allocate (sais.go pathological-input tmp),
	// which AllocsPerRun can't attribute; prove it never ran instead.
	if fb := V114Fallbacks() - fallbacksBefore; fb != 0 {
		t.Fatalf("%d SAIS fallbacks during the alloc gate — the 0-alloc result is unproven for v114", fb)
	}
}

func BenchmarkHashV114(b *testing.B) {
	b.ReportAllocs()
	h := NewWithBackend(BackendV114)
	var work [48]byte
	rand.Read(work[:])
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		work[47] = byte(i)
		_ = h.Hash(work[:])
	}
}
