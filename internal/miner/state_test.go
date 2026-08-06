package miner

import (
	"encoding/hex"
	"testing"

	"go-miner/internal/getwork"
)

func validJob() getwork.Job {
	blob := make([]byte, MiniblockSize)
	blob[0] = 0x41 // version nibble 1 (as real templates have)
	return getwork.Job{
		JobID:             "j1",
		Blockhashing_blob: hex.EncodeToString(blob),
		Difficultyuint64:  20000,
		Height:            100,
		MiniBlocks:        3,
	}
}

func TestSetJobBumpsEpochOnChangeOnly(t *testing.T) {
	st := &State{}
	j := validJob()
	changed, err := st.SetJob(j)
	if err != nil || !changed || st.Epoch() != 1 || !st.Active() {
		t.Fatalf("first job: changed=%v err=%v epoch=%d active=%v", changed, err, st.Epoch(), st.Active())
	}
	j.MiniBlocks = 4
	j.Blocks = 1
	j.Rejected = 2
	changed, err = st.SetJob(j) // counter-only keepalive
	if err != nil || changed || st.Epoch() != 1 {
		t.Fatalf("keepalive job: changed=%v err=%v epoch=%d", changed, err, st.Epoch())
	}
	if st.MiniBlocks.Load() != 4 || st.Blocks.Load() != 1 || st.Rejected.Load() != 2 {
		t.Fatal("counter-only keepalive did not mirror counters")
	}

	j.Difficultyuint64 = 30000
	changed, err = st.SetJob(j)
	if err != nil || !changed || st.Epoch() != 2 {
		t.Fatalf("difficulty change: changed=%v err=%v epoch=%d", changed, err, st.Epoch())
	}
	_, _, target, epoch := st.Job()
	if epoch != 2 || target != ComputeTarget(30000) {
		t.Fatalf("target snapshot not refreshed on difficulty change: epoch=%d target=%x", epoch, target)
	}

	j.JobID = "j2"
	changed, _ = st.SetJob(j)
	if !changed || st.Epoch() != 3 {
		t.Fatalf("new jobid: changed=%v epoch=%d", changed, st.Epoch())
	}
	if st.MiniBlocks.Load() != 4 || st.Height.Load() != 100 || st.Diff.Load() != 30000 {
		t.Fatal("counters not mirrored")
	}
}

func TestInvalidateStopsAndReactivatesIdenticalJob(t *testing.T) {
	st := &State{}
	if st.Active() {
		t.Fatal("zero State is active")
	}
	j := validJob()
	if changed, err := st.SetJob(j); err != nil || !changed {
		t.Fatalf("seed job: changed=%v err=%v", changed, err)
	}
	st.Invalidate()
	if st.Active() || st.Epoch() != 2 {
		t.Fatalf("invalidated state: active=%v epoch=%d, want false/2", st.Active(), st.Epoch())
	}
	st.Invalidate()
	if st.Epoch() != 2 {
		t.Fatalf("repeated invalidation bumped epoch to %d", st.Epoch())
	}
	if changed, err := st.SetJob(j); err != nil || !changed || !st.Active() || st.Epoch() != 3 {
		t.Fatalf("reactivation: changed=%v err=%v active=%v epoch=%d", changed, err, st.Active(), st.Epoch())
	}
}

func TestSetJobRejectsBadInput(t *testing.T) {
	st := &State{}

	j := validJob()
	j.Blockhashing_blob = "zz"
	if _, err := st.SetJob(j); err == nil {
		t.Fatal("want error for bad hex")
	}

	j = validJob()
	j.Blockhashing_blob = j.Blockhashing_blob[:94] // 47 bytes
	if _, err := st.SetJob(j); err == nil {
		t.Fatal("want error for short blob")
	}

	j = validJob()
	blob := make([]byte, MiniblockSize)
	blob[0] = 0x42 // version nibble 2
	j.Blockhashing_blob = hex.EncodeToString(blob)
	if _, err := st.SetJob(j); err != ErrBadVersion {
		t.Fatal("want ErrBadVersion for unknown version nibble")
	}

	j = validJob()
	j.Difficultyuint64 = 0
	if _, err := st.SetJob(j); err != ErrBadDiff {
		t.Fatal("want ErrBadDiff for zero difficulty")
	}

	j = validJob()
	j.JobID = ""
	if _, err := st.SetJob(j); err != ErrBadJobID {
		t.Fatal("want ErrBadJobID for empty jobid")
	}

	if st.Epoch() != 0 || st.Active() {
		t.Fatal("rejected jobs must not publish active work")
	}
}

func TestSetJobRejectsBadInputWithoutPublishing(t *testing.T) {
	st := &State{}
	j := validJob()
	j.Blocks = 2
	j.Rejected = 1
	if changed, err := st.SetJob(j); err != nil || !changed {
		t.Fatalf("seed job: changed=%v err=%v", changed, err)
	}

	badVersion := make([]byte, MiniblockSize)
	badVersion[0] = 0x42
	cases := []struct {
		name string
		edit func(*getwork.Job)
		want error
	}{
		{"bad blob", func(j *getwork.Job) { j.Blockhashing_blob = "zz" }, ErrBadBlob},
		{"bad version", func(j *getwork.Job) { j.Blockhashing_blob = hex.EncodeToString(badVersion) }, ErrBadVersion},
		{"zero diff", func(j *getwork.Job) { j.Difficultyuint64 = 0 }, ErrBadDiff},
		{"empty jobid", func(j *getwork.Job) { j.JobID = "" }, ErrBadJobID},
	}
	for _, tc := range cases {
		bad := validJob()
		bad.Height = 999
		bad.Difficultyuint64 = 30000
		bad.MiniBlocks = 9
		bad.Blocks = 8
		bad.Rejected = 7
		tc.edit(&bad)
		if _, err := st.SetJob(bad); err != tc.want {
			t.Fatalf("%s err=%v, want %v", tc.name, err, tc.want)
		}
		if st.Epoch() != 1 || st.Height.Load() != 100 || st.Diff.Load() != 20000 ||
			st.MiniBlocks.Load() != 3 || st.Blocks.Load() != 2 || st.Rejected.Load() != 1 {
			t.Fatalf("%s published state: epoch=%d height=%d diff=%d mb=%d blocks=%d rejected=%d",
				tc.name, st.Epoch(), st.Height.Load(), st.Diff.Load(), st.MiniBlocks.Load(),
				st.Blocks.Load(), st.Rejected.Load())
		}
	}
}
