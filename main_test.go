package main

import (
	"bufio"
	"bytes"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-miner/internal/config"
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

func TestEndpointChoicesAndValidation(t *testing.T) {
	for i, endpoint := range setupEndpoints {
		sc := bufio.NewScanner(strings.NewReader(fmt.Sprintf("%d\n", i+1)))
		if got := promptEndpoint(sc, &bytes.Buffer{}, "keep.example:1"); got != endpoint.address {
			t.Fatalf("choice %d = %q, want %q", i+1, got, endpoint.address)
		}
	}
	sc := bufio.NewScanner(strings.NewReader("\n"))
	if got := promptEndpoint(sc, &bytes.Buffer{}, "keep.example:1234"); got != "keep.example:1234" {
		t.Fatalf("blank choice = %q, want current endpoint", got)
	}
	var out bytes.Buffer
	sc = bufio.NewScanner(strings.NewReader("9\n2\n"))
	if got := promptEndpoint(sc, &out, "keep.example:1234"); got != setupEndpoints[1].address ||
		!strings.Contains(out.String(), "Invalid choice") {
		t.Fatalf("invalid choice did not retry: endpoint %q, output %q", got, out.String())
	}

	valid := []string{
		"pool.example:1",
		"wss://pool.example:65535",
		"ws://127.0.0.1:10100",
		"wss://[::1]:10100",
	}
	invalid := []string{
		"",
		" pool.example:10300",
		"http://pool.example:10300",
		"pool.example:0",
		"pool.example:65536",
		"pool.example:notaport",
		"pool.example:10300/path",
		"pool.example:10300?wallet=evil",
		"wallet@pool.example:10300",
		"pool.example:10300\"}",
		"pool_example:10300",
	}
	for _, endpoint := range valid {
		if !validEndpoint(endpoint) {
			t.Errorf("validEndpoint(%q) = false", endpoint)
		}
	}
	for _, endpoint := range invalid {
		if validEndpoint(endpoint) {
			t.Errorf("validEndpoint(%q) = true", endpoint)
		}
	}
}

func TestRunSetupDefaultsAndCustomRetry(t *testing.T) {
	t.Run("new config defaults to community pool", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		if code := runSetupIO(&options{cfgPath: path}, strings.NewReader("\n\n\n"), &bytes.Buffer{}); code != 0 {
			t.Fatalf("runSetupIO returned %d", code)
		}
		got, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if got.DaemonAddress == nil || *got.DaemonAddress != defaultDaemon {
			t.Fatalf("daemon = %v, want %q", got.DaemonAddress, defaultDaemon)
		}
	})

	t.Run("existing endpoint survives Enter", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := config.Save(path, "current.example:10300", "wallet", 2); err != nil {
			t.Fatal(err)
		}
		if code := runSetupIO(&options{cfgPath: path}, strings.NewReader("\n\n\n"), &bytes.Buffer{}); code != 0 {
			t.Fatalf("runSetupIO returned %d", code)
		}
		got, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if got.DaemonAddress == nil || *got.DaemonAddress != "current.example:10300" {
			t.Fatalf("daemon = %v, want existing endpoint", got.DaemonAddress)
		}
	})

	t.Run("invalid custom input retries", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		var out bytes.Buffer
		input := "5\npool.example:10300/\"injection\nwss://custom.example:1234\nwallet\n4\n"
		if code := runSetupIO(&options{cfgPath: path}, strings.NewReader(input), &out); code != 0 {
			t.Fatalf("runSetupIO returned %d", code)
		}
		got, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if got.DaemonAddress == nil || *got.DaemonAddress != "wss://custom.example:1234" {
			t.Fatalf("daemon = %v, want validated custom endpoint", got.DaemonAddress)
		}
		if !strings.Contains(out.String(), "Invalid endpoint") {
			t.Fatalf("setup did not report retry: %q", out.String())
		}
	})
}

func TestStatusRowStaysSilentUntilThereIsSomethingToSay(t *testing.T) {
	// Every launch: not connected, no job. This is the tick that used to print
	// "[DIRTYBIRD] 0.00 KH/s (0.00 KH/s avg) | Height:0 | ..." on every run.
	if statusRowHasSomethingToSay(false, 0) {
		t.Error("printed a row before connecting")
	}
	// Connected, but getwork has not pushed a job yet — workers are still
	// parked, so the rate is genuinely zero.
	if statusRowHasSomethingToSay(true, 0) {
		t.Error("printed a row before the first job arrived")
	}
	// Mining.
	if !statusRowHasSomethingToSay(true, 1) {
		t.Error("suppressed the row while actually mining")
	}
	// Dropped mid-run: a job was seen earlier so the epoch stays high, but
	// workers are parked for the whole backoff. Having once had a job must not
	// keep the row alive.
	if statusRowHasSomethingToSay(false, 5) {
		t.Error("kept the row alive across a disconnect")
	}
}
