package console

import "testing"

func TestResolvePalette(t *testing.T) {
	if got := ResolvePalette(false, false); got != Plain() {
		t.Fatal("no VT must resolve Plain")
	}
	if got := ResolvePalette(false, true); got != Plain() {
		t.Fatal("no VT + NO_COLOR must resolve Plain")
	}
	if got := ResolvePalette(true, true); got != Plain() {
		t.Fatal("NO_COLOR must win over VT")
	}
	got := ResolvePalette(true, false)
	if got != Colour() {
		t.Fatal("VT without NO_COLOR must resolve Colour")
	}
	// Pin the exact family escape codes.
	want := Palette{
		Label:  "\x1b[93m",
		Rate:   "\x1b[92m",
		Text:   "\x1b[97m",
		Avg:    "\x1b[32m",
		Height: "\x1b[34m",
		Mini:   "\x1b[36m",
		Block:  "\x1b[32m",
		Diff:   "\x1b[35m",
		Time:   "\x1b[37m",
		RejOK:  "\x1b[37m",
		RejHi:  "\x1b[91m",
		Reset:  "\x1b[0m",
	}
	if got != want {
		t.Fatalf("Colour() = %+v, want %+v", got, want)
	}
}

func TestPaletteRej(t *testing.T) {
	c := Colour()
	if got := c.Rej(0); got != "\x1b[37m" {
		t.Fatalf("Rej(0) = %q, want white", got)
	}
	if got := c.Rej(1); got != "\x1b[91m" {
		t.Fatalf("Rej(1) = %q, want bright red", got)
	}
	if got := Plain().Rej(1); got != "" {
		t.Fatalf("Plain().Rej(1) = %q, want empty", got)
	}
}
