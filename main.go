// go-miner: a pure-Go DERO AstroBWTv3 CPU miner (GETWORK over websocket).
// Sibling of the family's C++/Zig/Rust miners; protocol semantics ported from
// derohe cmd/dero-miner.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/cpuid/v2"

	"go-miner/internal/astrobwt"
	"go-miner/internal/config"
	"go-miner/internal/console"
	"go-miner/internal/getwork"
	"go-miner/internal/miner"
)

const (
	defaultDaemon = "community-pools.mysrv.cloud:10300"
	defaultWallet = "dero1qyvuemd6z0uzsx5ufc99f0jhyzvvpysmrd2t3526ht7a9dfh7jve2qqt0vu5y"
	maxThreads    = 255 // thread id lives in nonce byte 47
)

var setupEndpoints = []struct {
	name, address string
}{
	{"Community Pools", defaultDaemon},
	{"Rabid Mining", "dero.rabidmining.com:10300"},
	{"dero-node.net solo", "dero-node.net:10100"},
	{"DERO Foundation solo/full-block", "node.derofoundation.org:10100"},
}

type options struct {
	daemon, wallet, cfgPath      string
	saName, cpuProfile           string
	threads, secs                int
	bench, sustained, selftest   bool
	statbench                    bool
	dryRun, pin, high, debugFlag bool
	pair                         bool
	showVersion                  bool
	verbose, setup               bool
}

func (o *options) backend() astrobwt.Backend {
	if o.saName == "sais" {
		return astrobwt.BackendSAIS
	}
	return astrobwt.BackendV114
}

func validSAName(name string) bool { return name == "v114" || name == "sais" }

func parseFlags() *options {
	o := &options{}
	flag.StringVar(&o.daemon, "d", "", "")
	flag.StringVar(&o.daemon, "daemon-address", "", "")
	flag.StringVar(&o.wallet, "w", "", "")
	flag.StringVar(&o.wallet, "wallet", "", "")
	flag.IntVar(&o.threads, "t", 0, "")
	flag.IntVar(&o.threads, "threads", 0, "")
	flag.StringVar(&o.cfgPath, "c", "", "")
	flag.StringVar(&o.cfgPath, "config", "", "")
	flag.StringVar(&o.cfgPath, "config-file", "", "")
	flag.BoolVar(&o.bench, "bench", false, "")
	flag.BoolVar(&o.sustained, "sustained", false, "")
	flag.BoolVar(&o.statbench, "statbench", false, "")
	flag.IntVar(&o.secs, "secs", 30, "")
	flag.BoolVar(&o.selftest, "selftest", false, "")
	flag.BoolVar(&o.setup, "setup", false, "")
	flag.BoolVar(&o.dryRun, "dry-run", false, "")
	flag.BoolVar(&o.pin, "pin", false, "")
	flag.BoolVar(&o.high, "high", false, "")
	flag.BoolVar(&o.pair, "pair", false, "")
	flag.StringVar(&o.saName, "sa", "v114", "")
	flag.BoolVar(&o.debugFlag, "debug", false, "")
	flag.StringVar(&o.cpuProfile, "cpuprofile", "", "")
	flag.BoolVar(&o.verbose, "V", false, "")
	flag.BoolVar(&o.verbose, "verbose", false, "")
	flag.BoolVar(&o.showVersion, "v", false, "")
	flag.BoolVar(&o.showVersion, "version", false, "")
	flag.Usage = usage
	flag.Parse()
	return o
}

// usage mirrors the zig miner's help text (same flags, same shape), with the
// power-user flags in a trailing section.
func usage() {
	fmt.Fprintf(os.Stderr, `Usage: go-miner [-d [ws://|wss://]host:port] [-w wallet] [-t threads] [-c config.json] [-V] [--selftest]
  -d  daemon/pool address [scheme://]host:port  (default %s)
        DERO getwork (local derod AND pools) is TLS: bare and wss:// connect over TLS.
        ws:// forces plaintext (only for getwork behind a TLS-terminating proxy).
  -w  DERO wallet address            (default from config.json / built-in)
  -t  mining threads                 (default: logical CPU count)
  -c, --config-file <path>           config file (default: config.json)
  -V  verbose
  --selftest  run pow("a") KAT and exit (0=PASS,1=FAIL)
  --bench     run an AstroBWTv3 hashrate benchmark and exit
  --setup     interactively write config.json (pool/wallet/threads), then exit
  -h, --help / -v, --version

advanced (benchmarking/tuning):
  --sustained --secs N   fixed-window all-threads benchmark
  --statbench --secs N    real-worker status-line stability benchmark
  --pin / --high         P-core-first thread pinning / HIGH process priority
  --pair                 2 nonces/thread with 2-way SHA-NI final hash
  --sa v114|sais         suffix-array backend (default v114)
  --dry-run / --debug / --cpuprofile <file>
`, defaultDaemon)
}

// resolve applies precedence: explicit flag > config.json > compiled default.
func (o *options) resolve(f *config.File) {
	if o.daemon == "" && f != nil && f.DaemonAddress != nil {
		o.daemon = *f.DaemonAddress
	}
	if o.daemon == "" {
		o.daemon = defaultDaemon
	}
	if o.wallet == "" && f != nil && f.Wallet != nil {
		o.wallet = *f.Wallet
	}
	if o.wallet == "" {
		o.wallet = defaultWallet
	}
	if o.threads == 0 && f != nil && f.Threads != nil {
		o.threads = *f.Threads
	}
	if o.threads <= 0 {
		o.threads = runtime.NumCPU()
	}
	if o.threads > maxThreads {
		o.threads = maxThreads
	}
}

func main() { os.Exit(run()) }

func run() int {
	o := parseFlags()

	if o.showVersion {
		fmt.Printf("go-miner %s\n", version)
		return 0
	}
	if o.selftest {
		return runSelftest()
	}
	if o.setup {
		return runSetup(o)
	}
	if !validSAName(o.saName) {
		fmt.Fprintf(os.Stderr, "unknown --sa backend %q (want v114 or sais)\n", o.saName)
		return 1
	}
	cons := console.New()
	if err := kat(); err != nil {
		cons.Logf("ERROR", "pow(\"a\") self-test failed; refusing to mine.")
		return 1
	}

	// Steady-state heap is static (per-worker scratches); turn the collector
	// off with a memory-limit safety net.
	debug.SetGCPercent(-1)
	debug.SetMemoryLimit(2 << 30)

	if o.cpuProfile != "" {
		f, err := os.Create(o.cpuProfile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer f.Close()
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	// -high must apply to bench runs too, or benchmarks under background
	// load compare a NORMAL-priority go-miner against HIGH-priority siblings.
	if o.high {
		if err := miner.SetHighPriority(); err != nil {
			fmt.Fprintf(os.Stderr, "WARN could not set HIGH priority: %v\n", err)
		}
	}

	if o.bench || o.sustained || o.statbench {
		threads := o.threads
		if threads <= 0 {
			threads = runtime.NumCPU()
		}
		if o.bench {
			return runBench(threads, o.pin, o.backend(), o.pair)
		}
		if o.statbench {
			return runStatBench(cons, threads, o.secs, o)
		}
		return runSustained(threads, o.secs, o.pin, o.backend(), o.pair)
	}

	cfgPath := o.cfgPath
	if cfgPath == "" {
		if exe, err := os.Executable(); err == nil {
			cfgPath = filepath.Join(filepath.Dir(exe), "config.json")
		} else {
			cfgPath = "config.json"
		}
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	o.resolve(cfg)

	// Startup banner: the family look (zig miner main.zig ordering).
	cons.Logf("INFO", "Dirtybird Miner")
	cons.Logf("INFO", "Server:  %s", serverDisplay(o.daemon))
	cons.Logf("INFO", "Wallet:  %s", o.wallet)
	cons.Logf("INFO", "Threads: %d", o.threads)
	cons.Logf("INFO", "CPU: %s", cpuBrand())
	cons.Logf("INFO", "Features: avx2 %s | avx512 %s | sha %s",
		yesNo(cpuid.CPU.Supports(cpuid.AVX2)), yesNo(cpuid.CPU.Supports(cpuid.AVX512F)), yesNo(cpuid.CPU.Supports(cpuid.SHA)))
	cons.Logf("INFO", "Fast path: SHA-NI build %s; AVX512 mining path No",
		yesNo(cpuid.CPU.Supports(cpuid.SHA, cpuid.SSSE3, cpuid.SSE4)))
	fmt.Fprintln(os.Stderr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	st := &miner.State{}
	submits := make(chan getwork.Submit, 16)

	var prevMB, prevBlocks, prevRej uint64
	var counted bool
	client := &getwork.Client{
		Endpoint: o.daemon,
		Wallet:   o.wallet,
		Submits:  submits,
		Logf: func(format string, args ...interface{}) {
			cons.Logf("INFO", format, args...)
		},
	}
	if o.debugFlag {
		client.Debugf = func(format string, args ...interface{}) {
			cons.Logf("DEBUG", format, args...)
		}
	}
	client.OnJob = func(j getwork.Job) {
		if o.debugFlag {
			cons.Logf("DEBUG", "job %s height=%d diff=%d mb=%d blocks=%d rej=%d",
				j.JobID, j.Height, j.Difficultyuint64, j.MiniBlocks, j.Blocks, j.Rejected)
		}
		_, err := st.SetJob(j)
		if err != nil {
			if o.debugFlag {
				cons.Logf("DEBUG", "rejected job push: %v", err)
			}
			return
		}
		// The family CLIs surface share accounting only through the status
		// line counters; per-event lines are -debug chatter.
		if o.debugFlag {
			if j.LastError != "" {
				cons.Logf("DEBUG", "daemon reports: %s", j.LastError)
			}
			if !counted {
				prevMB, prevBlocks, prevRej = j.MiniBlocks, j.Blocks, j.Rejected
				counted = true
				return
			}
			if j.MiniBlocks > prevMB {
				cons.Logf("DEBUG", "miniblock ACCEPTED (%d total)", j.MiniBlocks)
			}
			if j.Blocks > prevBlocks {
				cons.Logf("DEBUG", "block FOUND (%d total)", j.Blocks)
			}
			if j.Rejected > prevRej {
				cons.Logf("DEBUG", "share REJECTED (%d total)", j.Rejected)
			}
			prevMB, prevBlocks, prevRej = j.MiniBlocks, j.Blocks, j.Rejected
		}
	}

	if !o.dryRun {
		var pinOrder []int
		if o.pin {
			pinOrder = miner.PinOrder()
			if o.debugFlag {
				cons.Logf("DEBUG", "pin order: %v", pinOrder)
			}
		}
		for t := 0; t < o.threads; t++ {
			go miner.Run(ctx, t, st, submits, pinOrder, o.backend(), o.pair)
		}
	} else {
		cons.Logf("INFO", "dry run: watching jobs only, not mining")
	}

	go client.Run(ctx)
	statusLoop(ctx, cons, st, client, o)
	fmt.Fprintf(os.Stderr, "\nShutdown. %d hashes, %d miniblocks (%d blocks), %d rejected.\n",
		st.TotalHashes.Load(), st.MiniBlocks.Load(), st.Blocks.Load(), st.Rejected.Load())
	return 0
}

// ANSI palette for the status line (the zig miner's main.zig set; log lines
// stay uncolored).
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

const hashrateWindowSlots = 10

type hashratePoint struct {
	at     time.Time
	hashes uint64
}

type hashrateWindow struct {
	points [hashrateWindowSlots]hashratePoint
	start  hashratePoint
	next   int
}

func newHashrateWindow(at time.Time, hashes uint64) hashrateWindow {
	start := hashratePoint{at: at, hashes: hashes}
	w := hashrateWindow{start: start}
	for i := range w.points {
		w.points[i] = start
	}
	return w
}

func rateKHS(hashes uint64, elapsed time.Duration) float64 {
	if elapsed <= 0 {
		return 0
	}
	return float64(hashes) / elapsed.Seconds() / 1000
}

func rateBetween(newer, older hashratePoint) float64 {
	if newer.hashes < older.hashes {
		return 0
	}
	return rateKHS(newer.hashes-older.hashes, newer.at.Sub(older.at))
}

func (w *hashrateWindow) sample(at time.Time, hashes uint64) float64 {
	current := hashratePoint{at: at, hashes: hashes}
	old := w.points[w.next]
	w.points[w.next] = current
	w.next = (w.next + 1) % len(w.points)
	return rateBetween(current, old)
}

func (w *hashrateWindow) average(at time.Time, hashes uint64) float64 {
	return rateBetween(hashratePoint{at: at, hashes: hashes}, w.start)
}

type statusFields struct {
	rate, average                        float64
	height, miniblocks, blocks, rejected uint64
	diff                                 string
	uptime                               time.Duration
	verbose                              bool
	submitted, stale, sendFails          uint64
}

func statusLayouts(s statusFields, color bool) (full, compact, minimal string) {
	reset, yellow, brightGreen, brightWhite := "", "", "", ""
	green, blue, cyan, magenta, white, brightRed := "", "", "", "", "", ""
	if color {
		reset, yellow, brightGreen, brightWhite = aReset, aBYellow, aBGreen, aBWhite
		green, blue, cyan, magenta, white, brightRed = aGreen, aBlue, aCyan, aMagenta, aWhite, aBRed
	}
	rejectedColor := white
	if s.rejected > 0 {
		rejectedColor = brightRed
	}
	seconds := int(s.uptime.Seconds())
	full = fmt.Sprintf(yellow+"[DIRTYBIRD] "+
		brightGreen+"%.2f KH/s"+brightWhite+" ("+green+"%.2f KH/s avg"+brightWhite+")"+
		" | "+blue+"Height:%d"+brightWhite+
		" | "+cyan+"Miniblocks:%d"+brightWhite+
		" | "+green+"Blocks:%d"+brightWhite+
		" | "+rejectedColor+"REJ:%d"+brightWhite+
		" | "+magenta+"Diff:%s"+brightWhite+
		" | "+white+"%02d:%02d:%02d"+brightWhite+reset,
		s.rate, s.average, s.height, s.miniblocks, s.blocks, s.rejected,
		s.diff, seconds/3600, seconds/60%60, seconds%60)
	if s.verbose {
		full += fmt.Sprintf(" | funnel submitted:%d acc:%d rej:%d stale:%d sendfail:%d",
			s.submitted, s.miniblocks, s.rejected, s.stale, s.sendFails)
	}
	compact = fmt.Sprintf(yellow+"[DIRTYBIRD] "+brightGreen+"%.2f KH/s"+brightWhite+
		" H:"+blue+"%d"+brightWhite+
		" M:"+cyan+"%d"+brightWhite+
		" B:"+green+"%d"+brightWhite+
		" R:"+rejectedColor+"%d"+reset,
		s.rate, s.height, s.miniblocks, s.blocks, s.rejected)
	minimal = fmt.Sprintf(brightGreen+"%.2f KH/s"+brightWhite+" H:"+blue+"%d"+reset,
		s.rate, s.height)
	return full, compact, minimal
}

// formatStatusLine keeps redirected records complete (columns <= 0), while an
// interactive terminal gets the richest layout that fits without using its
// final column. If even the minimal layout is too wide, return a clipped
// uncolored record so ANSI sequences can never be cut in half.
func formatStatusLine(s statusFields, color bool, columns int) string {
	width := columns
	if width > 1 {
		width--
	}
	plainFull, plainCompact, plainMinimal := statusLayouts(s, false)
	full, compact, minimal := plainFull, plainCompact, plainMinimal
	if color {
		full, compact, minimal = statusLayouts(s, true)
	}
	switch {
	case width <= 0 || len(plainFull) <= width:
		return full
	case len(plainCompact) <= width:
		return compact
	case len(plainMinimal) <= width:
		return minimal
	default:
		return plainMinimal[:width]
	}
}

// statusLoop renders the family status line at 1 Hz until ctx ends:
// [DIRTYBIRD] rate (avg) | Height | Miniblocks | Blocks | REJ | Diff | uptime
// — byte-for-byte the zig miner's reporter().
func statusLoop(ctx context.Context, cons *console.Console, st *miner.State, client *getwork.Client, o *options) {
	start := time.Now()
	startHashes := st.TotalHashes.Load()
	rates := newHashrateWindow(start, startHashes)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	// With SetGCPercent(-1) the collector only runs at the 2 GiB memory
	// limit; the small steady allocation drip (job JSON, status strings,
	// share hex) would ratchet RSS there over multi-day runs. An hourly
	// forced GC keeps the footprint at tens of MB; the live set is ~20
	// scratches, so the pause is immaterial.
	gcTick := time.NewTicker(time.Hour)
	defer gcTick.Stop()

	// The displayed rate is a ~10s sliding window (real timestamps), not the
	// raw 1s delta: per-thread counters flush in 16-hash chunks and tick
	// spacing jitters, so a 1s window bounces several percent around a flat
	// true rate. The ring starts filled with the start point, so the readout
	// ramps as an avg-since-start until the window is full.
	for {
		select {
		case <-ctx.Done():
			return
		case <-gcTick.C:
			runtime.GC()
			continue
		case <-tick.C:
		}
		cur := st.TotalHashes.Load()
		now := time.Now()
		rate := rates.sample(now, cur)
		elapsed := now.Sub(start)
		avg := rates.average(now, cur)
		rej := st.Rejected.Load()
		fields := statusFields{
			rate:       rate,
			average:    avg,
			height:     st.Height.Load(),
			miniblocks: st.MiniBlocks.Load(),
			blocks:     st.Blocks.Load(),
			rejected:   rej,
			diff:       fmtDiff(st.Diff.Load()),
			uptime:     elapsed,
			verbose:    o.verbose,
			submitted:  st.Submitted.Load(),
			stale:      st.Stale.Load(),
			sendFails:  client.SendFails.Load(),
		}
		cons.Status(
			formatStatusLine(fields, true, cons.TerminalWidth()),
			formatStatusLine(fields, false, 0),
		)
	}
}

// fmtDiff humanizes difficulty by integer division (zig miner fmtDiff: 20K,
// not 20.0K).
func fmtDiff(n uint64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%dG", n/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%dM", n/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%dK", n/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// serverDisplay shows the effective scheme like the zig banner (bare
// host:port dials TLS, so it displays as wss://).
func serverDisplay(ep string) string {
	if strings.Contains(ep, "://") {
		return ep
	}
	return "wss://" + ep
}

func cpuBrand() string {
	if b := cpuid.CPU.BrandName; b != "" {
		return b
	}
	return "unknown CPU"
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

func validHostname(host string) bool {
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	host = strings.TrimSuffix(host, ".")
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') &&
				(c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}

func validEndpoint(endpoint string) bool {
	if endpoint == "" || endpoint != strings.TrimSpace(endpoint) {
		return false
	}
	for _, c := range endpoint {
		if c <= ' ' || c == 0x7f || c == '"' || c == '\\' {
			return false
		}
	}
	address := endpoint
	if i := strings.Index(address, "://"); i >= 0 {
		if address[:i] != "ws" && address[:i] != "wss" {
			return false
		}
		address = address[i+3:]
	}
	if strings.ContainsAny(address, "/?#@") {
		return false
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil || (net.ParseIP(host) == nil && !validHostname(host)) {
		return false
	}
	for _, c := range portText {
		if c < '0' || c > '9' {
			return false
		}
	}
	port, err := strconv.Atoi(portText)
	return err == nil && port >= 1 && port <= 65535
}

func promptEndpoint(sc *bufio.Scanner, out io.Writer, current string) string {
	for {
		fmt.Fprintln(out, "  Endpoint:")
		for i, endpoint := range setupEndpoints {
			fmt.Fprintf(out, "    %d) %s — %s\n", i+1, endpoint.name, endpoint.address)
		}
		fmt.Fprintln(out, "    5) Custom")
		fmt.Fprintf(out, "  Choose 1-5 [%s]: ", current)
		if !sc.Scan() {
			return current
		}
		choice := strings.TrimSpace(sc.Text())
		if choice == "" {
			return current
		}
		if n, err := strconv.Atoi(choice); err == nil && n >= 1 && n <= len(setupEndpoints) {
			return setupEndpoints[n-1].address
		}
		if choice != "5" {
			fmt.Fprintln(out, "  Invalid choice; enter 1-5.")
			continue
		}
		for {
			fmt.Fprint(out, "  Custom [ws://|wss://]host:port (blank cancels): ")
			if !sc.Scan() || sc.Text() == "" {
				return current
			}
			if endpoint := sc.Text(); validEndpoint(endpoint) {
				return endpoint
			}
			fmt.Fprintln(out, "  Invalid endpoint; use [ws://|wss://]host:port with port 1-65535.")
		}
	}
}

// runSetup interactively writes config.json (the zig miner's --setup flow).
func runSetup(o *options) int {
	return runSetupIO(o, os.Stdin, os.Stderr)
}

func runSetupIO(o *options, in io.Reader, out io.Writer) int {
	cfgPath := o.cfgPath
	if cfgPath == "" {
		cfgPath = "config.json"
	}
	cur, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(out, "warning: could not parse %s: %v\n", cfgPath, err)
	}
	daemon, wallet, threads := defaultDaemon, defaultWallet, -1
	if cur != nil {
		if cur.DaemonAddress != nil {
			daemon = *cur.DaemonAddress
		}
		if cur.Wallet != nil {
			wallet = *cur.Wallet
		}
		if cur.Threads != nil {
			threads = *cur.Threads
		}
	}

	sc := bufio.NewScanner(in)
	prompt := func(label, def string) string {
		fmt.Fprintf(out, "  %s [%s]: ", label, def)
		if !sc.Scan() {
			return def
		}
		if s := strings.TrimSpace(sc.Text()); s != "" {
			return s
		}
		return def
	}

	fmt.Fprintln(out, "Setup -- press Enter to keep the current value.")
	daemon = promptEndpoint(sc, out, daemon)
	wallet = prompt("DERO wallet", wallet)
	if s := prompt("Threads (-1 = auto)", fmt.Sprintf("%d", threads)); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			threads = n
		}
	}
	if err := config.Save(cfgPath, daemon, wallet, threads); err != nil {
		fmt.Fprintf(out, "error: could not write %s: %v\n", cfgPath, err)
		return 1
	}
	fmt.Fprintf(out, "saved %s\n", cfgPath)
	return 0
}
