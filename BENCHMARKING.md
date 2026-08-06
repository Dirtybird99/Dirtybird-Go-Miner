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
5. Keep it only if median `BenchmarkHashV114` improves by at least 2% and
   sustained `20 --pin --high` does not regress.

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
