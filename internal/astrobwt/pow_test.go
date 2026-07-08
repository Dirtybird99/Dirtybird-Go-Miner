package astrobwt

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"testing"

	"go-miner/internal/refpow"
)

type powTest struct {
	out string
	in  string
}

// reference vectors from derohe astrobwtv3/pow_test.go
var referencePowTests = []powTest{
	{"54e2324ddacc3f0383501a9e5760f85d63e9bc6705e9124ca7aef89016ab81ea", "a"},
	{"faeaff767be60134f0bcc5661b5f25413791b4df8ad22ff6732024d35ec4e7d0", "ab"},
	{"715c3d8c61a967b7664b1413f8af5a2a9ba0005922cb0ba4fac8a2d502b92cd6", "abc"},
	{"74cc16efc1aac4768eb8124e23865da4c51ae134e29fa4773d80099c8bd39ab8", "abcd"},
	{"d080d0484272d4498bba33530c809a02a4785368560c5c3eac17b5dacd357c4b", "abcde"},
	{"813e89e0484cbd3fbb3ee059083af53ed761b770d9c245be142c676f669e4607", "abcdef"},
	{"3972fe8fe2c9480e9d4eff383b160e2f05cc855dc47604af37bc61fdf20f21ee", "abcdefg"},
	{"f96191b7e39568301449d75d42d05090e41e3f79a462819473a62b1fcc2d0997", "abcdefgh"},
	{"8c76af6a57dfed744d5b7467fa822d9eb8536a851884aa7d8e3657028d511322", "abcdefghi"},
	{"f838568c38f83034b2ff679d5abf65245bd2be1b27c197ab5fbac285061cf0a7", "abcdefghij"},
}

func TestKAT(t *testing.T) {
	h := New()
	for _, g := range referencePowTests {
		s := fmt.Sprintf("%x", h.Hash([]byte(g.in)))
		if s != g.out {
			t.Fatalf("pow(%q) = %s want %s", g.in, s, g.out)
		}
	}
}

// 48-byte miniblock vector, interleaved with random inputs to catch stale
// scratch state leaking between hashes (from derohe pow_test.go).
func TestRepeat(t *testing.T) {
	data, _ := hex.DecodeString("419ebb000000001bbdc9bf2200000000635d6e4e24829b4249fe0e67878ad4350000000043f53e5436cf610000086b00")

	h := New()
	var random_data [48]byte
	for i := 0; i < 1024; i++ {
		rand.Read(random_data[:])
		if i%2 == 0 {
			hash := fmt.Sprintf("%x", h.Hash(data))
			if hash != "c392762a462fd991ace791bfe858c338c10c23c555796b50f665b636cb8c8440" {
				t.Fatalf("%d test failed hash %s", i, hash)
			}
		} else {
			_ = h.Hash(random_data[:])
		}
	}
}

// Differential test against the verbatim derohe reference (internal/refpow).
func TestDifferentialVsReference(t *testing.T) {
	iters := 2000
	if testing.Short() {
		iters = 200
	}
	rnd := rand.New(rand.NewSource(42))
	h := New()

	lengths := []int{1, 2, 3, 7, 16, 31, 47, 48, 48, 48, 48, 49, 64, 255, 1024}
	buf := make([]byte, 1024)
	for i := 0; i < iters; i++ {
		n := lengths[i%len(lengths)]
		rnd.Read(buf[:n])
		got := h.Hash(buf[:n])
		want := refpow.AstroBWTv3(buf[:n])
		if got != want {
			t.Fatalf("mismatch on input %x: got %x want %x", buf[:n], got, want)
		}
	}
}

func TestZeroAllocs(t *testing.T) {
	h := New()
	var work [48]byte
	rand.Read(work[:])
	allocs := testing.AllocsPerRun(100, func() {
		_ = h.Hash(work[:])
	})
	if allocs != 0 {
		t.Fatalf("Hash allocates %v times per run, want 0", allocs)
	}
}

// BenchmarkHashSAIS measures the SAIS reference backend (New() defaults to
// SAIS on purpose). The production backend is measured by BenchmarkHashV114.
func BenchmarkHashSAIS(b *testing.B) {
	b.ReportAllocs()
	h := New()
	var work [48]byte
	rand.Read(work[:])
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		work[47] = byte(i)
		_ = h.Hash(work[:])
	}
}

func BenchmarkReference48(b *testing.B) {
	b.ReportAllocs()
	var work [48]byte
	rand.Read(work[:])
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		work[47] = byte(i)
		_ = refpow.AstroBWTv3(work[:])
	}
}
