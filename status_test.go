package main

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"testing"

	"go-miner/internal/console"
	"go-miner/internal/getwork"
	"go-miner/internal/miner"
)

// phoneFields reproduces the reference miner's Termux screenshot: the case
// the compact tier exists for.
func phoneFields() statusFields {
	return statusFields{Rate: 5.04, Avg: 4.96, Height: 7386579, Diff: "312M", Up: 77}
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func TestStatusTierGolden(t *testing.T) {
	f := phoneFields()

	// Wide budget: FULL, plain.
	full := selectStatusLine(f, console.Plain(), 200)
	wantFull := "[DIRTYBIRD] 5.04 KH/s (4.96 KH/s avg) | Height:7386579 | Miniblocks:0 | Blocks:0 | REJ:0 | Diff:312M | 00:01:17"
	if full != wantFull {
		t.Fatalf("FULL plain =\n%q, want\n%q", full, wantFull)
	}

	// The phone case: budget 50 selects COMPACT at exactly 50 columns.
	compact := selectStatusLine(f, console.Plain(), 50)
	wantCompact := "[DB] 5.04 KH/s | H:7386579 MB:0 B:0 R:0 | 00:01:17"
	if compact != wantCompact {
		t.Fatalf("COMPACT plain =\n%q, want\n%q", compact, wantCompact)
	}
	if len(wantCompact) != 50 {
		t.Fatalf("fixture drift: compact line is %d cols, want 50", len(wantCompact))
	}

	// ... and its exact colored byte sequence (the Rust port's golden).
	colored := selectStatusLine(f, console.Colour(), 50)
	wantColored := "\x1b[93m[DB] \x1b[92m5.04 KH/s\x1b[97m | \x1b[34mH:7386579\x1b[97m \x1b[36mMB:0\x1b[97m \x1b[32mB:0\x1b[97m \x1b[37mR:0\x1b[97m | \x1b[37m00:01:17\x1b[0m"
	if colored != wantColored {
		t.Fatalf("COMPACT colored =\n%q, want\n%q", colored, wantColored)
	}

	// Small budget: MINIMAL.
	minimal := selectStatusLine(f, console.Plain(), 30)
	if minimal != "[DB] 5.04 KH/s MB:0 B:0" {
		t.Fatalf("MINIMAL plain = %q", minimal)
	}

	// Tiny budget: truncated plain MINIMAL, never a cut escape.
	tiny := selectStatusLine(f, console.Colour(), 10)
	if tiny != "[DB] 5.04 " {
		t.Fatalf("truncated = %q", tiny)
	}
	if strings.Contains(tiny, "\x1b") {
		t.Fatal("truncation fallback must be escape-free")
	}
}

// TestStatusWidthInvariant is the anti-wrap property: at every width, in both
// palettes, with worst-case field values, the selected line never exceeds the
// budget.
func TestStatusWidthInvariant(t *testing.T) {
	worst := statusFields{
		Rate:       999999.99,
		Avg:        999999.99,
		Height:     math.MaxUint64,
		MiniBlocks: math.MaxUint64,
		Blocks:     math.MaxUint64,
		Rejected:   math.MaxUint64,
		Diff:       fmtDiff(math.MaxUint64),
		Up:         359999, // 99:59:59
	}
	pals := map[string]console.Palette{"plain": console.Plain(), "colour": console.Colour()}
	for name, pal := range pals {
		for width := 1; width <= 200; width++ {
			budget := width - 1
			line := selectStatusLine(worst, pal, budget)
			if got := len(stripANSI(line)); got > budget {
				t.Fatalf("%s width %d: %d visible cols > budget %d: %q", name, width, got, budget, line)
			}
		}
	}
}

// TestPlainColorTierParity: color must never change which tier fits — it
// costs bytes, never columns.
func TestPlainColorTierParity(t *testing.T) {
	f := phoneFields()
	f.Rejected = 3 // exercise the RejHi arm too
	for _, budget := range []int{199, 110, 79, 55, 40, 25} {
		plain := selectStatusLine(f, console.Plain(), budget)
		colored := selectStatusLine(f, console.Colour(), budget)
		if stripANSI(colored) != plain {
			t.Fatalf("budget %d: tiers diverge\nplain   %q\ncolored %q", budget, plain, colored)
		}
		if strings.Contains(plain, "\x1b") {
			t.Fatalf("budget %d: plain line has escapes: %q", budget, plain)
		}
		// Below the truncation fallback both palettes emit escapes on the
		// colored path; when a tier fit, colored must cost extra bytes.
		if len(colored) > len(plain) != strings.Contains(colored, "\x1b") {
			t.Fatalf("budget %d: colored len %d vs plain %d without escapes", budget, len(colored), len(plain))
		}
	}
}

// TestRenderFullMatchesLegacy pins the FULL tier to the exact byte shape of
// the pre-ladder fmt.Sprintf (HiveOS captures scrape it): every escape in
// place, both reject-color branches, with and without the -V funnel suffix.
func TestRenderFullMatchesLegacy(t *testing.T) {
	const (
		aReset   = "\x1b[0m"
		aBYellow = "\x1b[93m"
		aBGreen  = "\x1b[92m"
		aBWhite  = "\x1b[97m"
		aGreen   = "\x1b[32m"
		aBlue    = "\x1b[34m"
		aCyan    = "\x1b[36m"
		aMagenta = "\x1b[35m"
		aWhite   = "\x1b[37m"
		aBRed    = "\x1b[91m"
	)
	legacy := func(f statusFields) string {
		rejcol := aWhite
		if f.Rejected > 0 {
			rejcol = aBRed
		}
		up := f.Up
		line := fmt.Sprintf(aBYellow+"[DIRTYBIRD] "+
			aBGreen+"%.2f KH/s"+aBWhite+" ("+aGreen+"%.2f KH/s avg"+aBWhite+")"+
			" | "+aBlue+"Height:%d"+aBWhite+
			" | "+aCyan+"Miniblocks:%d"+aBWhite+
			" | "+aGreen+"Blocks:%d"+aBWhite+
			" | "+rejcol+"REJ:%d"+aBWhite+
			" | "+aMagenta+"Diff:%s"+aBWhite+
			" | "+aWhite+"%02d:%02d:%02d"+aBWhite+aReset,
			f.Rate, f.Avg, f.Height, f.MiniBlocks, f.Blocks,
			f.Rejected, f.Diff, up/3600, up/60%60, up%60)
		if f.Verbose {
			line += fmt.Sprintf(" | funnel submitted:%d acc:%d rej:%d stale:%d discarded:%d sendfail:%d",
				f.Submitted, f.MiniBlocks, f.Rejected, f.Stale, f.Discarded, f.SendFails)
		}
		return line
	}

	cases := []statusFields{
		{Rate: 11.31, Avg: 11.02, Height: 3141592, MiniBlocks: 962, Blocks: 93, Diff: "795K", Up: 71},
		{Rate: 0, Avg: 4.96, Height: 7368356, MiniBlocks: 962, Blocks: 93, Rejected: 25, Diff: "795K", Up: 71},
		{Rate: 1.5, Avg: 1.25, Height: 42, Rejected: 1, Diff: "0", Up: 3601,
			Verbose: true, Submitted: 10, Stale: 2, Discarded: 3, SendFails: 1},
	}
	for i, f := range cases {
		if got, want := renderTier(tierFull, console.Colour(), f).String(), legacy(f); got != want {
			t.Fatalf("case %d:\ngot  %q\nwant %q", i, got, want)
		}
	}
}

func TestSnapshotStatusIncludesDiscardedSubmits(t *testing.T) {
	client := &getwork.Client{}
	client.Discarded.Store(7)
	if got := snapshotStatus(&miner.State{}, client, 0, 0, 0, true).Discarded; got != 7 {
		t.Fatalf("Discarded = %d, want 7", got)
	}
}

func TestElideWallet(t *testing.T) {
	full := "dero1qyvuemd6z0uzsx5ufc99f0jhyzvvpysmrd2t3526ht7a9dfh7jve2qqt0vu5y"
	if got := elideWallet(full); got != "dero1qyv...vu5y" {
		t.Fatalf("elideWallet(full) = %q", got)
	}
	if got := elideWallet("dero1qyq"); got != "dero1qyq" {
		t.Fatalf("short wallet must pass through, got %q", got)
	}
	nonASCII := "dero1qyq\u00e9zzzzzzzzzzzz"
	if got := elideWallet(nonASCII); got != nonASCII {
		t.Fatalf("non-ASCII wallet must pass through, got %q", got)
	}
}
