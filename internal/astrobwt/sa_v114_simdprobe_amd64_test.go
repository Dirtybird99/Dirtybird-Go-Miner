//go:build goexperiment.simd && amd64

// Probe only. Compiled solely under GOEXPERIMENT=simd, never shipped, and
// deliberately test-only so no production file carries an experimental import.
// Judged arms use the measured group-size population; the large arms exist to
// show the shape of the curve and must not be quoted as miner workloads.

package astrobwt

import (
	"math/rand"
	"simd"
	"simd/archsimd"
	"testing"
	"unsafe"
)

func TestSimdProbeToolchain(t *testing.T) {
	lanes := simd.BroadcastUint32s(0).Len()
	t.Logf("uint32 lanes=%d (%d-bit) avx2=%v avx512=%v",
		lanes, lanes*32, archsimd.X86.AVX2(), archsimd.X86.AVX512())
	if lanes > 8 {
		t.Fatalf("lanes=%d exceeds the +8 scratch padding the kernels rely on", lanes)
	}
}

// --- probe 1: materializeOrigins under the portable simd package -----------

func probeMaterializeSIMD(dst, src *uint32, count, rel uint32) {
	w := simd.BroadcastUint32s(rel)
	lanes := w.Len()
	n := (int(count) + lanes - 1) / lanes * lanes
	d := unsafe.Slice(dst, n)
	s := unsafe.Slice(src, n)
	for i := 0; i < n; i += lanes {
		simd.LoadUint32s(s[i:]).Add(w).Store(d[i:])
	}
}

func probeMaterializeScalar(dst, src *uint32, count, rel uint32) {
	d := unsafe.Slice(dst, count)
	s := unsafe.Slice(src, count)
	for i := range d {
		d[i] = s[i] + rel
	}
}

// --- probe 2: buildEqualColumns under archsimd ------------------------------
// The portable package cannot express this: its mask types have no bitmask
// extraction, so there is no spelling for the VPMOVMSKB step.

func probeEqualColumnsArchSIMD(first *byte, groupCount uint32, out *[4]uint64) {
	// archsimd's own guidance: keep vector values in variables. An earlier
	// revision held the accumulators in a [8]Mask8x32 array and measured 3.6x
	// slower than the asm purely from spilling that aggregate.
	var zero archsimd.Uint8x32
	all := zero.Equal(zero)
	a0, a1, a2, a3, a4, a5, a6, a7 := all, all, all, all, all, all, all, all
	base := unsafe.Pointer(first)
	for group := uint32(1); group < groupCount; group++ {
		cur := unsafe.Add(base, uintptr(group)<<8)
		a0 = a0.And(archsimd.LoadUint8x32Array((*[32]uint8)(unsafe.Add(base, 0))).
			Equal(archsimd.LoadUint8x32Array((*[32]uint8)(unsafe.Add(cur, 0)))))
		a1 = a1.And(archsimd.LoadUint8x32Array((*[32]uint8)(unsafe.Add(base, 32))).
			Equal(archsimd.LoadUint8x32Array((*[32]uint8)(unsafe.Add(cur, 32)))))
		a2 = a2.And(archsimd.LoadUint8x32Array((*[32]uint8)(unsafe.Add(base, 64))).
			Equal(archsimd.LoadUint8x32Array((*[32]uint8)(unsafe.Add(cur, 64)))))
		a3 = a3.And(archsimd.LoadUint8x32Array((*[32]uint8)(unsafe.Add(base, 96))).
			Equal(archsimd.LoadUint8x32Array((*[32]uint8)(unsafe.Add(cur, 96)))))
		a4 = a4.And(archsimd.LoadUint8x32Array((*[32]uint8)(unsafe.Add(base, 128))).
			Equal(archsimd.LoadUint8x32Array((*[32]uint8)(unsafe.Add(cur, 128)))))
		a5 = a5.And(archsimd.LoadUint8x32Array((*[32]uint8)(unsafe.Add(base, 160))).
			Equal(archsimd.LoadUint8x32Array((*[32]uint8)(unsafe.Add(cur, 160)))))
		a6 = a6.And(archsimd.LoadUint8x32Array((*[32]uint8)(unsafe.Add(base, 192))).
			Equal(archsimd.LoadUint8x32Array((*[32]uint8)(unsafe.Add(cur, 192)))))
		a7 = a7.And(archsimd.LoadUint8x32Array((*[32]uint8)(unsafe.Add(base, 224))).
			Equal(archsimd.LoadUint8x32Array((*[32]uint8)(unsafe.Add(cur, 224)))))
	}
	out[0] = uint64(a0.ToBits()) | uint64(a1.ToBits())<<32
	out[1] = uint64(a2.ToBits()) | uint64(a3.ToBits())<<32
	out[2] = uint64(a4.ToBits()) | uint64(a5.ToBits())<<32
	out[3] = uint64(a6.ToBits()) | uint64(a7.ToBits())<<32
}

// --- equality gates ---------------------------------------------------------

func TestProbeMaterializeSIMDMatchesKernel(t *testing.T) {
	rng := rand.New(rand.NewSource(0xc0ffee))
	const pad = 8
	for _, count := range []uint32{1, 2, 3, 4, 5, 8, 9, 16, 17, 512} {
		src := make([]uint32, int(count)+pad)
		for i := range src {
			src[i] = rng.Uint32() >> 1
		}
		rel := rng.Uint32() >> 1
		want := make([]uint32, int(count)+pad)
		got := make([]uint32, int(count)+pad)
		materializeOrigins(&want[0], &src[0], count, rel)
		probeMaterializeSIMD(&got[0], &src[0], count, rel)
		for i := uint32(0); i < count; i++ {
			if got[i] != want[i] {
				t.Fatalf("count=%d: probe[%d]=%d, kernel=%d", count, i, got[i], want[i])
			}
		}
	}
}

func TestProbeEqualColumnsArchSIMDMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xbeef))
	for _, alphabet := range []int{1, 2, 3, 16, 256} {
		for _, groupCount := range []uint32{0, 1, 2, 3, 4, 5, 8, 9, 16, 64, 256} {
			n := int(groupCount) << 8
			if n == 0 {
				n = 256
			}
			data := make([]byte, n)
			for i := range data {
				data[i] = byte(rng.Intn(alphabet))
			}
			want := referenceEqualColumns(data, groupCount)
			var got [4]uint64
			probeEqualColumnsArchSIMD(&data[0], groupCount, &got)
			if got != want {
				t.Fatalf("alphabet=%d groupCount=%d:\n got %016x\nwant %016x",
					alphabet, groupCount, got, want)
			}
		}
	}
}

// --- benchmarks -------------------------------------------------------------

// judgedMaterializeCounts is the arm the probe is judged on: the hot call site
// is gated on groupCount >= 4 and the counts reaching it are typically 4-8.
var judgedMaterializeCounts = []uint32{4, 5, 8}

// judgedGroupCounts mirrors the measured group-size population; 1-3 group runs
// are 56.4% of it and never reach these kernels.
var judgedGroupCounts = []uint32{3, 4, 5, 8}

func benchMaterialize(b *testing.B, count uint32, f func(dst, src *uint32, count, rel uint32)) {
	const pad = 8
	src := make([]uint32, int(count)+pad)
	for i := range src {
		src[i] = uint32(i) * 251
	}
	dst := make([]uint32, int(count)+pad)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f(&dst[0], &src[0], count, uint32(i))
	}
	b.StopTimer()
	if dst[0] == 0xffffffff {
		b.Fatal("unreachable, keeps the store live")
	}
}

func BenchmarkMaterializeOrigins(b *testing.B) {
	arms := []struct {
		name string
		f    func(dst, src *uint32, count, rel uint32)
	}{
		{"asm", materializeOrigins},
		{"portablesimd", probeMaterializeSIMD},
		{"scalar", probeMaterializeScalar},
	}
	for _, count := range judgedMaterializeCounts {
		for _, arm := range arms {
			b.Run("judged/n"+itoaU(count)+"/"+arm.name, func(b *testing.B) {
				benchMaterialize(b, count, arm.f)
			})
		}
	}
	for _, count := range []uint32{64, 512} {
		for _, arm := range arms {
			b.Run("diagnostic/n"+itoaU(count)+"/"+arm.name, func(b *testing.B) {
				benchMaterialize(b, count, arm.f)
			})
		}
	}
}

func benchEqualColumns(b *testing.B, groupCount uint32, f func(*byte, uint32, *[4]uint64)) {
	n := int(groupCount) << 8
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(i / 251)
	}
	var out [4]uint64
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f(&data[0], groupCount, &out)
	}
	b.StopTimer()
	if out[0] == 0x1234567812345678 {
		b.Fatal("unreachable, keeps the store live")
	}
}

func BenchmarkBuildEqualColumns(b *testing.B) {
	arms := []struct {
		name string
		f    func(*byte, uint32, *[4]uint64)
	}{
		{"asm", buildEqualColumns},
		{"archsimd", probeEqualColumnsArchSIMD},
	}
	for _, groupCount := range judgedGroupCounts {
		for _, arm := range arms {
			b.Run("judged/g"+itoaU(groupCount)+"/"+arm.name, func(b *testing.B) {
				benchEqualColumns(b, groupCount, arm.f)
			})
		}
	}
	for _, groupCount := range []uint32{64, 256} {
		for _, arm := range arms {
			b.Run("diagnostic/g"+itoaU(groupCount)+"/"+arm.name, func(b *testing.B) {
				benchEqualColumns(b, groupCount, arm.f)
			})
		}
	}
}

func itoaU(v uint32) string {
	if v == 0 {
		return "0"
	}
	var buf [10]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
