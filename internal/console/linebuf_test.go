package console

import (
	"strings"
	"testing"
)

func TestLineBufColumns(t *testing.T) {
	var lb LineBuf
	lb.Esc("\x1b[93m")
	lb.Txt("[DB] ")
	lb.Esc("\x1b[92m")
	lb.Txt("5.04 KH/s")
	lb.Esc("\x1b[0m")
	if got, want := lb.Cols(), len("[DB] 5.04 KH/s"); got != want {
		t.Fatalf("Cols() = %d, want %d", got, want)
	}
	if got := lb.String(); got != "\x1b[93m[DB] \x1b[92m5.04 KH/s\x1b[0m" {
		t.Fatalf("String() = %q", got)
	}
	if len(lb.String()) <= lb.Cols() {
		t.Fatalf("escapes must cost bytes but not columns: len %d, cols %d", len(lb.String()), lb.Cols())
	}

	// Empty escape (Plain palette) is a no-op and keeps the buffer plain.
	var plain LineBuf
	plain.Esc("")
	plain.Txt("abc")
	if plain.hasEsc {
		t.Fatal("empty Esc must not mark the buffer as escaped")
	}
	if plain.String() != "abc" || plain.Cols() != 3 {
		t.Fatalf("plain buffer = %q cols %d", plain.String(), plain.Cols())
	}
}

func TestTruncateVisible(t *testing.T) {
	mk := func(s string) *LineBuf {
		var lb LineBuf
		lb.Txt(s)
		return &lb
	}

	over := mk("0123456789")
	over.TruncateVisible(4)
	if over.String() != "0123" || over.Cols() != 4 {
		t.Fatalf("over budget: %q cols %d", over.String(), over.Cols())
	}

	under := mk("01")
	under.TruncateVisible(4)
	if under.String() != "01" || under.Cols() != 2 {
		t.Fatalf("under budget must be untouched: %q", under.String())
	}

	exact := mk("0123")
	exact.TruncateVisible(4)
	if exact.String() != "0123" {
		t.Fatalf("exact budget must be untouched: %q", exact.String())
	}

	zero := mk("0123")
	zero.TruncateVisible(0)
	if zero.String() != "" || zero.Cols() != 0 {
		t.Fatalf("budget 0: %q cols %d", zero.String(), zero.Cols())
	}

	neg := mk("0123")
	neg.TruncateVisible(-3)
	if neg.String() != "" || neg.Cols() != 0 {
		t.Fatalf("negative budget clamps to 0: %q cols %d", neg.String(), neg.Cols())
	}

	// A buffer holding escapes is left unchanged (documented contract:
	// truncation is for plain renders only).
	var esc LineBuf
	esc.Esc("\x1b[93m")
	esc.Txt("0123456789")
	before := esc.String()
	esc.TruncateVisible(4)
	if esc.String() != before {
		t.Fatalf("escaped buffer must not be truncated: %q", esc.String())
	}
	if !strings.Contains(esc.String(), "\x1b") {
		t.Fatal("test setup: expected an escape in the buffer")
	}
}
