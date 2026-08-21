package astrobwt

import (
	"math/rand"
	"testing"
	"unsafe"
)

type carveSeg struct {
	name    string
	start   uintptr
	extent  uintptr
	align   uintptr
	length  int
	capa    int
	fill    func(tag uint32)
	check   func(tag uint32) (int, uint32, bool)
	wantLen int
	wantCap int
}

func segsOf(v *v114Scratch) []carveSeg {
	u32 := func(name string, s []uint32, wantLen, wantCap int) carveSeg {
		full := s[:cap(s)]
		return carveSeg{
			name:    name,
			start:   uintptr(unsafe.Pointer(unsafe.SliceData(s))),
			extent:  uintptr(cap(s)) * uintptr(v114SzU32),
			align:   unsafe.Alignof(uint32(0)),
			length:  len(s),
			capa:    cap(s),
			wantLen: wantLen,
			wantCap: wantCap,
			fill: func(tag uint32) {
				for i := range full {
					full[i] = tag<<24 | uint32(i)&0xffffff
				}
			},
			check: func(tag uint32) (int, uint32, bool) {
				for i := range full {
					if want := tag<<24 | uint32(i)&0xffffff; full[i] != want {
						return i, full[i], false
					}
				}
				return 0, 0, true
			},
		}
	}
	run := func(name string, s []stage5Run, wantLen, wantCap int) carveSeg {
		full := s[:cap(s)]
		return carveSeg{
			name:    name,
			start:   uintptr(unsafe.Pointer(unsafe.SliceData(s))),
			extent:  uintptr(cap(s)) * uintptr(v114SzRun),
			align:   unsafe.Alignof(stage5Run{}),
			length:  len(s),
			capa:    cap(s),
			wantLen: wantLen,
			wantCap: wantCap,
			fill: func(tag uint32) {
				for i := range full {
					full[i] = stage5Run{key: tag<<24 | uint32(i)&0xffffff, packed: tag}
				}
			},
			check: func(tag uint32) (int, uint32, bool) {
				for i := range full {
					if want := tag<<24 | uint32(i)&0xffffff; full[i].key != want || full[i].packed != tag {
						return i, full[i].key, false
					}
				}
				return 0, 0, true
			},
		}
	}
	// Expected len/cap written as independent literals, not as restatements of
	// the size constants, so the test states the contract rather than echoing
	// the code.
	return []carveSeg{
		u32("order", v.order, 0, 520),
		u32("arena", v.arena, 0, 131080),
		run("runs", v.runs, 0, 70912),
		run("radixTmp", v.radixTmp, 70912, 70912),
		u32("groupPos", v.groupPos, 0, 70912),
		u32("mergePos", v.mergePos, 70912, 70912),
		u32("runLens", v.runLens, 0, 70912),
		u32("nextLens", v.nextLens, 0, 70912),
	}
}

func assertContained(t *testing.T, label string, segs []carveSeg, base []byte) {
	t.Helper()
	lo := uintptr(unsafe.Pointer(unsafe.SliceData(base)))
	hi := lo + uintptr(len(base))
	for _, s := range segs {
		if s.start < lo || s.start+s.extent > hi {
			t.Fatalf("%s: %s spans [%#x,%#x), outside base [%#x,%#x)",
				label, s.name, s.start, s.start+s.extent, lo, hi)
		}
	}
}

func assertDisjoint(t *testing.T, label string, segs []carveSeg) {
	t.Helper()
	for i := 0; i < len(segs); i++ {
		for j := i + 1; j < len(segs); j++ {
			a, b := segs[i], segs[j]
			if a.start+a.extent > b.start && b.start+b.extent > a.start {
				t.Fatalf("%s: %s [%#x,%#x) overlaps %s [%#x,%#x)",
					label, a.name, a.start, a.start+a.extent,
					b.name, b.start, b.start+b.extent)
			}
		}
	}
}

func TestV114ScratchCarveLayout(t *testing.T) {
	v, base := carveV114Scratch()
	segs := segsOf(v)
	baseStart := uintptr(unsafe.Pointer(unsafe.SliceData(base)))

	t.Run("len and cap", func(t *testing.T) {
		for _, s := range segs {
			if s.length != s.wantLen || s.capa != s.wantCap {
				t.Fatalf("%s: len=%d cap=%d, want len=%d cap=%d",
					s.name, s.length, s.capa, s.wantLen, s.wantCap)
			}
		}
		// The padding contract the SIMD kernels round up into.
		if cap(v.order) < stage4MaxGroupRun+8 || cap(v.arena) < arenaIndexCount+8 {
			t.Fatalf("scratch is not padded: order=%d arena=%d", cap(v.order), cap(v.arena))
		}
	})

	t.Run("region size", func(t *testing.T) {
		if len(base) != v114ScratchBytes {
			t.Fatalf("base is %d bytes, want %d", len(base), v114ScratchBytes)
		}
	})

	t.Run("inside base", func(t *testing.T) { assertContained(t, "fresh", segs, base) })
	t.Run("no overlap", func(t *testing.T) { assertDisjoint(t, "fresh", segs) })

	t.Run("alignment", func(t *testing.T) {
		for _, s := range segs {
			if off := s.start - baseStart; off%v114SegAlign != 0 {
				t.Fatalf("%s starts at base+%d, not a %d-byte boundary", s.name, off, v114SegAlign)
			}
			if s.start%s.align != 0 {
				t.Fatalf("%s at %#x is not %d-byte aligned for its element type", s.name, s.start, s.align)
			}
		}
	})

	t.Run("budget", func(t *testing.T) {
		sum := 0
		for _, s := range segs {
			sum += (int(s.extent) + v114SegAlign - 1) &^ (v114SegAlign - 1)
		}
		if sum != v114ScratchBytes {
			t.Fatalf("rounded segments total %d, budget is %d", sum, v114ScratchBytes)
		}
	})

	t.Run("zeroed", func(t *testing.T) {
		for i, b := range base {
			if b != 0 {
				t.Fatalf("base[%d] = %#x, want 0", i, b)
			}
		}
	})
}

func TestV114ScratchCarveNoAliasing(t *testing.T) {
	// Write a distinct tag through every segment at full cap, then read them
	// all back. A stray write surfaces carrying the writer's tag, which names
	// the culprit without any address arithmetic.
	v, _ := carveV114Scratch()
	segs := segsOf(v)
	for i, s := range segs {
		s.fill(uint32(i + 1))
	}
	for i, s := range segs {
		if idx, got, ok := s.check(uint32(i + 1)); !ok {
			t.Fatalf("%s[%d] = %#x: tag %d, clobbered by segment tag %d",
				s.name, idx, got, i+1, got>>24)
		}
	}
}

func TestV114ScratchStaysInRegionAfterHashing(t *testing.T) {
	// The layout test only proves the shape at construction. Drive real hashes
	// and re-check the invariants that survive the runs/radixTmp and
	// runLens/nextLens swaps: containment, disjointness, caps.
	v, base := carveV114Scratch()
	s := NewScratchData()
	s.useV114 = true
	s.v114 = v

	rnd := rand.New(rand.NewSource(0xca47e))
	var work [48]byte
	for i := 0; i < 200; i++ {
		rnd.Read(work[:])
		_ = astroBWTv3(work[:], s)
	}

	segs := segsOf(v)
	assertContained(t, "after hashing", segs, base)
	assertDisjoint(t, "after hashing", segs)
	for _, seg := range segs {
		if seg.capa != seg.wantCap {
			t.Fatalf("after hashing: %s cap=%d, want %d", seg.name, seg.capa, seg.wantCap)
		}
	}
}
