//go:build !windows && !linux

package miner

import "runtime"

// PinOrder returns the avoidHT interleave; per-thread pinning is a no-op on
// platforms without an affinity implementation.
func PinOrder(threads int) []int {
	n := runtime.NumCPU()
	order := make([]int, 0, n)
	for i := 0; i < n; i += 2 {
		order = append(order, i)
	}
	for i := 1; i < n; i += 2 {
		order = append(order, i)
	}
	return pinOrderForThreads(order, threads)
}

func pinCurrentThread(tid int, order []int) {}

// PinThreadForBench is a no-op on unsupported platforms, like pinCurrentThread.
func PinThreadForBench(tid int, order []int) {}

func SetHighPriority() error { return nil }
