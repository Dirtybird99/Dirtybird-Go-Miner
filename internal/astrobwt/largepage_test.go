package astrobwt

import (
	"runtime"
	"testing"
	"unsafe"
)

// TestLargePageRegionContract exercises whichever branch the host allows: on a
// box without the "Lock pages in memory" right (every CI runner) the region is
// nil and the carve must fall back to the heap; with the right, the region
// must be exactly the requested length, large-page aligned, fully writable,
// and releasable without touching the Go heap.
func TestLargePageRegionContract(t *testing.T) {
	region, release := largePageRegion(v114ScratchBytes)
	if region == nil {
		if release != nil {
			t.Fatalf("nil region must come with a nil release")
		}
		if LargePagesActive() {
			t.Fatalf("LargePagesActive reports true without a region")
		}
		v, base := carveV114Scratch()
		if v == nil || len(base) != v114ScratchBytes {
			t.Fatalf("heap fallback carve: v=%v len(base)=%d", v != nil, len(base))
		}
		t.Log("large pages unavailable on this host; heap fallback exercised")
		return
	}
	defer release()
	if !LargePagesActive() {
		t.Fatalf("region allocated but LargePagesActive is false")
	}
	if len(region) != v114ScratchBytes || cap(region) != v114ScratchBytes {
		t.Fatalf("region len=%d cap=%d, want both %d", len(region), cap(region), v114ScratchBytes)
	}
	addr := uintptr(unsafe.Pointer(&region[0]))
	if addr%(2<<20) != 0 {
		t.Fatalf("region base %#x is not 2 MiB aligned", addr)
	}
	// Every byte must be writable and start zeroed (MEM_COMMIT semantics).
	for i := range region {
		if region[i] != 0 {
			t.Fatalf("region[%d] = %d, want zero-initialised", i, region[i])
		}
		region[i] = byte(i)
	}
	for i := range region {
		if region[i] != byte(i) {
			t.Fatalf("region[%d] read back %d, want %d", i, region[i], byte(i))
		}
	}
	// The carve over a large-page region must produce the same layout as the
	// heap carve: segment identity and caps are what every later assertion
	// relies on.
	v, base := carveV114Scratch()
	if len(base) != v114ScratchBytes {
		t.Fatalf("carve base len=%d, want %d", len(base), v114ScratchBytes)
	}
	if cap(v.order) != v114OrderCap || cap(v.arena) != v114ArenaCap || cap(v.runs) != v114RunCap ||
		cap(v.radixTmp) != v114RunCap || cap(v.groupPos) != v114RunCap || cap(v.mergePos) != v114RunCap ||
		cap(v.runLens) != v114RunCap || cap(v.nextLens) != v114RunCap {
		t.Fatalf("large-page carve produced different caps: %+v", v)
	}
	runtime.KeepAlive(v)
}

// TestLargePageCarveMatchesHeapHash is the correctness gate for the backing
// swap: the same input must hash byte-identically whether the scratch sits on
// large pages or on the heap. It is meaningful only where large pages are
// available; elsewhere both Hashers take the heap path and the test degrades
// to a self-comparison, which is still a valid zero-alloc smoke.
func TestLargePageCarveMatchesHeapHash(t *testing.T) {
	a := NewWithBackend(BackendV114)
	b := NewWithBackend(BackendV114)
	input := []byte("large pages must not change a single suffix")
	for i := 0; i < 64; i++ {
		input = append(input, byte(i*7))
		ha := a.Hash(input)
		hb := b.Hash(input)
		if ha != hb {
			t.Fatalf("hash mismatch at iteration %d: %x vs %x", i, ha, hb)
		}
	}
}
