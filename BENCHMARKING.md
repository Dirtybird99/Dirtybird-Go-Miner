# Benchmarking

This repo uses a small benchmark matrix inspired by Kolkov's regex benchmark
repos: fixed workloads, side-by-side variants, raw logs, machine metadata, and
generated summaries. The research trail is recorded in
[PERF_RESEARCH.md](PERF_RESEARCH.md). These repos are external references for
methodology only; their regex-engine code is not vendored into the miner.

Optional source-repository provenance can be included by passing checkout paths:

```powershell
powershell -ExecutionPolicy Bypass -File scripts\bench-matrix.ps1 `
  -RegexBenchPath C:\src\regex-bench `
  -CoregexPath C:\src\coregex
```

Omitted paths remain recorded as "not checked out"; none of these external
repositories are required to build or benchmark the miner.

**Measurement discontinuity (2026-08):** the harness rewrite that aligned the
workload with the Rust/Zig comparators changed three things at once — the input
blob generation (Zig-matched xoshiro256++ stream with real big-endian nonces at
bytes 43-46, replacing random blobs varied at bytes 0-1), the rate denominator
(actual elapsed time including the join tail, replacing the nominal window), and
the worker counter batch. Sustained figures recorded before this rewrite are not
comparable to figures recorded after it — newer numbers read lower for the same
build. Do not bridge comparisons across the rewrite; re-baseline instead.

Also note `--bench` runs a 1-second warmup per thread count while `--sustained`
starts cold (its first checkpoint row is labelled `ramp` for this reason), so
the two modes are not directly comparable at equal thread counts.

## AMD Linux / HiveOS Scaling

`scripts/bench-linux-scaling.sh` measures topology, pairing, the v4 AVX-512
candidate, and optional Linux worker pinning without connecting to a daemon.
It takes local binaries only: use the published v0.2.18 Linux binary as the
immutable baseline, then build current v3/v4 candidates from the same source,
Go toolchain, and PGO profile. Keep debug symbols so returned pprof files can
be resolved:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v3 \
  go build -pgo=default.pgo -trimpath -o go-miner-v3 .
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v4 \
  go build -pgo=default.pgo -trimpath -o go-miner-v4 .
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v4 \
  go test -c -o astrobwt-v4.test ./internal/astrobwt

bash scripts/bench-linux-scaling.sh screen \
  --baseline ./go-miner-v0.2.18 --current-v3 ./go-miner-v3 \
  --current-v4 ./go-miner-v4 --v4-test-binary ./astrobwt-v4.test \
  --label zen-host
```

The screen runs v0.2.18/x1 and current v3/v4 x1/x2 at geometric and detected
physical/SMT boundaries, with two mirrored 120-second legs per arm. The runner
verifies version, self-tests, requested pipeline and pin mode, build metadata,
and the v4 build marker before accepting a rate. A v4 test binary is required
whenever v4 can run; it executes the focused differential suite and the
1,000,008-hash gate before timing, followed by a recorded idle cooldown of at
least three minutes. Use the same-binary confirmation below to isolate Linux
pinning. If `screen --miner-pin` is used to find the maximum, the v0.2.18 arm
remains unpinned because that release has no Linux affinity.

Confirm only the screen winner against its runner-up or baseline:

```bash
bash scripts/bench-linux-scaling.sh confirm \
  --base ./go-miner-v3 --candidate ./go-miner-v4 \
  --base-kind v3 --candidate-kind v4 \
  --base-pair x2 --candidate-pair x2 \
  --v4-test-binary ./astrobwt-v4.test --threads 1 \
  --label v4-vs-v3-1t

# Repeat at the high-throughput count selected by the independent screen;
# use "physical", "logical", or its numeric thread count.
bash scripts/bench-linux-scaling.sh confirm \
  --base ./go-miner-v3 --candidate ./go-miner-v4 \
  --base-kind v3 --candidate-kind v4 \
  --base-pair x2 --candidate-pair x2 \
  --v4-test-binary ./astrobwt-v4.test --threads logical \
  --label v4-vs-v3-peak

# The same binary on both arms isolates Linux worker pinning.
bash scripts/bench-linux-scaling.sh confirm \
  --base ./go-miner-v3 --candidate ./go-miner-v3 \
  --base-kind v3 --candidate-kind v3 --base-pair x2 --candidate-pair x2 \
  --threads logical --candidate-pin --label pin-vs-unpinned

bash scripts/bench-linux-scaling.sh profile \
  --binary ./go-miner-v4 --kind v4 --pair x2 \
  --v4-test-binary ./astrobwt-v4.test --label v4-profile
```

Confirmation uses the repository's eight-leg, 240-second Thue-Morse order and
writes analyzer-compatible `legs.csv`. `--base-pin` and `--candidate-pin`
control affinity per arm; `--miner-pin` pins both when neither is the v0.2.18
baseline. Profiling is deliberately separate: the existing Go CPU profiler
runs at 1T, all-physical, and all-logical, while
`perf stat` records generic counters only when available and permitted. Every
mode writes raw logs, topology, build hashes, CSV/JSON metadata, checksums, and
a `.tar.gz` beneath ignored `bench-results/linux-scaling/`.

If AMD uProf is already installed, append
`--uprof /opt/AMDuProf_X.Y-ZZZ/bin/AMDuProfCLI`; profile mode then adds a
separate hotspots/call-stack collection at each target count. It never installs
uProf or makes it a benchmark dependency.

The AMD retention targets are 1T and the peak physical/logical boundary chosen
by the independent screen. Run a confirmation block at both. Keep a candidate
only when one target's one-sided 95% lower bound exceeds +0.6% and the other
has point estimate and CI upper bound no worse than -0.5%; neither archive is
retention-grade by itself. Direct v4 confirmation and profiling require the
same native test bundle and cooldown as screening.

## Go / Rust / Zig Head-to-Head

`scripts\bench-head-to-head.ps1` runs the same deterministic blob stream and
nonce sequence in all three miners. Run x1 and x2 separately, then compare the
best validated mode for each miner/thread count:

```powershell
powershell -ExecutionPolicy Bypass -File scripts\bench-head-to-head.ps1 `
  -GoBinary .\go-miner.exe `
  -RustBinary "C:\path\to\dero-miner.exe" `
  -ZigBinary "C:\path\to\bench.exe" `
  -Pipeline x1 -Threads 1,20 -DurationSecs 120 -CooldownSecs 180 -PinHigh

powershell -ExecutionPolicy Bypass -File scripts\bench-head-to-head.ps1 `
  -GoBinary .\go-miner.exe `
  -RustBinary "C:\path\to\dero-miner.exe" `
  -ZigBinary "C:\path\to\bench2.exe" `
  -Pipeline x2 -Threads 1,20 -DurationSecs 120 -CooldownSecs 180 -PinHigh
```

The runner uses balanced `Go-Rust-Zig-Zig-Rust-Go` ordering, hashes every
artifact, records tool versions and explicit Rust environment controls, and
writes raw logs, `runs.csv`, and `manifest.json` under ignored
`bench-results\head-to-head\`. Rust experiment overrides are cleared for each
run and the caller's original environment is restored afterward. The runner
persists and rejects an x1/x2 banner mismatch, an unparseable rate, a nonzero
exit, or a launch error. It also rejects an output path outside `bench-results`,
a thread count outside 1-255, or a `-PinHigh` thread count above the frozen Zig
comparator's 24-entry affinity map. `-DryRun` prints every command without
executing it.

Go, Rust, and Zig all start timing before worker creation and report actual
elapsed time including the final batch/join tail. The Go workload is pinned by
`TestBenchmarkWorkMatchesZigSeed`; it reproduces Zig's
`DefaultPrng.init(12345 + tid)`, byte 47 thread id, and consecutive big-endian
nonces at bytes 43-46.

## Matrix Run

From the repo root:

```powershell
powershell -ExecutionPolicy Bypass -File scripts\bench-matrix.ps1
```

The script builds one optimized local binary with `GOAMD64=v3` and
`-pgo=default.pgo` when available, then writes results under
`bench-results\<timestamp>\`:

- `raw.log` has full command output.
- `results.csv` has parseable per-run benchmark rows.
- `aggregate.csv` ranks variants by median KH/s and includes min/max/mean/stddev.
- `summary.md` ranks variants by median KH/s.
- `env.json` records commit, dirty status, CPU, Go version, and benchmark
  settings, candidate name, and optional external source repo commits.

Use `-Candidate <name>` to label a baseline or experiment:

```powershell
powershell -ExecutionPolicy Bypass -File scripts\bench-matrix.ps1 -Candidate kolkov-baseline
```

Useful quick smoke test:

```powershell
powershell -ExecutionPolicy Bypass -File scripts\bench-matrix.ps1 -Secs 5 -Repeat 1 -Threads 1,2
```

Optional overcommit check:

```powershell
powershell -ExecutionPolicy Bypass -File scripts\bench-matrix.ps1 -IncludeOvercommit
```

Optional v114 descriptor counters:

```powershell
go run -tags v114stats . --sustained --secs 30 -t 20 --sa v114 --pin --high
```

The `v114stats` tag prints group-count and merge-path counters after benchmark
runs. Normal builds do not include counter overhead.

Status-line stability can be measured through the real worker pipeline without
a daemon (redirected output emits one plain status record per second, so no
environment variable is needed; `GOMINER_FORCE_STATUS=1` is still accepted):

```powershell
go run . --statbench --secs 90 -t 20 --pin --high 2> statbench.log
```

The 16-hash counter flush and sliding rate window target display stability;
they are not included in the native-key hashrate improvement claim below.

## Current Local Finding

The latest fair comparison uses 20 threads only. Pinned/high-priority ABBA
(`baseline/candidate/candidate/baseline`, 45s windows, 20s cooldowns) measured
the native-key radix candidate against detached `4bba298`:

```powershell
go-miner.exe --sustained --secs 45 -t 20 --sa v114 --pin --high
```

Observed medians:

```text
baseline  18.823933, 18.645533 KH/s -> 18.734733 median
candidate 19.004244, 18.877644 KH/s -> 18.940944 median (+1.100688%)
```

The single-core pinned microbenchmark improved +2.33%. The sustained result has
two legs per arm, so retain the raw values and caveat rather than treating the
point estimate as a universal CPU claim. Generated `bench-results` are local and
ignored; the protocol and all four sustained readings are recorded here.

## Optimization Loop

Use regex-benchmark/coregex notes as hypothesis sources, not as correctness
evidence. AstroBWT is consensus hashing; every candidate must remain
byte-identical to the reference.

Default loop:

1. Run `go test ./...`.
2. Establish a fresh matrix baseline with `scripts\bench-matrix.ps1 -Candidate baseline`.
3. Profile only the current best setting with `--cpuprofile`.
4. Try one hot-path candidate at a time.
5. Keep it only if it clears the retention gate in PERF_RESEARCH.md "Gates
   For Any Candidate": one-sided 95% lower bound above the ~0.6% attribution
   floor on the paired instruments at either target (20T sustained is
   primary), and no demonstrated regression beyond 0.5% at the other.
   Decide on the drift-adjusted Thue-Morse fit and the paired micro couples,
   never on raw medians.

Plausible transferable ideas include flatter tables, branch reduction, and
measured loop unrolling. Regex prefilters and skip-ahead strategies are not
directly applicable unless a proof and differential tests show identical
AstroBWT output.

Optional race-check tooling:

```powershell
racedetector test ./...
```

Keep `racedetector` external. Do not add it to `go.mod` unless the miner grows
a first-class CI workflow around it.
