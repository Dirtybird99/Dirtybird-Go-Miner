package miner

import (
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"

	"go-miner/internal/getwork"
)

// MiniblockSize is derohe block.MINIBLOCK_SIZE.
const MiniblockSize = 48

var (
	ErrBadBlob    = errors.New("blockhashing_blob is not 48 bytes")
	ErrBadVersion = errors.New("unsupported miniblock version, check for a miner update")
	ErrBadDiff    = errors.New("job difficulty is zero")
)

// State is the shared job snapshot plus counters (the zig miner's state.zig
// model). Workers poll Epoch and re-snapshot the job when it changes.
type State struct {
	mu     sync.Mutex
	blob   [MiniblockSize]byte
	jobid  string
	target [32]byte
	epoch  atomic.Uint64 // bumped on every job change; 0 = no job yet

	Height atomic.Uint64
	Diff   atomic.Uint64

	// local counters
	TotalHashes atomic.Uint64
	Submitted   atomic.Uint64
	Stale       atomic.Uint64 // submits dropped because the mailbox was full

	// mirrored from job pushes (authoritative accept/reject accounting)
	Blocks     atomic.Uint64
	MiniBlocks atomic.Uint64
	Rejected   atomic.Uint64
}

// SetJob validates and installs a pushed job. It always mirrors the daemon
// counters; it bumps the epoch only when the work itself changed.
func (s *State) SetJob(j getwork.Job) (changed bool, err error) {
	s.Height.Store(j.Height)
	s.Diff.Store(j.Difficultyuint64)
	s.Blocks.Store(j.Blocks)
	s.MiniBlocks.Store(j.MiniBlocks)
	s.Rejected.Store(j.Rejected)

	var blob [MiniblockSize]byte
	n, err := hex.Decode(blob[:], []byte(j.Blockhashing_blob))
	if err != nil || n != MiniblockSize || len(j.Blockhashing_blob) != MiniblockSize*2 {
		return false, ErrBadBlob
	}
	if blob[0]&0xf != 1 { // derohe miner.go version-nibble check
		return false, ErrBadVersion
	}
	if j.Difficultyuint64 == 0 {
		return false, ErrBadDiff
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if blob == s.blob && j.JobID == s.jobid {
		return false, nil
	}
	s.blob = blob
	s.jobid = j.JobID
	s.target = ComputeTarget(j.Difficultyuint64)
	s.epoch.Add(1)
	return true, nil
}

// Job returns a consistent snapshot of the current work.
func (s *State) Job() (blob [MiniblockSize]byte, jobid string, target [32]byte, epoch uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.blob, s.jobid, s.target, s.epoch.Load()
}

// Epoch is the cheap per-hash staleness check.
func (s *State) Epoch() uint64 { return s.epoch.Load() }
