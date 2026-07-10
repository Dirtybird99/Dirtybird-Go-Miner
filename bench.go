package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
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

// hashFor runs `threads` hashing goroutines for `dur` and returns total hashes.
func hashFor(threads int, dur time.Duration, pinOrder []int, backend astrobwt.Backend, pair bool) uint64 {
	var total atomic.Uint64
	var stop atomic.Bool
	var wg sync.WaitGroup
	for t := 0; t < threads; t++ {
		wg.Add(1)
		go func(tid int) {
			defer wg.Done()
			runtime.LockOSThread()
			if pinOrder != nil {
				miner.PinThreadForBench(tid, pinOrder)
			}
			h := astrobwt.NewWithBackend(backend)
			var workA, workB [48]byte
			rand.Read(workA[:])
			workB = workA
			workA[47] = byte(tid)
			workB[47] = byte(tid)
			workB[42]++ // distinct nonce lane
			var local uint64
			for !stop.Load() {
				workA[0] = byte(local) // vary input
				workA[1] = byte(local >> 8)
				if pair {
					workB[0] = byte(local)
					workB[1] = byte(local >> 8)
					_, _ = h.HashPair(workA[:], workB[:])
					local += 2
				} else {
					_ = h.Hash(workA[:])
					local++
				}
				if local%16 == 0 {
					total.Add(16)
				}
			}
			total.Add(local % 16)
		}(t)
	}
	time.Sleep(dur)
	stop.Store(true)
	wg.Wait()
	return total.Load()
}

// runBench sweeps thread counts and prints a derohe-style table.
func runBench(maxThreads int, pin bool, backend astrobwt.Backend, pair bool) int {
	var pinOrder []int
	if pin {
		pinOrder = miner.PinOrder()
	}
	fmt.Printf("go-miner %s bench, %d logical CPUs, pin=%v, sa=%s, pair=%v\n",
		version, runtime.NumCPU(), pin, backendName(backend), pair)
	fmt.Printf("%8s %12s %14s %14s\n", "Threads", "Total H/s", "Per-thread", "Time/PoW")

	counts := []int{1, 2, 4, 8, 12, 16, 20, 23, 24}
	seen := map[int]bool{}
	for _, tc := range append(counts, maxThreads) {
		if tc > maxThreads || seen[tc] {
			continue
		}
		seen[tc] = true
		hashFor(tc, time.Second, pinOrder, backend, pair) // warmup
		const window = 5 * time.Second
		n := hashFor(tc, window, pinOrder, backend, pair)
		hs := float64(n) / window.Seconds()
		fmt.Printf("%8d %12.1f %14.1f %14s\n", tc, hs, hs/float64(tc),
			time.Duration(float64(window)/float64(n)*float64(tc)).Round(time.Microsecond))
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

// runSustained runs all threads for a fixed wall window — the honest
// hybrid-CPU number.
func runSustained(threads, secs int, pin bool, backend astrobwt.Backend, pair bool) int {
	var pinOrder []int
	if pin {
		pinOrder = miner.PinOrder()
	}
	fmt.Printf("go-miner %s sustained bench: %d threads, %ds, pin=%v, sa=%s, pair=%v\n",
		version, threads, secs, pin, backendName(backend), pair)
	hashFor(threads, 2*time.Second, pinOrder, backend, pair) // warmup
	window := time.Duration(secs) * time.Second
	n := hashFor(threads, window, pinOrder, backend, pair)
	hs := float64(n) / window.Seconds()
	fmt.Printf("%d hashes in %v = %.2f KH/s (%.1f H/s/thread)\n", n, window, hs/1000, hs/float64(threads))
	printInstrumentationStats()
	return 0
}

// runStatBench drives the real mining pipeline — miner.Run workers plus the
// 1 Hz statusLoop — on a synthetic never-winning job for a fixed window, so
// the displayed rate can be captured offline (GOMINER_FORCE_STATUS=1) and its
// stability measured. No daemon involved; nothing is ever submitted.
func runStatBench(cons *console.Console, threads, secs int, o *options) int {
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
