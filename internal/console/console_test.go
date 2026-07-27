package console

import (
	"io"
	"os"
	"testing"
	"time"
)

func TestFormatRecord(t *testing.T) {
	now := time.Date(2026, 7, 26, 18, 7, 22, 967_000_000, time.Local)
	if got, want := formatRecord(now, "INFO", "Dirtybird Go Miner"),
		"26/07 18:07:22.967  INFO  Dirtybird Go Miner\n"; got != want {
		t.Fatalf("INFO record = %q, want %q", got, want)
	}
	// %-5s: ERROR fills the level field, so only the single literal space
	// separates it from the message (INFO gets two).
	if got, want := formatRecord(now, "ERROR", "connection lost"),
		"26/07 18:07:22.967  ERROR connection lost\n"; got != want {
		t.Fatalf("ERROR record = %q, want %q", got, want)
	}
	if got, want := formatRecord(now, "WARN", "x"),
		"26/07 18:07:22.967  WARN  x\n"; got != want {
		t.Fatalf("WARN record = %q, want %q", got, want)
	}
}

func TestParseColsOverride(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"80", 80, true},
		{"1", 1, true},
		{"9999", 9999, true},
		{"0", 0, false},
		{"10000", 0, false},
		{"-5", 0, false},
		{"abc", 0, false},
		{"", 0, false},
		{"80x", 0, false},
	}
	for _, c := range cases {
		got, ok := parseColsOverride(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parseColsOverride(%q) = (%d, %v), want (%d, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// Redirected output gets one plain record per tick even without
// GOMINER_FORCE_STATUS (family parity with the C and Rust miners).
func TestRedirectedStatusIsPlainNewlineRecord(t *testing.T) {
	oldStderr := os.Stderr
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = write
	t.Cleanup(func() {
		os.Stderr = oldStderr
		read.Close()
		write.Close()
	})

	New().Status("\r\x1b[32mterminal\x1b[0m", "plain")
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "plain\n" {
		t.Fatalf("redirected status = %q, want %q", got, "plain\n")
	}
}

func TestForcedStatusIsPlainNewlineRecord(t *testing.T) {
	oldStderr := os.Stderr
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = write
	t.Cleanup(func() {
		os.Stderr = oldStderr
		read.Close()
		write.Close()
	})
	t.Setenv("GOMINER_FORCE_STATUS", "1")

	New().Status("\r\x1b[32mterminal\x1b[0m", "plain")
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "plain\n" {
		t.Fatalf("forced status = %q, want %q", got, "plain\n")
	}
}
