package miner

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-miner/internal/astrobwt"
	"go-miner/internal/getwork"
)

// Exercises the worker/state/submit concurrency under `go test -race`:
// 4 real workers grind while jobs churn every 50ms at difficulty 1 (every
// hash is a share), draining through the submit mailbox like main does.
func TestWorkersUnderJobChurn(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	st := &State{}
	submits := make(chan getwork.Submit, 16)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for tid := 0; tid < 4; tid++ {
		wg.Add(1)
		go func(tid int) {
			defer wg.Done()
			// half the workers pair-hash, half single-hash
			Run(ctx, tid, st, submits, nil, astrobwt.BackendV114, tid%2 == 0)
		}(tid)
	}

	var submitted atomic.Int64
	var unstamped atomic.Int64
	go func() {
		for s := range submits {
			submitted.Add(1)
			// SetJob starts epochs at 1, so 0 means the worker never stamped
			// the share and the writer's epoch gate would be inert
			if s.Epoch == 0 {
				unstamped.Add(1)
			}
		}
	}()

	blob := make([]byte, MiniblockSize)
	blob[0] = 0x41
	for i := uint32(0); ctx.Err() == nil; i++ {
		binary.BigEndian.PutUint32(blob[8:], i) // change the work each push
		if _, err := st.SetJob(getwork.Job{
			JobID:             hex.EncodeToString(blob[8:12]),
			Blockhashing_blob: hex.EncodeToString(blob),
			Difficultyuint64:  1,
			Height:            uint64(i),
		}); err != nil {
			t.Errorf("SetJob: %v", err)
			return
		}
		// alternate invalidation into the churn so workers race SetJob,
		// Invalidate, and Submit concurrently under -race
		if i%2 == 1 {
			st.Invalidate()
		}
		time.Sleep(50 * time.Millisecond)
	}
	<-ctx.Done()
	wg.Wait() // workers must exit before the mailbox can close
	close(submits)

	if st.TotalHashes.Load() == 0 {
		t.Fatal("workers hashed nothing")
	}
	if st.Submitted.Load() == 0 {
		t.Fatal("no shares submitted at difficulty 1")
	}
	if n := unstamped.Load(); n != 0 {
		t.Fatalf("%d share(s) carried epoch 0 — workers are not stamping submits", n)
	}
	t.Logf("hashes=%d submitted=%d drained=%d epoch=%d",
		st.TotalHashes.Load(), st.Submitted.Load(), submitted.Load(), st.Epoch())
}

func TestWorkerStopsWhileJobIsInvalid(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	st := &State{}
	j := validJob()
	j.Difficultyuint64 = 1 << 62
	if _, err := st.SetJob(j); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Run(ctx, 0, st, make(chan getwork.Submit, 1), nil, astrobwt.BackendV114, false)
		close(done)
	}()
	waitForHashes := func(after uint64) uint64 {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			if n := st.TotalHashes.Load(); n > after {
				return n
			}
			select {
			case <-deadline:
				t.Fatalf("hash count did not advance past %d", after)
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	waitForHashes(0)
	st.Invalidate()
	time.Sleep(200 * time.Millisecond) // let the in-flight hash and local counter flush finish
	paused := st.TotalHashes.Load()
	time.Sleep(200 * time.Millisecond)
	if got := st.TotalHashes.Load(); got != paused {
		t.Fatalf("worker hashed invalid work: count advanced from %d to %d", paused, got)
	}
	if changed, err := st.SetJob(j); err != nil || !changed {
		t.Fatalf("reactivate identical job: changed=%v err=%v", changed, err)
	}
	waitForHashes(paused)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop after cancellation")
	}
}

// TestWorkerSubmitsVerifiableShares is the only place anything checks that a
// submitted blob is the blob the worker actually hashed. Every other gate
// stops at the hash function: the million-hash differential and --selftest
// call Hash, never HashPair, and the churn test above asserts only that
// shares arrive with a stamped epoch. That left the worker's two-lane branch
// - the one every amd64 build takes since pairing became the default - with
// no correctness gate at all, so a lane mix-up would surface as pool-side
// rejects rather than as a red test.
//
// The check is deliberately end-to-end: decode what was submitted, re-run the
// PoW over those exact bytes, and require it to meet the job's target. A blob
// whose nonce does not belong to the hash that qualified it fails that, which
// is precisely the lane-crossing failure mode. Nonce uniqueness and the
// thread-id byte are checked alongside, and both pipelines run so the
// assertions cannot be vacuous for one of them.
func TestWorkerSubmitsVerifiableShares(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	for _, tc := range []struct {
		name string
		pair bool
	}{{"x1", false}, {"x2", true}} {
		t.Run(tc.name, func(t *testing.T) {
			// 256 is low enough to yield shares in a second and high enough
			// that both lanes winning the same iteration is ~1/65536, so a
			// lane mix-up cannot hide behind a double win.
			const difficulty = 256
			const tid = 7

			blob := make([]byte, MiniblockSize)
			for i := range blob {
				blob[i] = byte(i * 7)
			}
			blob[0] = 0x41 // miniblock version nibble the miner requires

			st := &State{}
			if _, err := st.SetJob(getwork.Job{
				JobID:             "job-verify",
				Blockhashing_blob: hex.EncodeToString(blob),
				Difficultyuint64:  difficulty,
				Height:            4242,
			}); err != nil {
				t.Fatalf("SetJob: %v", err)
			}
			target := ComputeTarget(difficulty)

			submits := make(chan getwork.Submit, 256)
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()

			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				Run(ctx, tid, st, submits, nil, astrobwt.BackendV114, tc.pair)
			}()

			collected := make(chan []getwork.Submit, 1)
			go func() {
				var got []getwork.Submit
				for s := range submits {
					got = append(got, s)
				}
				collected <- got
			}()

			<-ctx.Done()
			wg.Wait()
			close(submits)
			shares := <-collected

			if len(shares) == 0 {
				t.Fatalf("no shares submitted at difficulty %d in 4s (hashes=%d)", difficulty, st.TotalHashes.Load())
			}

			h := astrobwt.NewWithBackend(astrobwt.BackendV114)
			seen := make(map[uint32]int, len(shares))
			for i, s := range shares {
				if s.JobID != "job-verify" {
					t.Fatalf("share %d: JobID = %q, want job-verify", i, s.JobID)
				}
				raw, err := hex.DecodeString(s.Blob)
				if err != nil {
					t.Fatalf("share %d: blob is not hex: %v", i, err)
				}
				if len(raw) != MiniblockSize {
					t.Fatalf("share %d: blob is %d bytes, want %d", i, len(raw), MiniblockSize)
				}
				if raw[47] != tid {
					t.Fatalf("share %d: thread id byte = %d, want %d", i, raw[47], tid)
				}
				pow := h.Hash(raw)
				if !MeetsTarget(&pow, &target) {
					t.Fatalf("share %d: re-hashing the submitted blob does not meet the target; the submitted bytes are not the ones that were hashed (blob %s pow %x nonce %d)", i, s.Blob, pow, binary.BigEndian.Uint32(raw[43:47]))
				}
				nonce := binary.BigEndian.Uint32(raw[43:47])
				if prev, dup := seen[nonce]; dup {
					t.Fatalf("share %d repeats nonce %d already submitted as share %d", i, nonce, prev)
				}
				seen[nonce] = i
			}
			t.Logf("%s: %d shares, all re-verified against the target; hashes=%d", tc.name, len(shares), st.TotalHashes.Load())
		})
	}
}
