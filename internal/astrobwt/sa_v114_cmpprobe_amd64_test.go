//go:build goexperiment.simd && amd64

// Probe only; see sa_v114_simdprobe_amd64_test.go for the ground rules.
//
// The shipping comparator resolves the first 8 bytes after the key with one
// big-endian word compare and only then calls bytes.Compare. This probe scans
// 32 bytes at a time instead, which is a different shape rather than a wider
// version of the same loop. The ledger records that the walks are cheap and
// per-call cost dominates, so the realistic workload sits at the short end of
// the common-prefix sweep below.

package astrobwt

import (
	"bytes"
	"math/bits"
	"math/rand"
	"simd/archsimd"
	"testing"
	"unsafe"
)

func probeCompareSuffixesArchSIMD(v *stage4View, a, b uint32) int {
	if a == b {
		return 0
	}
	aLen := v.logicalLen - a
	bLen := v.logicalLen - b
	commonWithKey := aLen
	if bLen < commonWithKey {
		commonWithKey = bLen
	}
	if commonWithKey > 3 {
		common := commonWithKey - 3
		da := v.data[a+3:]
		db := v.data[b+3:]
		i := uint32(0)
		for ; i+32 <= common; i += 32 {
			x := archsimd.LoadUint8x32(da[i:])
			y := archsimd.LoadUint8x32(db[i:])
			if diff := ^x.Equal(y).ToBits(); diff != 0 {
				k := i + uint32(bits.TrailingZeros32(diff))
				if da[k] < db[k] {
					return -1
				}
				return 1
			}
		}
		if c := bytes.Compare(da[i:common], db[i:common]); c != 0 {
			return c
		}
	}
	if aLen == bLen {
		return 0
	}
	if aLen < bLen {
		return -1
	}
	return 1
}

// simdCompareBytes is bytes.Compare with a 32-byte-at-a-time head. Only the
// head differs; the tail defers to the runtime's own asm.
func simdCompareBytes(x, y []byte) int {
	n := len(x)
	if len(y) < n {
		n = len(y)
	}
	i := 0
	for ; i+32 <= n; i += 32 {
		xv := archsimd.LoadUint8x32(x[i:])
		yv := archsimd.LoadUint8x32(y[i:])
		if d := ^xv.Equal(yv).ToBits(); d != 0 {
			k := i + bits.TrailingZeros32(d)
			if x[k] < y[k] {
				return -1
			}
			return 1
		}
	}
	return bytes.Compare(x[i:], y[i:])
}

// probeCompareSuffixesHybrid keeps the shipping kernel's big-endian word for
// the first eight bytes, which the sweep shows is unbeatable below the
// crossover, and swaps only the bytes.Compare tail for a vector scan.
func probeCompareSuffixesHybrid(v *stage4View, a, b uint32) int {
	if a == b {
		return 0
	}
	aLen := v.logicalLen - a
	bLen := v.logicalLen - b
	commonWithKey := aLen
	if bLen < commonWithKey {
		commonWithKey = bLen
	}
	if commonWithKey > 3 {
		common := commonWithKey - 3
		if common >= 8 {
			dp := unsafe.Pointer(&v.data[0])
			av := bits.ReverseBytes64(*(*uint64)(unsafe.Add(dp, uintptr(a)+3)))
			bv := bits.ReverseBytes64(*(*uint64)(unsafe.Add(dp, uintptr(b)+3)))
			if av != bv {
				if av < bv {
					return -1
				}
				return 1
			}
			if c := simdCompareBytes(v.data[a+11:a+3+common], v.data[b+11:b+3+common]); c != 0 {
				return c
			}
		} else if c := bytes.Compare(v.data[a+3:a+3+common], v.data[b+3:b+3+common]); c != 0 {
			return c
		}
	}
	if aLen == bLen {
		return 0
	}
	if aLen < bLen {
		return -1
	}
	return 1
}

func TestProbeCompareSuffixesMatchesKernel(t *testing.T) {
	rng := rand.New(rand.NewSource(0xfeed))
	for _, alphabet := range []int{1, 2, 3, 256} {
		for _, n := range []int{4, 16, 64, 300, 4096} {
			data := make([]byte, n+64)
			for i := 0; i < n; i++ {
				data[i] = byte(rng.Intn(alphabet))
			}
			v := &stage4View{data: data, logicalLen: uint32(n)}
			for trial := 0; trial < 400; trial++ {
				a := uint32(rng.Intn(n))
				b := uint32(rng.Intn(n))
				want := compareSuffixesAfterKey(v, a, b)
				for _, probe := range []struct {
					name string
					f    func(*stage4View, uint32, uint32) int
				}{{"archsimd", probeCompareSuffixesArchSIMD}, {"hybrid", probeCompareSuffixesHybrid}} {
					if got := probe.f(v, a, b); got != want {
						t.Fatalf("%s: alphabet=%d n=%d a=%d b=%d: probe=%d kernel=%d",
							probe.name, alphabet, n, a, b, got, want)
					}
				}
			}
		}
	}
}

// buildCommonPrefixView makes two suffixes over a constant run (the documented
// shape: 99.98% of large literal groups have repeated-byte keys) that agree for
// exactly `common` bytes after the 3-byte key, then differ.
func buildCommonPrefixView(common int) (*stage4View, uint32, uint32) {
	const gap = 8192
	n := 2 * gap
	data := make([]byte, n+64)
	for i := 0; i < n; i++ {
		data[i] = 0x5a
	}
	a, b := uint32(0), uint32(gap)
	data[int(b)+3+common] = 0x5b
	return &stage4View{data: data, logicalLen: uint32(n)}, a, b
}

func benchCompare(b *testing.B, common int, f func(*stage4View, uint32, uint32) int) {
	v, x, y := buildCommonPrefixView(common)
	sink := 0
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink += f(v, x, y)
	}
	b.StopTimer()
	if sink == 1<<62 {
		b.Fatal("unreachable, keeps the result live")
	}
}

func BenchmarkCompareSuffixesAfterKey(b *testing.B) {
	arms := []struct {
		name string
		f    func(*stage4View, uint32, uint32) int
	}{
		{"kernel", compareSuffixesAfterKey},
		{"archsimd", probeCompareSuffixesArchSIMD},
		{"hybrid", probeCompareSuffixesHybrid},
	}
	// judged: TestMeasureComparatorPrefixLengths measured the real population
	// over 100.6M equal-key adjacent pairs from 2000 real hashes -- mean common
	// prefix 96 bytes, only 7.50% under 8, and 75.29% at 32 or more. These four
	// sizes straddle that distribution.
	for _, common := range []int{32, 64, 96, 128} {
		for _, arm := range arms {
			b.Run("judged/c"+itoaU(uint32(common))+"/"+arm.name, func(b *testing.B) {
				benchCompare(b, common, arm.f)
			})
		}
	}
	// secondary: the short end, 7.50% of the measured population, where the
	// shipping big-endian word resolves without a second pass. c4096 is
	// diagnostic only and must not be quoted as the miner's workload.
	for _, common := range []int{0, 2, 7, 15, 256, 4096} {
		for _, arm := range arms {
			b.Run("secondary/c"+itoaU(uint32(common))+"/"+arm.name, func(b *testing.B) {
				benchCompare(b, common, arm.f)
			})
		}
	}
}
