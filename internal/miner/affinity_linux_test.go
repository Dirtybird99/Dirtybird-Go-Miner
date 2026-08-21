//go:build linux

package miner

import (
	"runtime"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPinOrderAppliesToCurrentThread(t *testing.T) {
	order := PinOrder(1)
	if len(order) != 1 {
		t.Skip("affinity unavailable in this environment")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var original unix.CPUSet
	if err := unix.SchedGetaffinity(0, &original); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := unix.SchedSetaffinity(0, &original); err != nil {
			t.Errorf("restore affinity: %v", err)
		}
	}()

	pinCurrentThread(0, order)
	var got unix.CPUSet
	if err := unix.SchedGetaffinity(0, &got); err != nil {
		t.Fatal(err)
	}
	if got.Count() != 1 || !got.IsSet(order[0]) {
		t.Fatalf("affinity = %v CPUs including target=%v, want only CPU %d", got.Count(), got.IsSet(order[0]), order[0])
	}
}
