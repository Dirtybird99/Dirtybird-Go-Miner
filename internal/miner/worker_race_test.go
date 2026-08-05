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
	go func() {
		for range submits {
			submitted.Add(1)
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
