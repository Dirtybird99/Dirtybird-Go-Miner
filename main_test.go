package main

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestValidSAName(t *testing.T) {
	for _, name := range []string{"v114", "sais"} {
		if !validSAName(name) {
			t.Fatalf("%q should be valid", name)
		}
	}
	for _, name := range []string{"", "SAIS", "libsais", "nope"} {
		if validSAName(name) {
			t.Fatalf("%q should be invalid", name)
		}
	}
}

func TestHashrateWindow(t *testing.T) {
	start := time.Unix(0, 0)
	window := newHashrateWindow(start, 10_000)

	if got := window.sample(start.Add(2500*time.Millisecond), 12_500); math.Abs(got-1) > 1e-9 {
		t.Fatalf("irregular startup sample = %v KH/s, want 1", got)
	}
	if got := window.average(start.Add(2500*time.Millisecond), 12_500); math.Abs(got-1) > 1e-9 {
		t.Fatalf("session average = %v KH/s, want 1", got)
	}

	window = newHashrateWindow(start, 0)
	for second := 1; second <= 5; second++ {
		got := window.sample(start.Add(time.Duration(second)*time.Second), uint64(second*1000))
		if math.Abs(got-1) > 1e-9 {
			t.Fatalf("steady sample %d = %v KH/s, want 1", second, got)
		}
	}
	for second := 6; second <= 15; second++ {
		got := window.sample(start.Add(time.Duration(second)*time.Second), 5_000)
		switch second {
		case 10:
			if math.Abs(got-0.5) > 1e-9 {
				t.Fatalf("stalled sample 10 = %v KH/s, want 0.5", got)
			}
		case 15:
			if got != 0 {
				t.Fatalf("stalled sample 15 = %v KH/s, want 0", got)
			}
		}
	}
	if got := window.sample(start.Add(16*time.Second), 4_999); got != 0 {
		t.Fatalf("decreasing counter = %v KH/s, want 0", got)
	}
}

func TestFormatStatusLine(t *testing.T) {
	fields := statusFields{
		rate:       23.76,
		average:    20.71,
		height:     7_212_998,
		miniblocks: 1_998,
		blocks:     262,
		rejected:   4,
		diff:       "312M",
		uptime:     time.Hour + 2*time.Minute + 3*time.Second,
	}
	const want = "[DIRTYBIRD] 23.76 KH/s (20.71 KH/s avg) | Height:7212998 | Miniblocks:1998 | Blocks:262 | REJ:4 | Diff:312M | 01:02:03"
	if got := formatStatusLine(fields, false); got != want {
		t.Fatalf("plain status:\n got %q\nwant %q", got, want)
	}
	colored := formatStatusLine(fields, true)
	if !strings.Contains(colored, "\x1b[") {
		t.Fatalf("terminal status has no ANSI color: %q", colored)
	}
	if strings.Contains(formatStatusLine(fields, false), "\x1b") {
		t.Fatal("plain status contains ANSI")
	}
}
