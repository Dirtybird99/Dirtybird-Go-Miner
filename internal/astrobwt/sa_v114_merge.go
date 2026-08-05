package astrobwt

// Stage-5 merge: two stable radix passes order the records by the low two
// lexical key bytes, then the byte0 pass is fused with materialization — each
// record's positions are written directly at their final SA offset through 256
// per-bucket cursors, so the third record scatter and the sequential group
// scan disappear. Equal-key collisions get contiguous provisional writes plus
// a chained fixup entry; a post-pass re-sorts or merges exactly those ranges
// (in-place insertion sort for all-singleton groups <=32, linear merge for two
// sub-runs, bottom-up k-way merge otherwise). Port of write_fused_runs_to_sa
// (v114_stubs.cpp) restructured around the scatter; merge semantics unchanged.

import (
	"bytes"
	"encoding/binary"
	"unsafe"
)

// keySentinel initializes the per-bucket previous-key slots. Keys are 24-bit,
// so no record can ever equal it; without a sentinel, a bucket whose first
// arrival had key 0 (or a stale value from a previous hash) would open a
// phantom collision group and the fixup would re-sort a range spanning
// unequal keys — silently wrong, since the after-key compare skips the first
// three bytes.
const keySentinel = 0xffffffff

// compareSuffixesAfterKey compares two suffixes whose first 3 bytes (the
// record key) are already known equal.
func compareSuffixesAfterKey(v *stage4View, a, b uint32) int {
	if a == b {
		return 0
	}
	aLen := v.logicalLen - a
	bLen := v.logicalLen - b
	commonWithKey := aLen
	if bLen < commonWithKey {
		commonWithKey = bLen
	}
	if commonWithKey <= 3 {
		if aLen == bLen {
			return 0
		}
		if aLen < bLen {
			return -1
		}
		return 1
	}

	common := commonWithKey - 3
	ap := v.data[a+3:]
	bp := v.data[b+3:]
	if common >= 8 {
		av := binary.BigEndian.Uint64(ap)
		bv := binary.BigEndian.Uint64(bp)
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
		if c := bytes.Compare(ap[8:common], bp[8:common]); c != 0 {
			return c
		}
	} else if c := bytes.Compare(ap[:common], bp[:common]); c != 0 {
		return c
	}
	if aLen == bLen {
		return 0
	}
	if aLen < bLen {
		return -1
	}
	return 1
}

func suffixLessAfterKey(v *stage4View, a, b uint32) bool {
	cmp := compareSuffixesAfterKey(v, a, b)
	if cmp != 0 {
		return cmp < 0
	}
	return a < b
}

// radixSortRunsByLowKeyBytes stably sorts the records by (byte1, byte2) of
// the native little-endian key — byte2 pass then byte1 pass, ping-ponging
// runs->tmp->runs. Byte0, the lexically most significant byte, is handled by
// the materializing scatter, whose per-bucket cursor table is sized from
// posCounts0 (positions, not records, per byte0 bucket) accumulated here.
func radixSortRunsByLowKeyBytes(v *v114Scratch, posCounts0 *[256]uint32) {
	runs := v.runs
	n := len(runs)
	tmp := v.radixTmp[:n]

	var counts1, counts2 [256]uint32
	for i := range runs {
		r := runs[i]
		posCounts0[r.key&0xff] += r.count()
		counts1[(r.key>>8)&0xff]++
		counts2[(r.key>>16)&0xff]++
	}

	var sum uint32
	for i := 0; i < 256; i++ {
		c := counts2[i]
		counts2[i] = sum
		sum += c
	}
	for i := range runs {
		r := runs[i]
		tmp[counts2[(r.key>>16)&0xff]] = r
		counts2[(r.key>>16)&0xff]++
	}

	sum = 0
	for i := 0; i < 256; i++ {
		c := counts1[i]
		counts1[i] = sum
		sum += c
	}
	for i := range tmp {
		r := tmp[i]
		runs[counts1[(r.key>>8)&0xff]] = r
		counts1[(r.key>>8)&0xff]++
	}
}

// fixGroup describes one equal-key collision group: a contiguous SA range
// [saStart, saEnd) holding k provisionally-ordered sub-runs, whose start
// offsets live in the fixSlots chain from firstSlot.
type fixGroup struct {
	saStart, saEnd      uint32
	firstSlot, lastSlot int32
}

// noteCollision opens or extends the collision group for bucket b. Kept out
// of line so the scatter's hot loop does not spill registers for a branch
// taken a few hundred times per hash. prevStart is where the group's previous
// record wrote; curStart is where the current record is about to write.
//
//go:noinline
func noteCollision(v *v114Scratch, open *[256]int32, b uint32, prevStart, curStart uint32) {
	g := open[b]
	if g < 0 {
		v.fixSlots = append(v.fixSlots, prevStart)
		v.fixNext = append(v.fixNext, -1)
		first := int32(len(v.fixSlots) - 1)
		v.fixGroups = append(v.fixGroups, fixGroup{saStart: prevStart, firstSlot: first, lastSlot: first})
		g = int32(len(v.fixGroups) - 1)
		open[b] = g
	}
	v.fixSlots = append(v.fixSlots, curStart)
	v.fixNext = append(v.fixNext, -1)
	idx := int32(len(v.fixSlots) - 1)
	fg := &v.fixGroups[g]
	v.fixNext[fg.lastSlot] = idx
	fg.lastSlot = idx
}

func mergeSortedPositionsAfterKey(view *stage4View, src []uint32, leftBegin, leftEnd, rightEnd int, dst []uint32, dstBegin int) {
	left, right, out := leftBegin, leftEnd, dstBegin
	for left < leftEnd && right < rightEnd {
		lpos, rpos := src[left], src[right]
		if suffixLessAfterKey(view, lpos, rpos) {
			dst[out] = lpos
			left++
		} else {
			dst[out] = rpos
			right++
		}
		out++
	}
	for left < leftEnd {
		dst[out] = src[left]
		left++
		out++
	}
	for right < rightEnd {
		dst[out] = src[right]
		right++
		out++
	}
}

// mergeEqualKeyRuns: bottom-up pairwise merge of the per-run sorted position
// lists in v.groupPos (lengths in v.runLens); result ends in v.groupPos.
func mergeEqualKeyRuns(view *stage4View, v *v114Scratch) {
	if len(v.runLens) <= 1 {
		return
	}
	n := len(v.groupPos)
	v.mergePos = v.mergePos[:cap(v.mergePos)]
	src := v.groupPos
	dst := v.mergePos[:n]
	fromGroupPos := true
	for len(v.runLens) > 1 {
		v.nextLens = v.nextLens[:0]
		inBase, outBase := 0, 0
		for i := 0; i < len(v.runLens); i += 2 {
			leftLen := int(v.runLens[i])
			if i+1 == len(v.runLens) {
				copy(dst[outBase:outBase+leftLen], src[inBase:inBase+leftLen])
				v.nextLens = append(v.nextLens, uint32(leftLen))
				inBase += leftLen
				outBase += leftLen
				continue
			}
			rightLen := int(v.runLens[i+1])
			mergeSortedPositionsAfterKey(view, src, inBase, inBase+leftLen, inBase+leftLen+rightLen, dst, outBase)
			v.nextLens = append(v.nextLens, uint32(leftLen+rightLen))
			inBase += leftLen + rightLen
			outBase += leftLen + rightLen
		}
		v.runLens, v.nextLens = v.nextLens, v.runLens
		src, dst = dst, src
		fromGroupPos = !fromGroupPos
	}
	if !fromGroupPos { // final result sits in mergePos; move it back
		copy(v.groupPos[:n], src[:n])
	}
}

// fixupCollisionGroups restores correct order inside every collision group's
// SA range. Sub-run boundaries come from the group's slot chain; the merge
// helpers and comparator are the same ones the scan-based writer used, so the
// output multiset and order are identical.
func fixupCollisionGroups(view *stage4View, v *v114Scratch, saU32 []uint32) {
	for gi := range v.fixGroups {
		g := &v.fixGroups[gi]
		v.runLens = v.runLens[:0]
		prev := v.fixSlots[g.firstSlot]
		for idx := v.fixNext[g.firstSlot]; idx >= 0; idx = v.fixNext[idx] {
			s := v.fixSlots[idx]
			v.runLens = append(v.runLens, s-prev)
			prev = s
		}
		v.runLens = append(v.runLens, g.saEnd-prev)
		k := len(v.runLens)
		size := int(g.saEnd - g.saStart)
		seg := saU32[g.saStart:g.saEnd:g.saEnd]

		if size == k && k <= 32 {
			// every sub-run is a single position: the population the old
			// stack-array literal path handled, now sorted in place
			v114StatsRecordLiteralGroup(k)
			for i := 1; i < size; i++ {
				pos := seg[i]
				j := i
				for j > 0 && suffixLessAfterKey(view, pos, seg[j-1]) {
					seg[j] = seg[j-1]
					j--
				}
				seg[j] = pos
			}
		} else if k == 2 {
			// linear merge of two sorted sub-runs; the source must be copied
			// out first — merging back into the same range would overwrite
			// unread left-run positions
			v114StatsRecordTwoRunMerge()
			v.groupPos = v.groupPos[:size]
			copy(v.groupPos, seg)
			mergeSortedPositionsAfterKey(view, v.groupPos, 0, int(v.runLens[0]), size, seg, 0)
		} else {
			v114StatsRecordLargeFallbackMerge()
			v.groupPos = v.groupPos[:size]
			copy(v.groupPos, seg)
			mergeEqualKeyRuns(view, v)
			copy(seg, v.groupPos[:size])
		}
	}
}

// writeFusedRunsToSA sorts the records by key and writes the final SA
// positions, materializing during the byte0 scatter.
func writeFusedRunsToSA(view *stage4View, v *v114Scratch, sa []int32) bool {
	var posCounts0 [256]uint32
	radixSortRunsByLowKeyBytes(v, &posCounts0)

	// prefix-sum the position counts into per-bucket [cur, bucketEnd) ranges.
	// The total check is the old terminal outPos == len(sa) hoisted up front:
	// same decline set, and it is what keeps every later write in bounds.
	type hotState struct{ cur, prevKey uint32 }
	var st [256]hotState
	var bucketEnd [256]uint32
	var sum uint32
	for b := 0; b < 256; b++ {
		st[b].cur = sum
		st[b].prevKey = keySentinel
		sum += posCounts0[b]
		bucketEnd[b] = sum
	}
	if int(sum) != len(sa) {
		return false
	}

	// uint32 view of sa: positions < 2^31, so int32/uint32 bits are identical
	// and arena runs can be bulk-copied (the C++ memcpys here). buildSAv114
	// guarantees len(sa) >= 1.
	saU32 := unsafe.Slice((*uint32)(unsafe.Pointer(&sa[0])), len(sa))

	v.fixSlots = v.fixSlots[:0]
	v.fixNext = v.fixNext[:0]
	v.fixGroups = v.fixGroups[:0]
	var open [256]int32
	var prevStart [256]uint32
	for b := range open {
		open[b] = -1
	}

	// Equal keys arrive consecutively within a byte0 bucket: the two passes
	// left the records stably sorted by (byte1, byte2), and equal full keys
	// share byte0, so within one bucket's arrival subsequence they are
	// adjacent. That is what lets a single prevKey slot per bucket detect
	// every collision and close each group exactly once.
	runs := v.runs
	arena := v.arena
	for i := range runs {
		r := runs[i]
		b := r.key & 0xff
		hs := &st[b]
		start := hs.cur
		if r.key == hs.prevKey {
			noteCollision(v, &open, b, prevStart[b], start)
		} else if open[b] >= 0 {
			v.fixGroups[open[b]].saEnd = start
			open[b] = -1
		}
		if r.packed>>17 == 0 {
			// literal singleton (packed IS the position) — hottest case
			if start >= bucketEnd[b] {
				return false
			}
			saU32[start] = r.packed
			hs.cur = start + 1
		} else {
			cnt := r.packed >> 17
			begin := r.packed & 0x1ffff
			if start+cnt > bucketEnd[b] {
				return false
			}
			copy(saU32[start:bucketEnd[b]], arena[begin:begin+cnt])
			hs.cur = start + cnt
		}
		hs.prevKey = r.key
		prevStart[b] = start
	}

	// close still-open groups, then verify every bucket landed exactly on its
	// boundary — the analogue of the old terminal check, converting any
	// cursor-accounting bug into a decline instead of a silently wrong SA
	for b := 0; b < 256; b++ {
		if open[b] >= 0 {
			v.fixGroups[open[b]].saEnd = st[b].cur
		}
		if st[b].cur != bucketEnd[b] {
			return false
		}
	}

	fixupCollisionGroups(view, v, saU32)
	return true
}
