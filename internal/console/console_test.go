package console

import (
	"io"
	"os"
	"testing"
)

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
