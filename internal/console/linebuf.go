package console

// LineBuf accumulates a status line while counting visible columns separately
// from bytes: SGR escapes cost bytes but zero columns, and tier selection must
// compare columns against the terminal width (comparing byte length would
// reject every colored layout on every terminal and pin output to the
// narrowest tier).
type LineBuf struct {
	buf    []byte
	cols   int
	hasEsc bool
}

// Esc appends a zero-width escape sequence. An empty sequence (the Plain
// palette) is a no-op, so plain renders stay truncatable.
func (l *LineBuf) Esc(seq string) {
	if seq == "" {
		return
	}
	l.buf = append(l.buf, seq...)
	l.hasEsc = true
}

// Txt appends printable text. Every tier layout is pure ASCII, so bytes ==
// columns by contract.
func (l *LineBuf) Txt(s string) {
	l.buf = append(l.buf, s...)
	l.cols += len(s)
}

// Cols is the visible column count (escapes excluded).
func (l *LineBuf) Cols() int { return l.cols }

func (l *LineBuf) String() string { return string(l.buf) }

// TruncateVisible cuts the line to at most budget columns. Only valid on
// escape-free renders — cutting a colored line can land mid-escape and wedge
// the terminal's color state — so a buffer holding escapes is left unchanged.
func (l *LineBuf) TruncateVisible(budget int) {
	if budget < 0 {
		budget = 0
	}
	if l.hasEsc || l.cols <= budget {
		return
	}
	l.buf = l.buf[:budget]
	l.cols = budget
}
