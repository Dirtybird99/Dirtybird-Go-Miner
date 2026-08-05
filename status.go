package main

import (
	"fmt"

	"go-miner/internal/console"
	"go-miner/internal/getwork"
	"go-miner/internal/miner"
)

// The five status-line tiers, widest first. Selection is render-then-measure
// (selectStatusLine): there are deliberately no width thresholds — tier
// widths are data-dependent (a tall height changes which tier fits), and the
// C/Rust ports both learned that hardcoded cutoffs reintroduce line-wrap.
const (
	tierFull = iota + 1
	tierMedium
	tierNarrow
	tierCompact
	tierMinimal
)

// statusFields is a single-tick snapshot of everything the status line can
// show. Every atomic is read exactly once, so a job push mid-selection cannot
// mix fields from different jobs across tiers.
type statusFields struct {
	Rate, Avg  float64
	Height     uint64
	MiniBlocks uint64
	Blocks     uint64
	Rejected   uint64
	Diff       string // pre-formatted via fmtDiff
	Up         int    // whole seconds
	Verbose    bool
	Submitted  uint64
	Stale      uint64
	Discarded  uint64
	SendFails  uint64
}

func snapshotStatus(st *miner.State, client *getwork.Client, rate, avg float64, up int, verbose bool) statusFields {
	return statusFields{
		Rate:       rate,
		Avg:        avg,
		Height:     st.Height.Load(),
		MiniBlocks: st.MiniBlocks.Load(),
		Blocks:     st.Blocks.Load(),
		Rejected:   st.Rejected.Load(),
		Diff:       fmtDiff(st.Diff.Load()),
		Up:         up,
		Verbose:    verbose,
		Submitted:  st.Submitted.Load(),
		Stale:      st.Stale.Load(),
		Discarded:  client.Discarded.Load(),
		SendFails:  client.SendFails.Load(),
	}
}

// renderTier builds one tier of the status ladder. Escape placement is
// load-bearing: the tag runs straight into the rate color (no Text escape
// between), separators sit in the Text color left by the preceding field,
// and tiers 2-5 close the last field directly with Reset (the C/Rust byte
// convention). FULL alone keeps the legacy Go quirk of Text+Reset at the end
// — that exact byte shape predates the ladder and HiveOS captures it. With
// the Plain palette every Esc is a no-op and the same code emits an
// escape-free line.
func renderTier(tier int, p console.Palette, f statusFields) *console.LineBuf {
	lb := &console.LineBuf{}
	hh, mm, ss := f.Up/3600, f.Up/60%60, f.Up%60
	uptime := fmt.Sprintf("%02d:%02d:%02d", hh, mm, ss)

	// field prints one colored field followed by a separator in Text color.
	field := func(color, text, sep string) {
		lb.Esc(color)
		lb.Txt(text)
		lb.Esc(p.Text)
		lb.Txt(sep)
	}
	// last prints the final field and closes the line.
	last := func(color, text string) {
		lb.Esc(color)
		lb.Txt(text)
		lb.Esc(p.Reset)
	}

	lb.Esc(p.Label)
	switch tier {
	case tierFull:
		lb.Txt("[DIRTYBIRD] ")
		field(p.Rate, fmt.Sprintf("%.2f KH/s", f.Rate), " (")
		field(p.Avg, fmt.Sprintf("%.2f KH/s avg", f.Avg), ") | ")
		field(p.Height, fmt.Sprintf("Height:%d", f.Height), " | ")
		field(p.Mini, fmt.Sprintf("Miniblocks:%d", f.MiniBlocks), " | ")
		field(p.Block, fmt.Sprintf("Blocks:%d", f.Blocks), " | ")
		field(p.Rej(f.Rejected), fmt.Sprintf("REJ:%d", f.Rejected), " | ")
		field(p.Diff, "Diff:"+f.Diff, " | ")
		field(p.Time, uptime, "") // legacy trailing Text escape
		lb.Esc(p.Reset)
		if f.Verbose {
			lb.Txt(fmt.Sprintf(" | funnel submitted:%d acc:%d rej:%d stale:%d discarded:%d sendfail:%d",
				f.Submitted, f.MiniBlocks, f.Rejected, f.Stale, f.Discarded, f.SendFails))
		}

	case tierMedium: // abbreviated labels, every field kept
		lb.Txt("[DIRTYBIRD] ")
		field(p.Rate, fmt.Sprintf("%.2f KH/s", f.Rate), " (")
		field(p.Avg, fmt.Sprintf("%.2f avg", f.Avg), ") | ")
		field(p.Height, fmt.Sprintf("H:%d", f.Height), " | ")
		field(p.Mini, fmt.Sprintf("MB:%d", f.MiniBlocks), " | ")
		field(p.Block, fmt.Sprintf("B:%d", f.Blocks), " | ")
		field(p.Rej(f.Rejected), fmt.Sprintf("R:%d", f.Rejected), " | ")
		field(p.Diff, "D:"+f.Diff, " | ")
		last(p.Time, uptime)

	case tierNarrow: // drops the average
		lb.Txt("[DIRTYBIRD] ")
		field(p.Rate, fmt.Sprintf("%.2f KH/s", f.Rate), " | ")
		field(p.Height, fmt.Sprintf("H:%d", f.Height), " | ")
		field(p.Mini, fmt.Sprintf("MB:%d", f.MiniBlocks), " | ")
		field(p.Block, fmt.Sprintf("B:%d", f.Blocks), " | ")
		field(p.Rej(f.Rejected), fmt.Sprintf("R:%d", f.Rejected), " | ")
		field(p.Diff, "D:"+f.Diff, " | ")
		last(p.Time, uptime)

	case tierCompact: // phone-sized: short tag, no difficulty, spaces between counters
		lb.Txt("[DB] ")
		field(p.Rate, fmt.Sprintf("%.2f KH/s", f.Rate), " | ")
		field(p.Height, fmt.Sprintf("H:%d", f.Height), " ")
		field(p.Mini, fmt.Sprintf("MB:%d", f.MiniBlocks), " ")
		field(p.Block, fmt.Sprintf("B:%d", f.Blocks), " ")
		field(p.Rej(f.Rejected), fmt.Sprintf("R:%d", f.Rejected), " | ")
		last(p.Time, uptime)

	default: // tierMinimal: rate and the two counters that pay the bills
		lb.Txt("[DB] ")
		field(p.Rate, fmt.Sprintf("%.2f KH/s", f.Rate), " ")
		field(p.Mini, fmt.Sprintf("MB:%d", f.MiniBlocks), " ")
		last(p.Block, fmt.Sprintf("B:%d", f.Blocks))
	}
	return lb
}

// selectStatusLine picks the widest tier that fits the budget. When even
// MINIMAL is too wide it re-renders MINIMAL escape-free and truncates —
// cutting a colored line could land mid-escape.
func selectStatusLine(f statusFields, pal console.Palette, budget int) string {
	for tier := tierFull; tier <= tierMinimal; tier++ {
		lb := renderTier(tier, pal, f)
		if lb.Cols() <= budget {
			return lb.String()
		}
	}
	lb := renderTier(tierMinimal, console.Plain(), f)
	lb.TruncateVisible(budget)
	return lb.String()
}

// elideWallet shortens a wallet for the banner: head 8 + "..." + tail 4.
// Short or non-ASCII inputs pass through unchanged (the raw argument lands
// here when address parsing fails, and a byte slice through a multi-byte
// character would corrupt it).
func elideWallet(w string) string {
	if len(w) <= 15 {
		return w
	}
	for i := 0; i < len(w); i++ {
		if w[i] >= 0x80 {
			return w
		}
	}
	return w[:8] + "..." + w[len(w)-4:]
}
