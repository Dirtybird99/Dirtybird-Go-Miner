//go:build amd64

package astrobwt

// Single-stream final hash through the repo's own SHA-NI block asm —
// test-only prototype for the D1 decision (is minio leaving SHA-NI
// throughput on the table on this host?).

import (
	stdsha "crypto/sha256"
	"testing"
)

func sha256Sum256SingleNI(p []byte) [32]byte {
	st := sha256IV
	full := len(p) &^ 63
	if full > 0 {
		sha256BlocksNI(&st, &p[0], full>>6)
	}
	var out [32]byte
	sha256FinishNI(&st, p, full, &out)
	return out
}

func TestSHA256SingleNIMatchesStdlib(t *testing.T) {
	if !useSHANI {
		t.Skip("no SHA-NI on this host")
	}
	lengths := []int{1, 55, 56, 63, 64, 65, 119, 120, 127, 128, 192, 4096, saBufLen}
	for _, n := range lengths {
		buf := make([]byte, n)
		for i := range buf {
			buf[i] = byte(i*131 + n)
		}
		if got, want := sha256Sum256SingleNI(buf), stdsha.Sum256(buf); got != want {
			t.Fatalf("len %d: singleNI %x != stdlib %x", n, got, want)
		}
	}
}

func BenchmarkSHA256SingleNI283K(b *testing.B) {
	if !useSHANI {
		b.Skip("no SHA-NI on this host")
	}
	benchSHA256(b, saBufLen, sha256Sum256SingleNI)
}

func BenchmarkSHA256SingleNI48(b *testing.B) {
	if !useSHANI {
		b.Skip("no SHA-NI on this host")
	}
	benchSHA256(b, 48, sha256Sum256SingleNI)
}
