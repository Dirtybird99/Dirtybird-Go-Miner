package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"math/bits"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"go-miner/internal/astrobwt"
	"go-miner/internal/console"
	"go-miner/internal/getwork"
	"go-miner/internal/miner"
)

const benchmarkCounterBatch = 64

func splitmix64(state *uint64) uint64 {
	*state += 0x9e3779b97f4a7c15
	z := *state
	z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
	z = (z ^ z>>27) * 0x94d049bb133111eb
	return z ^ z>>31
}

// benchmarkWork matches Zig DefaultPrng.init(12345 + tid): splitmix64 seeds
// xoshiro256++, whose u64 output is copied little-endian into the 48-byte job.
func benchmarkWork(tid int) [48]byte {
	seed := uint64(12345 + tid)
	state := [4]uint64{
		splitmix64(&seed), splitmix64(&seed), splitmix64(&seed), splitmix64(&seed),
	}
	var work [48]byte
	for offset := 0; offset < len(work); offset += 8 {
		next := bits.RotateLeft64(state[0]+state[3], 23) + state[0]
		t := state[1] << 17
		state[2] ^= state[0]
		state[3] ^= state[1]
		state[1] ^= state[2]
		state[0] ^= state[3]
		state[2] ^= t
		state[3] = bits.RotateLeft64(state[3], 45)
		binary.LittleEndian.PutUint64(work[offset:offset+8], next)
	}
	work[47] = byte(tid)
	return work
}

type hashRun struct {
	total atomic.Uint64
	stop  atomic.Bool
	wg    sync.WaitGroup
	start time.Time
}

func startHashRun(threads int, pinOrder []int, backend astrobwt.Backend, pair bool) *hashRun {
	r := &hashRun{start: time.Now()}
	for tid := 0; tid < threads; tid++ {
		r.wg.Add(1)
		go func(tid int) {
			defer r.wg.Done()
			runtime.LockOSThread()
			if pinOrder != nil {
				miner.PinThreadForBench(tid, pinOrder)
			}
			h := astrobwt.NewWithBackend(backend)
			workA := benchmarkWork(tid)
			workB := workA
			var nonce uint32
			for {
				if pair {
					for range benchmarkCounterBatch / 2 {
						binary.BigEndian.PutUint32(workA[43:47], nonce)
						binary.BigEndian.PutUint32(workB[43:47], nonce+1)
						_, _ = h.HashPair(workA[:], workB[:])
						nonce += 2
					}
				} else {
					for range benchmarkCounterBatch {
						binary.BigEndian.PutUint32(workA[43:47], nonce)
						_ = h.Hash(workA[:])
						nonce++
					}
				}
				r.total.Add(benchmarkCounterBatch)
				if r.stop.Load() {
					return
				}
			}
		}(tid)
	}
	return r
}

func (r *hashRun) finish() uint64 {
	r.stop.Store(true)
	r.wg.Wait()
	return r.total.Load()
}

// hashFor runs `threads` hashing goroutines for `dur` and returns completed
// hashes plus actual elapsed time, including thread startup and join tail.
func hashFor(threads int, dur time.Duration, pinOrder []int, backend astrobwt.Backend, pair bool) (uint64, time.Duration) {
	r := startHashRun(threads, pinOrder, backend, pair)
	if wait := time.Until(r.start.Add(dur)); wait > 0 {
		time.Sleep(wait)
	}
	n := r.finish()
	return n, time.Since(r.start)
}

func pipelineName(pair bool) string {
	if pair && astrobwt.PairHashSupported() {
		return "x2"
	}
	return "x1"
}

// runBench sweeps thread counts and prints a derohe-style table.
func runBench(maxThreads int, pin bool, backend astrobwt.Backend, pair bool) int {
	var pinOrder []int
	if pin {
		pinOrder = miner.PinOrder()
	}
	fmt.Printf("go-miner %s bench, %d logical CPUs, pin=%v, sa=%s, pipeline=%s\n",
		version, runtime.NumCPU(), pin, backendName(backend), pipelineName(pair))
	fmt.Printf("%8s %12s %14s %14s\n", "Threads", "Total H/s", "Per-thread", "Time/PoW")

	counts := []int{1, 2, 4, 8, 12, 16, 20, 23, 24}
	seen := map[int]bool{}
	for _, tc := range append(counts, maxThreads) {
		if tc > maxThreads || seen[tc] {
			continue
		}
		seen[tc] = true
		_, _ = hashFor(tc, time.Second, pinOrder, backend, pair) // warmup
		const window = 5 * time.Second
		n, elapsed := hashFor(tc, window, pinOrder, backend, pair)
		hs := float64(n) / elapsed.Seconds()
		fmt.Printf("%8d %12.1f %14.1f %14s\n", tc, hs, hs/float64(tc),
			time.Duration(float64(elapsed)/float64(n)*float64(tc)).Round(time.Microsecond))
	}
	printInstrumentationStats()
	return 0
}

func printInstrumentationStats() {
	printStageStats()
	astrobwt.PrintV114Stats(os.Stdout)
}

// printStageStats prints the per-stage cycle table when the binary was built
// with -tags stagestats. Counters are cumulative over every hash this process
// computed (warmups included), which is fine for share percentages.
func printStageStats() {
	if !astrobwt.StageStatsEnabled {
		return
	}
	pro, wolf, sa, sha, n := astrobwt.StageSnapshot()
	total := pro + wolf + sa + sha
	if n == 0 || total == 0 {
		return
	}
	fmt.Printf("\nper-stage cycles/hash over %d hashes (rdtsc):\n", n)
	for _, s := range []struct {
		name string
		cyc  uint64
	}{{"prologue", pro}, {"wolf", wolf}, {"sa", sa}, {"sha", sha}, {"total", total}} {
		fmt.Printf("%10s %12.0f cyc/hash %7.2f%%\n",
			s.name, float64(s.cyc)/float64(n), 100*float64(s.cyc)/float64(total))
	}
}

// validSecs rejects nonpositive windows and values whose nanosecond
// conversion overflows int64 — an overflowed window ends in milliseconds and
// reports a plausible-looking rate for a run that never warmed up.
func validSecs(secs int) bool {
	return secs > 0 && int64(secs) <= math.MaxInt64/int64(time.Second)
}

// runSustained runs all threads for a fixed wall window — the honest
// hybrid-CPU number.
func runSustained(threads, secs int, pin bool, backend astrobwt.Backend, pair bool) int {
	if !validSecs(secs) {
		fmt.Fprintln(os.Stderr, "--secs must be a positive number of seconds")
		return 1
	}
	var pinOrder []int
	if pin {
		pinOrder = miner.PinOrder()
	}
	fmt.Printf("go-miner %s sustained bench: %d threads, %ds, pin=%v, sa=%s, pipeline=%s\n",
		version, threads, secs, pin, backendName(backend), pipelineName(pair))
	window := time.Duration(secs) * time.Second
	r := startHashRun(threads, pinOrder, backend, pair)
	start := r.start
	lastTime := start
	var lastCount uint64
	var checkpoints []time.Duration
	for _, checkpoint := range []time.Duration{10 * time.Second, 30 * time.Second, 60 * time.Second, 90 * time.Second, 120 * time.Second} {
		if checkpoint <= window {
			checkpoints = append(checkpoints, checkpoint)
		}
	}
	for checkpoint := 150 * time.Second; checkpoint < window; checkpoint += 30 * time.Second {
		checkpoints = append(checkpoints, checkpoint)
	}
	if len(checkpoints) == 0 || checkpoints[len(checkpoints)-1] != window {
		checkpoints = append(checkpoints, window)
	}
	for i, checkpoint := range checkpoints {
		if checkpoint > window {
			continue
		}
		if wait := time.Until(start.Add(checkpoint)); wait > 0 {
			time.Sleep(wait)
		}
		now := time.Now()
		count := r.total.Load()
		if checkpoint == window {
			count = r.finish()
			now = time.Now()
		}
		rate := float64(count-lastCount) / now.Sub(lastTime).Seconds()
		label := fmt.Sprintf("%ds", int(checkpoint/time.Second))
		if i == 0 {
			// the first interval starts before worker spawn/pinning, so it
			// measures ramp, not peak
			label = "ramp"
		} else if checkpoint >= 120*time.Second {
			label = "120+"
		}
		fmt.Printf("%-5s t=%-4s interval=%8.2f KH/s total=%d\n", label, checkpoint, rate/1000, count)
		lastCount = count
		lastTime = now
	}
	n := r.total.Load()
	elapsed := lastTime.Sub(start)
	hs := float64(n) / elapsed.Seconds()
	fmt.Printf("%d hashes in %v = %.2f KH/s (%.1f H/s/thread)\n", n, elapsed.Round(time.Millisecond), hs/1000, hs/float64(threads))
	printInstrumentationStats()
	return 0
}

// runStatBench drives the real mining pipeline — miner.Run workers plus the
// 1 Hz statusLoop — on a synthetic never-winning job for a fixed window, so
// the displayed rate can be captured offline (redirected stderr gets one
// plain status record per tick) and its stability measured. No daemon
// involved; nothing is ever submitted.
func runStatBench(cons *console.Console, threads, secs int, o *options) int {
	if !validSecs(secs) {
		fmt.Fprintln(os.Stderr, "--secs must be a positive number of seconds")
		return 1
	}
	st := &miner.State{}
	blob := make([]byte, miner.MiniblockSize)
	blob[0] = 0x01 // version nibble the job gate expects
	if _, err := st.SetJob(getwork.Job{
		JobID:             "statbench",
		Blockhashing_blob: hex.EncodeToString(blob),
		Difficultyuint64:  1 << 62, // target no hash will ever meet
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var pinOrder []int
	if o.pin {
		pinOrder = miner.PinOrder()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(secs)*time.Second)
	defer cancel()
	submits := make(chan getwork.Submit, 16)
	for t := 0; t < threads; t++ {
		go miner.Run(ctx, t, st, submits, pinOrder, o.backend(), o.pair)
	}
	start := time.Now()
	statusLoop(ctx, cons, st, &getwork.Client{}, o)
	elapsed := time.Since(start)
	time.Sleep(200 * time.Millisecond) // let workers flush their local counters
	n := st.TotalHashes.Load()
	fmt.Fprintf(os.Stderr, "\nstatbench: %d hashes in %.2fs = %.3f KH/s true\n",
		n, elapsed.Seconds(), float64(n)/elapsed.Seconds()/1000)
	return 0
}

func backendName(b astrobwt.Backend) string {
	if b == astrobwt.BackendV114 {
		return "v114"
	}
	return "sais"
}
