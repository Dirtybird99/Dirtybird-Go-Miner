package astrobwt

// Pure-Go port of the "v1.14 descriptor" suffix array
// (zig miner vendor/v114/v114_stubs.cpp, stage_v114_sa_build_compact_fused_raw
// and callees; MIT, derived from the dirtybird reference). It exploits the
// repeat structure of wolfCompute's output — each "template" is a run of
// 256-byte chunks between RC4 rescrambles, recorded as markers during the
// wolf loop — to build the EXACT suffix array ~2x faster than SAIS.
//
// Faithful-port rules: singleton behavior is the C++ default
// (count1_singletons == false); all limits (0x20000 arena, 512-group runs,
// 25-group short path, 32-literal stack groups) match the C++ constants.
// Any decline falls back to the SAIS backend for that hash.

import (
	"sync/atomic"
	"unsafe"
)

const (
	arenaIndexCount   = 0x20000 // kDescriptorArenaIndexCount
	stage4MaxGroupRun = arenaIndexCount >> 8
	stage4ShortRunMax = 25
)

// v114Fallbacks counts hashes where the descriptor SA declined and the SAIS
// path ran instead (observability; correctness is unaffected).
var v114Fallbacks atomic.Uint64

// V114Fallbacks reports the process-wide fallback count.
func V114Fallbacks() uint64 { return v114Fallbacks.Load() }

// stage5Run mirrors the C++ 8-byte descriptor record. key is the native
// little-endian 24-bit load. packed: count<<17 | arenaBegin, or a literal
// position when the count bits are zero.
type stage5Run struct {
	key    uint32
	packed uint32
}

func (r stage5Run) encodedCount() uint32 { return r.packed >> 17 }
func (r stage5Run) isLiteral() bool      { return r.packed>>17 == 0 }
func (r stage5Run) begin() uint32        { return r.packed & 0x1ffff }
func (r stage5Run) count() uint32 {
	c := r.packed >> 17
	if c == 0 {
		return 1
	}
	return c
}

// v114Scratch holds the reusable buffers; allocated once per Hasher on first
// v114 use (~2.8MB).
type v114Scratch struct {
	order    []uint32
	arena    []uint32
	runs     []stage5Run
	radixTmp []stage5Run
	groupPos []uint32
	mergePos []uint32
	runLens  []uint32
	nextLens []uint32
}

const (
	v114OrderCap = stage4MaxGroupRun + 8 // the +8 the SIMD kernels round up into
	v114ArenaCap = arenaIndexCount + 8
	v114RunCap   = int(MAX_LENGTH)
	v114SegAlign = 64

	v114SzU32 = int(unsafe.Sizeof(uint32(0)))
	v114SzRun = int(unsafe.Sizeof(stage5Run{}))

	v114SegOrder = (v114OrderCap*v114SzU32 + v114SegAlign - 1) &^ (v114SegAlign - 1)
	v114SegArena = (v114ArenaCap*v114SzU32 + v114SegAlign - 1) &^ (v114SegAlign - 1)
	v114SegRuns  = (v114RunCap*v114SzRun + v114SegAlign - 1) &^ (v114SegAlign - 1)
	v114SegPos   = (v114RunCap*v114SzU32 + v114SegAlign - 1) &^ (v114SegAlign - 1)

	v114ScratchBytes = v114SegOrder + v114SegArena + 2*v114SegRuns + 4*v114SegPos
)

// v114Carver hands out cache-line-separated segments of one contiguous region.
type v114Carver struct {
	base []byte
	off  int
}

// bytes reserves n bytes and advances to the next cache line. The reslice
// bounds-checks the whole extent, unlike &base[off], which would check only the
// first byte and let unsafe.Slice fabricate a whole segment over one valid byte.
func (c *v114Carver) bytes(n int) unsafe.Pointer {
	seg := c.base[c.off : c.off+n]
	c.off += (n + v114SegAlign - 1) &^ (v114SegAlign - 1)
	return unsafe.Pointer(&seg[0])
}

// u32 and run take an element count, so a segment's reservation and its length
// are stated once and cannot drift apart.
func (c *v114Carver) u32(n int) []uint32 {
	return unsafe.Slice((*uint32)(c.bytes(n*v114SzU32)), n)
}

func (c *v114Carver) run(n int) []stage5Run {
	return unsafe.Slice((*stage5Run)(c.bytes(n*v114SzRun)), n)
}

// newV114Scratch allocates the reusable buffers; ~2.8MB per Hasher on first
// v114 use.
func newV114Scratch() *v114Scratch {
	v, _ := carveV114Scratch()
	return v
}

// carveV114Scratch also returns the backing region, which only the layout test
// needs; production drops it.
//
// The eight slice headers hold interior pointers into base, and Go resolves any
// address inside a heap object back to that object, so the region lives exactly
// as long as the last surviving slice.
//
// Every element type carved here is pointer-free, and that is load-bearing:
// base is a []byte, so its span is NOSCAN and carries no pointer bitmap. The
// collector never scans a word in this region, which is legal only because no
// pointer is ever stored in it.
//
// Segment identity is not durable past the first hash: radixSortRunsByStoredKey
// swaps runs with radixTmp and mergeEqualKeyRuns swaps runLens with nextLens.
// Both preserve containment, disjointness and caps, which is all any later
// assertion may rely on.
func carveV114Scratch() (*v114Scratch, []byte) {
	c := v114Carver{base: make([]byte, v114ScratchBytes)}

	order := c.u32(v114OrderCap)
	arena := c.u32(v114ArenaCap)
	runs := c.run(v114RunCap)
	radixTmp := c.run(v114RunCap)
	groupPos := c.u32(v114RunCap)
	mergePos := c.u32(v114RunCap)
	runLens := c.u32(v114RunCap)
	nextLens := c.u32(v114RunCap)

	if c.off != v114ScratchBytes {
		// Unreachable while every term is constant. Live so that adding a
		// buffer or changing a cap without updating the budget fails on the
		// first Hasher rather than aliasing two segments.
		panic("astrobwt: v114 scratch carve consumed the wrong number of bytes")
	}

	return &v114Scratch{
		order:    order[:0],
		arena:    arena[:0],
		runs:     runs[:0],
		radixTmp: radixTmp,
		groupPos: groupPos[:0],
		mergePos: mergePos,
		runLens:  runLens[:0],
		nextLens: nextLens[:0],
	}, c.base
}

// buildStage5Flags is the port of build_v114_stage5_flags (sa_v114.zig):
// group-boundary flags from the wolf template markers. Returns 0 on failure.
func buildStage5Flags(markers []uint16, nTemplates, logicalLen uint32, flags []byte) uint32 {
	if logicalLen == 0 {
		return 0
	}
	flagsLen := (logicalLen >> 8) + 1
	if uint32(len(flags)) < flagsLen {
		return 0
	}
	for i := uint32(0); i < flagsLen; i++ {
		flags[i] = 0
	}
	flags[0] = 1
	limit := nTemplates
	if limit > 277 {
		limit = 277
	}
	for i := uint32(0); i < limit; i++ {
		posData := uint32(markers[i])
		startGroup := posData >> 7
		groupCount := posData & 0x7f
		boundary := startGroup + groupCount
		if groupCount != 0 && boundary > 0 && boundary < flagsLen {
			flags[boundary] = 1
		}
	}
	return flagsLen
}

// stage4View bundles what the emit/merge stages read. data extends at least 4
// zero bytes past logicalLen (padding for the unaligned 32-bit loads behind
// the 24-bit keys), which buildSAv114 ensures.
type stage4View struct {
	data       []byte
	logicalLen uint32
}

// buildSAv114 builds the suffix array of s.data[:logicalLen] into
// s.sa[:logicalLen] using the descriptor path. Returns false on any decline;
// the caller falls back to SAIS. LittleEndian only (sa int32s are written
// directly; the C++ writes LE bytes).
func buildSAv114(s *ScratchData, logicalLen uint32) bool {
	if !LittleEndian || logicalLen == 0 || logicalLen > arenaIndexCount || s.nTemplates == 0 {
		return false
	}
	// zero the key-load padding (stale bytes from the previous hash): 3 key
	// bytes plus a 4th so the unaligned 32-bit key reads never see stale data
	s.data[logicalLen] = 0
	s.data[logicalLen+1] = 0
	s.data[logicalLen+2] = 0
	s.data[logicalLen+3] = 0

	flagsLen := buildStage5Flags(s.markers[:], s.nTemplates, logicalLen, s.flags[:])
	if flagsLen == 0 {
		return false
	}
	if s.v114 == nil {
		s.v114 = newV114Scratch()
	}
	v := s.v114
	v.arena = v.arena[:0]
	v.runs = v.runs[:0]

	view := stage4View{data: s.data[:], logicalLen: logicalLen}
	fullGroups := logicalLen >> 8
	runStart := uint32(0)
	for group := uint32(1); group <= fullGroups; group++ {
		if s.flags[group] != 0 || group == fullGroups {
			if !emitFullGroupRun(&view, runStart, group, v) {
				return false
			}
			runStart = group
		}
	}
	if !emitLiteralRecords(&view, fullGroups<<8, logicalLen&0xff, v) {
		return false
	}
	return writeFusedRunsToSA(&view, v, s.sa[:logicalLen])
}
