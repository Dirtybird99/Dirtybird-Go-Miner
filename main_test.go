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
	if got := formatStatusLine(fields, false, 0); got != want {
		t.Fatalf("plain status:\n got %q\nwant %q", got, want)
	}
	colored := formatStatusLine(fields, true, 0)
	if !strings.Contains(colored, "\x1b[") {
		t.Fatalf("terminal status has no ANSI color: %q", colored)
	}
	if strings.Contains(formatStatusLine(fields, false, 0), "\x1b") {
		t.Fatal("plain status contains ANSI")
	}
}

func stripStatusANSI(s string) string {
	for _, code := range []string{
		aReset, aBYellow, aBGreen, aBWhite, aGreen,
		aBlue, aCyan, aMagenta, aWhite, aBRed,
	} {
		s = strings.ReplaceAll(s, code, "")
	}
	return s
}

func TestFormatStatusLineWidths(t *testing.T) {
	fields := statusFields{
		rate:       23.76,
		average:    20.71,
		height:     7_212_998,
		miniblocks: 1_998,
		blocks:     262,
		rejected:   4,
		diff:       "312M",
		uptime:     time.Hour,
		verbose:    true,
		submitted:  2_000,
		stale:      3,
		sendFails:  2,
	}
	tests := []struct {
		name     string
		width    int
		contains string
		excludes string
	}{
		{"wide", 200, "KH/s avg", ""},
		{"80 columns", 80, "[DIRTYBIRD]", "KH/s avg"},
		{"56 columns", 56, " M:1998", "KH/s avg"},
		{"40 columns", 40, " H:7212998", "[DIRTYBIRD]"},
		{"unknown fallback", 40, " H:7212998", "[DIRTYBIRD]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatStatusLine(fields, true, tt.width)
			plain := stripStatusANSI(got)
			if tt.width > 1 && len(plain) >= tt.width {
				t.Fatalf("visible width %d reaches terminal width %d: %q", len(plain), tt.width, got)
			}
			if !strings.Contains(plain, tt.contains) {
				t.Fatalf("%q does not contain %q", plain, tt.contains)
			}
			if tt.excludes != "" && strings.Contains(plain, tt.excludes) {
				t.Fatalf("%q unexpectedly contains %q", plain, tt.excludes)
			}
			if strings.Contains(plain, "funnel") && tt.width < 200 {
				t.Fatalf("compact status retained verbose counters: %q", plain)
			}
		})
	}

	got := formatStatusLine(fields, true, 8)
	if got != "23.76 K" || strings.Contains(got, "\x1b") {
		t.Fatalf("narrow status = %q, want safe uncolored clip", got)
	}
	if got := formatStatusLine(fields, false, 0); !strings.Contains(got, "funnel submitted:2000") {
		t.Fatalf("redirected status is not complete: %q", got)
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
