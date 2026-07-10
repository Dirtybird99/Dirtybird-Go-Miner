// go-miner: a pure-Go DERO AstroBWTv3 CPU miner (GETWORK over websocket).
// Sibling of the family's C++/Zig/Rust miners; protocol semantics ported from
// derohe cmd/dero-miner.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
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

// statusLoop renders the family status line at 1 Hz until ctx ends:
// [DIRTYBIRD] rate (avg) | Height | Miniblocks | Blocks | REJ | Diff | uptime
// — byte-for-byte the zig miner's reporter().
func statusLoop(ctx context.Context, cons *console.Console, st *miner.State, client *getwork.Client, o *options) {
	start := time.Now()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	// With SetGCPercent(-1) the collector only runs at the 2 GiB memory
	// limit; the small steady allocation drip (job JSON, status strings,
	// share hex) would ratchet RSS there over multi-day runs. An hourly
	// forced GC keeps the footprint at tens of MB; the live set is ~20
	// scratches, so the pause is immaterial.
	gcTick := time.NewTicker(time.Hour)
	defer gcTick.Stop()

	// The displayed rate is a ~15s sliding window (real timestamps), not the
	// raw 1s delta: per-thread counters flush in 16-hash chunks and tick
	// spacing jitters, so a 1s window bounces several percent around a flat
	// true rate. The ring starts filled with the start point, so the readout
	// ramps as an avg-since-start until the window is full.
	type ratePoint struct {
		t time.Time
		h uint64
	}
	const rateSlots = 16
	var ring [rateSlots]ratePoint
	for i := range ring {
		ring[i] = ratePoint{t: start}
	}
	tickN := 0
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
		slot := tickN % rateSlots
		old := ring[slot] // the sample rateSlots ticks ago
		ring[slot] = ratePoint{t: now, h: cur}
		tickN++
		dt := now.Sub(old.t).Seconds()
		if dt <= 0 {
			dt = 1
		}
		rate := float64(cur-old.h) / dt / 1000 // KH/s over the window
		elapsed := time.Since(start)
		avg := float64(cur) / elapsed.Seconds() / 1000
		up := int(elapsed.Seconds())
		rej := st.Rejected.Load()
		rejcol := aWhite
		if rej > 0 {
			rejcol = aBRed
		}
		line := fmt.Sprintf(aBYellow+"[DIRTYBIRD] "+
			aBGreen+"%.2f KH/s"+aBWhite+" ("+aGreen+"%.2f KH/s avg"+aBWhite+")"+
			" | "+aBlue+"Height:%d"+aBWhite+
			" | "+aCyan+"Miniblocks:%d"+aBWhite+
			" | "+aGreen+"Blocks:%d"+aBWhite+
			" | "+rejcol+"REJ:%d"+aBWhite+
			" | "+aMagenta+"Diff:%s"+aBWhite+
			" | "+aWhite+"%02d:%02d:%02d"+aBWhite+aReset,
			rate, avg, st.Height.Load(), st.MiniBlocks.Load(), st.Blocks.Load(),
			rej, fmtDiff(st.Diff.Load()), up/3600, up/60%60, up%60)
		if o.verbose {
			line += fmt.Sprintf(" | funnel submitted:%d acc:%d rej:%d stale:%d sendfail:%d",
				st.Submitted.Load(), st.MiniBlocks.Load(), rej, st.Stale.Load(), client.SendFails.Load())
		}
		cons.Status(line)
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

// runSetup interactively writes config.json (the zig miner's --setup flow).
func runSetup(o *options) int {
	cfgPath := o.cfgPath
	if cfgPath == "" {
		cfgPath = "config.json"
	}
	cur, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not parse %s: %v\n", cfgPath, err)
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

	sc := bufio.NewScanner(os.Stdin)
	prompt := func(label, def string) string {
		fmt.Fprintf(os.Stderr, "  %s [%s]: ", label, def)
		if !sc.Scan() {
			return def
		}
		if s := strings.TrimSpace(sc.Text()); s != "" {
			return s
		}
		return def
	}

	fmt.Fprintln(os.Stderr, "Setup -- press Enter to keep the current value.")
	daemon = prompt("Daemon/pool host:port", daemon)
	wallet = prompt("DERO wallet", wallet)
	if s := prompt("Threads (-1 = auto)", fmt.Sprintf("%d", threads)); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			threads = n
		}
	}
	if err := config.Save(cfgPath, daemon, wallet, threads); err != nil {
		fmt.Fprintf(os.Stderr, "error: could not write %s: %v\n", cfgPath, err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "saved %s\n", cfgPath)
	return 0
}
