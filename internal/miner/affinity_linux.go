//go:build linux

package miner

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"golang.org/x/sys/unix"
)

// PinOrder returns a complete order for threads, with one allowed logical CPU
// from every physical core first, then the remaining SMT siblings. It returns
// nil rather than partially pinning when there are too few allowed CPUs.
//
// x/sys/unix.CPUSet is fixed to CPU IDs below 1024. SchedGetaffinity fails on
// systems whose kernel affinity mask needs more space; those runs safely stay
// unpinned rather than using a hand-rolled dynamic mask.
func PinOrder(threads int) []int {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var set unix.CPUSet
	if err := unix.SchedGetaffinity(0, &set); err != nil {
		return nil
	}
	count := set.Count()
	allowed := make([]int, 0, count)
	for cpu := 0; len(allowed) < count; cpu++ {
		if set.IsSet(cpu) {
			allowed = append(allowed, cpu)
		}
	}

	siblingLists := make(map[int]string, len(allowed))
	for _, cpu := range allowed {
		path := filepath.Join("/sys/devices/system/cpu", "cpu"+strconv.Itoa(cpu), "topology", "thread_siblings_list")
		data, err := os.ReadFile(path)
		if err != nil {
			order := pinOrderForThreads(allowed, threads)
			if order == nil || !preflightPinOrder(order, set) {
				return nil
			}
			return order
		}
		siblingLists[cpu] = string(data)
	}
	order := pinOrderForThreads(linuxPinOrder(allowed, siblingLists), threads)
	if order == nil || !preflightPinOrder(order, set) {
		return nil
	}
	return order
}

func preflightPinOrder(order []int, original unix.CPUSet) bool {
	for _, cpu := range order {
		var target unix.CPUSet
		target.Set(cpu)
		if !target.IsSet(cpu) || unix.SchedSetaffinity(0, &target) != nil {
			_ = unix.SchedSetaffinity(0, &original)
			return false
		}
	}
	return unix.SchedSetaffinity(0, &original) == nil
}

func pinCurrentThread(tid int, order []int) {
	if tid < 0 || tid >= len(order) || order[tid] < 0 {
		return
	}
	var set unix.CPUSet
	set.Set(order[tid])
	if !set.IsSet(order[tid]) { // CPU ID outside x/sys's fixed-size CPUSet
		return
	}
	_ = unix.SchedSetaffinity(0, &set)
}

// PinThreadForBench pins the calling OS thread (which must be locked) for
// bench-mode workers, same as mining workers.
func PinThreadForBench(tid int, order []int) { pinCurrentThread(tid, order) }

func SetHighPriority() error { return nil }
