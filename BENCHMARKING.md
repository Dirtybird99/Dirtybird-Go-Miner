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
a daemon:

```powershell
$env:GOMINER_FORCE_STATUS = "1"
go run . --statbench --secs 90 -t 20 --pin --high
Remove-Item Env:GOMINER_FORCE_STATUS
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
