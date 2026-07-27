package console

// Palette is the per-field SGR set for the status line (the C miner's
// console.cpp palette, kept identical across every tier so the line still
// reads as the same miner when it shrinks). Plain is all-empty, so the same
// renderers emit an escape-free line for pipes.
type Palette struct {
	Label  string // [DIRTYBIRD] / [DB] tag
	Rate   string // instantaneous KH/s
	Text   string // separators
	Avg    string // average KH/s
	Height string
	Mini   string
	Block  string
	Diff   string
	Time   string // uptime
	RejOK  string // reject counter, none rejected
	RejHi  string // reject counter, some rejected
	Reset  string
}

// Colour is the family palette (bright yellow tag, bright green rate, ...).
func Colour() Palette {
	return Palette{
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
}

func Plain() Palette { return Palette{} }

// Rej picks the reject-counter color — the one field whose color carries
// information (red only when shares have been rejected).
func (p Palette) Rej(rejected uint64) string {
	if rejected > 0 {
		return p.RejHi
	}
	return p.RejOK
}

// ResolvePalette is pure so it tests without touching env or TTY state.
// Color needs a VT-capable stream and no NO_COLOR opt-out; any non-empty
// NO_COLOR value disables color (so NO_COLOR= does not).
func ResolvePalette(vt, noColor bool) Palette {
	if !vt || noColor {
		return Plain()
	}
	return Colour()
}
