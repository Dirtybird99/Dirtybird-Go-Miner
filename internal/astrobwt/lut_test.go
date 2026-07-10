package astrobwt

import (
	"bytes"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"go-miner/internal/refpow"
)

// The op loop runs at most 32 iterations (pos2-pos1 is masked to 0x1f), so a
// per-call table build could never amortize. Guard the constant the whole
// design rests on.
func TestOpLoopIsBounded(t *testing.T) {
	for hi := 0; hi < 256; hi++ {
		for lo := 0; lo <= hi; lo++ {
			pos1, pos2 := byte(lo), byte(hi)
			if pos2-pos1 > 32 {
				pos2 = pos1 + (pos2-pos1)&0x1f
			}
			if pos2 < pos1 || pos2-pos1 > 32 {
				t.Fatalf("pos1=%d pos2=%d: span %d out of range", pos1, pos2, pos2-pos1)
			}
		}
	}
}

func TestLUTTableIntegrity(t *testing.T) {
	rows := 0
	for op := 0; op < 256; op++ {
		row := opRow[op]
		if !isPureOp(byte(op)) {
			if row != -1 {
				t.Fatalf("op %d is not pure but opRow=%d", op, row)
			}
			continue
		}
		rows++
		if row < 0 || int(row) >= pureOpCount {
			t.Fatalf("op %d: opRow=%d out of range", op, row)
		}
		for x := 0; x < 256; x++ {
			if got, want := opLUT[row][x], pureOp(byte(op), byte(x)); got != want {
				t.Fatalf("opLUT[%d][%d] = %d, want %d (op %d)", row, x, got, want, op)
			}
		}
	}
	if rows != pureOpCount {
		t.Fatalf("pureOpSet has %d ops, pureOpCount = %d", rows, pureOpCount)
	}
}

// Distinct ops must not collide onto one row, or an op would silently apply
// another op's transform.
func TestLUTRowsAreDistinct(t *testing.T) {
	seen := make(map[int16]int, pureOpCount)
	for op := 0; op < 256; op++ {
		row := opRow[op]
		if row < 0 {
			continue
		}
		if prev, dup := seen[row]; dup {
			t.Fatalf("ops %d and %d share row %d", prev, op, row)
		}
		seen[row] = op
	}
}

// The tables are derived from pow.go's switch. If someone edits an op body
// without regenerating, the tables go stale and the miner produces wrong hashes.
func TestGeneratedFileUpToDate(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the generator")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	// `go test` runs us in the package dir; a compiled test binary launched from
	// elsewhere (as the pinned benchmark runs do) has no sources to regenerate.
	if _, err := os.Stat("lutgen"); err != nil {
		t.Skip("not running in the package source dir")
	}
	got := filepath.Join(t.TempDir(), "pureop_gen.go")
	out, err := exec.Command("go", "run", "./lutgen", "-o", got).CombinedOutput()
	if err != nil {
		t.Fatalf("go run ./lutgen: %v\n%s", err, out)
	}
	fresh, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile("pureop_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	fresh = bytes.ReplaceAll(fresh, []byte("\r\n"), []byte("\n"))
	committed = bytes.ReplaceAll(committed, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(fresh, committed) {
		t.Fatal("pureop_gen.go is stale; run `go generate lut.go` from internal/astrobwt")
	}
}

// The real gate: a full hash must stay byte-identical to the verbatim derohe
// reference. Under -tags lut this exercises the table kernel; without the tag it
// re-checks the branchy switch. Both backends share the op loop.
func TestLUTDifferentialVsReference(t *testing.T) {
	iters := 5000
	if testing.Short() {
		iters = 500
	}
	lengths := []int{1, 2, 3, 7, 16, 31, 47, 48, 48, 48, 48, 49, 64, 255, 1024}

	for _, bk := range []struct {
		name string
		b    Backend
	}{{"SAIS", BackendSAIS}, {"V114", BackendV114}} {
		t.Run(bk.name, func(t *testing.T) {
			rnd := rand.New(rand.NewSource(1337))
			h := NewWithBackend(bk.b)
			buf := make([]byte, 1024)
			for i := 0; i < iters; i++ {
				n := lengths[i%len(lengths)]
				rnd.Read(buf[:n])
				if got, want := h.Hash(buf[:n]), refpow.AstroBWTv3(buf[:n]); got != want {
					t.Fatalf("useLUT=%v mismatch on %x:\n got %x\nwant %x", useLUT, buf[:n], got, want)
				}
			}
		})
	}
}
