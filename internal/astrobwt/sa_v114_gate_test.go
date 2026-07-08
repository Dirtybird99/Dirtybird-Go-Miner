package astrobwt

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

// TestV114MillionHashGate is Gate G2: N-hash differential (v114 vs SAIS)
// across all cores. Enabled by V114_GATE_HASHES (e.g. 1000000); skipped
// otherwise so normal `go test` stays fast.
func TestV114MillionHashGate(t *testing.T) {
	nStr := os.Getenv("V114_GATE_HASHES")
	if nStr == "" {
		t.Skip("set V114_GATE_HASHES=1000000 to run the full gate")
	}
	total, err := strconv.Atoi(nStr)
	if err != nil || total <= 0 {
		t.Fatalf("bad V114_GATE_HASHES: %q", nStr)
	}

	workers := runtime.NumCPU()
	per := (total + workers - 1) / workers
	var done atomic.Int64
	var failed atomic.Bool
	var once sync.Once
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(seed))
			hv := NewWithBackend(BackendV114)
			hs := NewWithBackend(BackendSAIS)
			var work [48]byte
			for i := 0; i < per && !failed.Load(); i++ {
				rnd.Read(work[:])
				got := hv.Hash(work[:])
				want := hs.Hash(work[:])
				if got != want {
					failed.Store(true)
					once.Do(func() {
						t.Errorf("MISMATCH input %x: v114 %x, sais %x", work, got, want)
					})
					return
				}
				done.Add(1)
			}
		}(int64(1000 + w))
	}
	wg.Wait()

	if failed.Load() {
		t.Fatalf("gate failed after %d hashes", done.Load())
	}
	fmt.Printf("V114 GATE: %d/%d hashes matched, %d fallbacks\n",
		done.Load(), total, V114Fallbacks())
}
