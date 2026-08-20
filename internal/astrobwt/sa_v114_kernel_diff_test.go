package astrobwt

import (
	"math/rand"
	"testing"
)

func referenceEqualColumns(data []byte, groupCount uint32) [4]uint64 {
	out := [4]uint64{^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0)}
	for group := uint32(1); group < groupCount; group++ {
		for column := 0; column < 256; column++ {
			if data[int(group)<<8+column] != data[column] {
				out[column>>6] &^= uint64(1) << uint(column&63)
			}
		}
	}
	return out
}

func TestBuildEqualColumnsMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5eed))
	groupCounts := []uint32{0, 1, 2, 3, 4, 5, 7, 8, 9, 15, 16, 17, 33, 64, 127, 256}

	// alphabet 1 makes almost every column equal, 256 almost none; the
	// narrow ones are what the miner actually feeds this kernel.
	for _, alphabet := range []int{1, 2, 3, 16, 256} {
		for _, groupCount := range groupCounts {
			for trial := 0; trial < 8; trial++ {
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
				buildEqualColumns(&data[0], groupCount, &got)
				if got != want {
					t.Fatalf("alphabet=%d groupCount=%d trial=%d:\n got %016x\nwant %016x",
						alphabet, groupCount, trial, got, want)
				}
			}
		}
	}
}

func TestBuildEqualColumnsIsolatesEachColumn(t *testing.T) {
	// One differing column at a time: catches a kernel that drops or
	// misplaces a single lane, which random data can mask.
	for column := 0; column < 256; column++ {
		data := make([]byte, 5<<8)
		data[1<<8+column] = 1
		var got [4]uint64
		buildEqualColumns(&data[0], 5, &got)
		for c := 0; c < 256; c++ {
			equal := got[c>>6]&(uint64(1)<<uint(c&63)) != 0
			if want := c != column; equal != want {
				t.Fatalf("differing column %d: column %d equal=%v, want %v", column, c, equal, want)
			}
		}
	}
}

func TestMaterializeOriginsMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xd00d))
	const pad = 8
	for _, count := range []uint32{1, 2, 3, 4, 5, 7, 8, 9, 15, 16, 17, 63, 64, 65, 512} {
		for trial := 0; trial < 8; trial++ {
			src := make([]uint32, int(count)+pad)
			for i := range src {
				src[i] = rng.Uint32() >> 1
			}
			rel := rng.Uint32() >> 1
			dst := make([]uint32, int(count)+pad)
			guard := uint32(0xdeadbeef)
			for i := range dst {
				dst[i] = guard
			}
			materializeOrigins(&dst[0], &src[0], count, rel)
			for i := uint32(0); i < count; i++ {
				if want := src[i] + rel; dst[i] != want {
					t.Fatalf("count=%d trial=%d: dst[%d]=%d, want %d", count, trial, i, dst[i], want)
				}
			}
			// The kernel may round up to its vector width; it must not run
			// past the padding the scratch allocator guarantees.
			for i := int(count) + pad; i < len(dst); i++ {
				if dst[i] != guard {
					t.Fatalf("count=%d: wrote past padding at %d", count, i)
				}
			}
		}
	}
}
