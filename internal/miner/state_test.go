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
	if err != nil || !changed || st.Epoch() != 1 {
		t.Fatalf("first job: changed=%v err=%v epoch=%d", changed, err, st.Epoch())
	}
	changed, err = st.SetJob(j) // identical push (the ~500ms keepalive)
	if err != nil || changed || st.Epoch() != 1 {
		t.Fatalf("identical job: changed=%v err=%v epoch=%d", changed, err, st.Epoch())
	}
	j.JobID = "j2"
	changed, _ = st.SetJob(j)
	if !changed || st.Epoch() != 2 {
		t.Fatalf("new jobid: changed=%v epoch=%d", changed, st.Epoch())
	}
	if st.MiniBlocks.Load() != 3 || st.Height.Load() != 100 || st.Diff.Load() != 20000 {
		t.Fatal("counters not mirrored")
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

	if st.Epoch() != 0 {
		t.Fatal("rejected jobs must not bump the epoch")
	}
}
