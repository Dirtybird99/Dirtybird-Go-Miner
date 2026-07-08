package astrobwt

// Final-SHA yardstick: the fast miners spend ~276K cyc (~1 byte/cyc) hashing
// the ~283 KB SA buffer. If neither implementation gets near that here,
// SHA-NI is not engaging and the final hash is a co-culprit (the Rust port
// once lost 98.5% of its hash time to exactly this).

import (
	stdsha "crypto/sha256"
	"testing"

	miniosha "github.com/minio/sha256-simd"
)

func benchSHA256(b *testing.B, size int, sum func([]byte) [32]byte) {
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = byte(i * 131)
	}
	b.SetBytes(int64(size))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sum(buf)
	}
}

const saBufLen = int(MAX_LENGTH) * 4 // 283648, the final-hash input size

func BenchmarkSHA256Minio283K(b *testing.B)  { benchSHA256(b, saBufLen, miniosha.Sum256) }
func BenchmarkSHA256Stdlib283K(b *testing.B) { benchSHA256(b, saBufLen, stdsha.Sum256) }
func BenchmarkSHA256Minio48(b *testing.B)    { benchSHA256(b, 48, miniosha.Sum256) }
func BenchmarkSHA256Stdlib48(b *testing.B)   { benchSHA256(b, 48, stdsha.Sum256) }
