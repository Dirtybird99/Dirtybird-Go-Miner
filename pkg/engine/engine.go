// Copyright 2017-2026 DERO Project. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.
//
// Package engine is the embeddable mining engine built on go-miner's
// internal pipeline (getwork client + AstroBWTv3 workers + share state).
//
// Unlike the standalone go-miner CLI, engine.Start does not disable the Go
// GC nor raise the process memory limit: it is meant to live inside another
// process (a wallet GUI/TUI), where a 2 GiB heap cap would fight the host
// application. Callers wanting the CLI's steady-state behavior should keep
// running go-miner as a binary instead.
package engine

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"go-miner/internal/astrobwt"
	"go-miner/internal/getwork"
	"go-miner/internal/miner"
)

// Version is the go-miner release this engine was built from. Bump it when
// upstream tags a new release.
const Version = "0.2.18"

const (
	// MaxThreads caps the worker count: the thread id lives in nonce byte 47.
	MaxThreads = 255
	// submitBuffer is how many found shares may queue before workers drop
	// rather than stall the hot loop (family convention).
	submitBuffer = 16
)

// ErrBrokenHash is returned by Start when the AstroBWTv3 pow("a") known
// answer test fails: the hash pipeline is broken and mining would be
// meaningless (and shares would be rejected).
var ErrBrokenHash = errors.New("AstroBWTv3 self-test failed; refusing to mine")

// ErrInvalidConfig is returned by Start for an empty endpoint or wallet.
var ErrInvalidConfig = errors.New("engine: endpoint and wallet are required")

// DefaultPair mirrors the CLI default: 2-way batched final hashing on for
// arm64 with SHA2 extensions, opt-in elsewhere.
func DefaultPair() bool {
	return runtime.GOARCH == "arm64" && astrobwt.PairHashSupported()
}

// DefaultBackendName is the suffix-array implementation used unless Config
// overrides it ("v114" — the v1.14 descriptor SA, ~2x the SAIS reference
// speed).
const DefaultBackendName = "v114"

func (c Config) backend() (astrobwt.Backend, error) {
	switch c.Backend {
	case "":
		return astrobwt.BackendV114, nil
	case "v114":
		return astrobwt.BackendV114, nil
	case "sais":
		return astrobwt.BackendSAIS, nil
	default:
		return 0, fmt.Errorf("engine: unknown backend %q (want \"v114\" or \"sais\")", c.Backend)
	}
}

// NormalizeThreads clamps a requested thread count into [1, MaxThreads],
// defaulting to the logical CPU count for n <= 0.
func NormalizeThreads(n int) int {
	if n <= 0 {
		n = runtime.NumCPU()
	}
	if n > MaxThreads {
		n = MaxThreads
	}
	if n < 1 {
		n = 1
	}
	return n
}

// Config is the immutable input to Start.
type Config struct {
	// Endpoint is the daemon/pool getwork address. Accepts
	// [ws://|wss://]host:port; a bare host:port implies wss (DERO getwork is
	// TLS). ws:// forces plaintext (only for getwork behind a TLS-terminating
	// proxy).
	Endpoint string
	// Wallet is the DERO address payouts go to.
	Wallet string
	// Threads is the worker count; 0 = logical CPU count, capped at
	// MaxThreads. Thread id lives in nonce byte 47 (max 255).
	Threads int
	// Pair enables the 2-way batched final hash (2 nonces/thread). Defaults
	// to DefaultPair() when zero-valued.
	Pair bool
	// Pin enables P-core-first thread pinning where supported (Windows amd64
	// <= 64 logical CPUs in v1; a no-op elsewhere).
	Pin bool
	// Backend selects the suffix-array implementation; "" or "v114" selects
	// the v1.14 descriptor SA, "sais" the reference SAIS. DefaultBackendName
	// is the default.
	Backend string
	// Debug enables per-job / per-share logging chatter through Logf.
	Debug bool
	// Logf receives leveled log lines ("INFO"/"ERROR"/"DEBUG"/"WARN").
	// May be nil; messages are dropped then.
	Logf func(level, format string, args ...interface{})
}

// Stats is a cheap, atomic snapshot of engine activity. All reads hit
// atomic fields; Hashrate is the only derived value (sliding window).
type Stats struct {
	Running    bool
	Connected  bool
	Hashrate   float64 // H/s over the last ~10s window
	Hashes     uint64
	MiniBlocks uint64
	Blocks     uint64
	Rejected   uint64
	Height     uint64
	Difficulty uint64
	Endpoint   string
	Wallet     string
	Threads    int
}

// Engine drives the getwork client plus one worker per thread. Create it with
// Start; Stop returns all goroutines before returning. Stats is safe to call
// concurrently.
type Engine struct {
	cfg     Config
	client  *getwork.Client
	state   *miner.State
	submits chan getwork.Submit

	cancel context.CancelFunc
	done   chan struct{} // closed when client + workers have all exited
	wg     sync.WaitGroup

	rate *hashrateWindow
	mu   sync.Mutex // guards the rate window against Stats/ sampler
}

func (e *Engine) logf(level, format string, args ...interface{}) {
	if e.cfg.Logf != nil {
		e.cfg.Logf(level, format, args...)
	}
}

func (e *Engine) pair() bool {
	if e.cfg.Pair {
		return true
	}
	// cfg.Pair is a plain bool; DefaultPair() only when it was left off AND
	// the platform default wants it. A caller disabling pair on arm64 cannot
	// be distinguished here, so the CLI-level override stays a CLI concern.
	return DefaultPair()
}

// Start validates the config, runs the AstroBWTv3 known-answer test, and
// launches the getwork client plus one worker per thread. It returns
// immediately; the first job arrives from the daemon ~500ms later. Cancel ctx
// (or call Stop) to tear everything down.
func Start(ctx context.Context, cfg Config) (*Engine, error) {
	if cfg.Endpoint == "" || cfg.Wallet == "" {
		return nil, ErrInvalidConfig
	}
	threads := NormalizeThreads(cfg.Threads)
	cfg.Threads = threads

	backend, err := cfg.backend()
	if err != nil {
		return nil, err
	}

	// Refuse to mine against a broken hash pipeline (same gate as the CLI):
	// KAT the resolved backend with pow("a").
	h := astrobwt.NewWithBackend(backend)
	if got := fmt.Sprintf("%x", h.Hash([]byte("a"))); got != katHash {
		return nil, fmt.Errorf("%w: pow(\"a\") = %s", ErrBrokenHash, got)
	}

	ctx, cancel := context.WithCancel(ctx)
	e := &Engine{
		cfg:     cfg,
		state:   &miner.State{},
		submits: make(chan getwork.Submit, submitBuffer),
		cancel:  cancel,
		done:    make(chan struct{}),
		rate:    newHashrateWindow(time.Now(), 0),
	}

	e.client = &getwork.Client{
		Endpoint:     cfg.Endpoint,
		Wallet:       cfg.Wallet,
		Submits:      e.submits,
		OnDisconnect: e.state.Invalidate,
		SubmitValid: func(s getwork.Submit) bool {
			return e.state.Active() && e.state.Epoch() == s.Epoch
		},
		Logf: func(format string, args ...interface{}) {
			e.logf("INFO", format, args...)
		},
		Errorf: func(format string, args ...interface{}) {
			e.logf("ERROR", format, args...)
		},
	}
	if cfg.Debug {
		e.client.Debugf = func(format string, args ...interface{}) {
			e.logf("DEBUG", format, args...)
		}
	}
	e.client.OnJob = e.onJob

	var pinOrder []int
	if cfg.Pin {
		pinOrder = miner.PinOrder()
	}
	for t := 0; t < cfg.Threads; t++ {
		t := t
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			miner.Run(ctx, t, e.state, e.submits, pinOrder, backend, e.pair())
		}()
	}

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.client.Run(ctx)
	}()
	go e.sampleLoop(ctx)

	go func() {
		e.wg.Wait()
		close(e.done)
	}()
	return e, nil
}

// onJob installs a pushed job into the shared state. It mirrors the CLI's
// rejection policy: a version-nibble failure means the miner cannot mine the
// current chain (stop hashing until an update); every other rejection is the
// shape of a keepalive/status frame and the last good job stays mineable.
func (e *Engine) onJob(j getwork.Job) bool {
	if e.cfg.Debug {
		e.logf("DEBUG", "job %s height=%d diff=%d mb=%d blocks=%d rej=%d",
			j.JobID, j.Height, j.Difficultyuint64, j.MiniBlocks, j.Blocks, j.Rejected)
	}
	_, err := e.state.SetJob(j)
	if err != nil {
		if errors.Is(err, miner.ErrBadVersion) {
			e.state.Invalidate()
			e.logf("ERROR", "rejected job push: %v", err)
		} else {
			e.logf("WARN", "ignored non-job frame: %v", err)
		}
		return false
	}
	if e.cfg.Debug && j.LastError != "" {
		e.logf("DEBUG", "daemon reports: %s", j.LastError)
	}
	return true
}

// Stop cancels the engine context and blocks until the client and every
// worker goroutine have exited. It is safe to call more than once.
func (e *Engine) Stop() {
	e.cancel()
	<-e.done
}

// Stats returns an atomic snapshot of engine activity.
func (e *Engine) Stats() Stats {
	now := time.Now()
	hashes := e.state.TotalHashes.Load()
	e.mu.Lock()
	rate := e.rate.sample(now, hashes)
	e.mu.Unlock()
	return Stats{
		Running:    true,
		Connected:  e.client.Connected.Load(),
		Hashrate:   rate,
		Hashes:     hashes,
		MiniBlocks: e.state.MiniBlocks.Load(),
		Blocks:     e.state.Blocks.Load(),
		Rejected:   e.state.Rejected.Load(),
		Height:     e.state.Height.Load(),
		Difficulty: e.state.Diff.Load(),
		Endpoint:   e.client.HostPort(),
		Wallet:     e.cfg.Wallet,
		Threads:    e.cfg.Threads,
	}
}

// sampleLoop records a hashrate sample once per second so Stats' sliding
// window stays warm even when Stats is polled sparsely.
func (e *Engine) sampleLoop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			e.mu.Lock()
			e.rate.sample(now, e.state.TotalHashes.Load())
			e.mu.Unlock()
		}
	}
}

// katHash is AstroBWTv3("a"); both backends must produce it. A var (not
// const) so tests can simulate a broken pipeline.
var katHash = "54e2324ddacc3f0383501a9e5760f85d63e9bc6705e9124ca7aef89016ab81ea"

// hashrateWindow is a ring of timestamped hash counters (main.go's window,
// simplified). sample records a point and returns H/s measured across the
// ring slot it overwrote, so the readout is a ~10s sliding average.
type hashrateWindow struct {
	points [10]hashratePoint
	next   int
	start  hashratePoint
}

type hashratePoint struct {
	at     time.Time
	hashes uint64
}

func newHashrateWindow(at time.Time, hashes uint64) *hashrateWindow {
	start := hashratePoint{at: at, hashes: hashes}
	w := &hashrateWindow{start: start}
	for i := range w.points {
		w.points[i] = start
	}
	return w
}

func (w *hashrateWindow) sample(at time.Time, hashes uint64) float64 {
	cur := hashratePoint{at: at, hashes: hashes}
	old := w.points[w.next]
	w.points[w.next] = cur
	w.next = (w.next + 1) % len(w.points)
	if cur.hashes < old.hashes || cur.at.Sub(old.at) <= 0 {
		return 0
	}
	return float64(cur.hashes-old.hashes) / cur.at.Sub(old.at).Seconds()
}
