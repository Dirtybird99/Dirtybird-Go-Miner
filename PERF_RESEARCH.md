# Performance Research Notes

This note records the Kolkov/coregex repos reviewed for miner tuning. These
repos are external research inputs only. Do not vendor their code into the
miner without a separate license and correctness review.

## Source Provenance

| Source | Commit | Relevance |
|---|---|---|
| [kolkov/regex-bench](https://github.com/kolkov/regex-bench) | `68fb667312f47069d3167b2a2ca1bd8709e05115` | Modern cross-language regex benchmark discipline and coregex result framing. |
| [kolkov/regex-benchmark](https://github.com/kolkov/regex-benchmark) | `17d073ec864931546e2694783f6231e4696a9ed4` | Older Docker-based language benchmark; useful mainly as a caution about benchmark scope. |
| [kolkov/uawk](https://github.com/kolkov/uawk) | `97c7d564c77f7b1cd2c01555abb553a57cd04dc2` | Go VM using coregex; useful for strategy-gated fast paths and zero-CGO posture. |
| [kolkov/uawk-bench](https://github.com/kolkov/uawk-bench) | `150384da29f21288713a11c17e8d6713bfcd8309` | Benchmark runner shape: warmups, repeats, min/max/mean/median/stddev, CSV/JSON/Markdown output. |
| [kolkov/racedetector](https://github.com/kolkov/racedetector) | `b203ae801cb8285950693fc08455512db02fee4a` | Optional pure-Go race-check workflow for environments where CGO race builds are awkward. |
| [coregx/coregex](https://github.com/coregx/coregex) | `2812db759a501caae1bccbfb261701b6ddb57784` | Optimization notes: flat buffers, strategy selection, regression gates, scalar-vs-SIMD measurement. |
| [Tritonn204/tnn-miner](https://github.com/Tritonn204/tnn-miner) | web, 2026-07 | Reference-class C++ AstroBWTv3 miner. Vendors libdivsufsort; ships four op-loop kernels (`branch`/`lookup`/`avx2`/`wolf`) with runtime auto-tune. |
| [IlyaGrebnov/libsais](https://github.com/IlyaGrebnov/libsais) | web, 2026-07 | SA-IS SACA. Its edge over divsufsort is scalar: unrolling, software prefetch, branchless induction, MSB rank marking. |
| [Dismantling DivSufSort](https://arxiv.org/pdf/1710.01896) | arXiv | divsufsort sorts only B\*-suffixes then induces the rest; hot kernel is introspective multikey quicksort, **not** a radix sort. |

## Applicable Ideas

- Keep benchmark runs reproducible: fixed windows, warmup, repeat count,
  candidate labels, raw logs, metadata, CSV, and Markdown summaries.
- Rank by median throughput when there are repeats; preserve min/max/stddev so
  thermal drift and scheduler noise stay visible.
- Prefer fixed flat buffers and per-worker scratch over allocation-heavy helper
  structures in hot paths.
- Use strategy gates for special cases, but only after stats prove a frequent
  case exists.
- Benchmark scalar code against SIMD or unsafe candidates. Coregex documents
  cases where SIMD was slower because setup cost or false positives dominated.

## Rejected Or Non-Portable Ideas

- Regex skip-ahead, prefilter, reverse-suffix, and Teddy literal matching do
  not directly map to AstroBWT. AstroBWT must transform every byte
  deterministically; it cannot skip candidate regions the way a regex searcher
  can.
- Direct coregex integration is irrelevant to the miner runtime because there
  is no regex workload in mining.
- The older `regex-benchmark` suite includes process/language comparison
  concerns that are useful for benchmark humility, but not for v114 suffix-array
  implementation.
- Prior local hot-path attempts remain rejected unless new stats contradict
  them: fixed small group specialization, 12-bit radix sort, and unsafe 8-byte
  suffix compare loads all regressed. The arXiv divsufsort teardown independently
  explains the radix result: the reference hot kernel is a comparison sort
  (introspective multikey quicksort), not a radix sort.

### Lookup-table op kernel (`-tags lut`) — measured, rejected on Raptor Lake

tnn-miner ships a `lookup` kernel for the branchy op loop and auto-tunes against
`branch`/`avx2`/`wolf` per CPU, so we built the pure-Go equivalent: 149 of the 256
ops depend only on `step_3[i]`, so each 4-op dependent chain collapses to one load
from a 149x256 (~37KB) table. Code is retained behind `-tags lut`; the untagged
hash hot path is unaffected (`go tool objdump -s astroBWTv3Stream` shows zero
`opLUT`/`opRow` references — the `const useLUT = false` branch is fully eliminated).

Correctness held: KAT vectors pass and 10,000 random inputs across both backends are
byte-identical to `internal/refpow`.

Speed did not. Pinned, single-threaded, `-count=7`, medians of `BenchmarkHashV114`:

| core | branch (base) | lut | delta |
|---|---|---|---|
| P-core (Raptor Cove, affinity `0x1`) | 598,746 ns | 632,431 ns | **-5.63%** |
| E-core (Gracemont, affinity `0x10000`) | 875,214 ns | 893,474 ns | **-2.09%** |

Why the a-priori uop argument was wrong: the four byte-ops are ALU work that Go
compiles tightly (`POPCNT`, shift-or rotates), and the core retires ALU uops faster
than the two loads/cycle the L1 ports allow. The LUT converts ALU-bound work into
load-port-bound work and adds ~37KB of cache pressure against the 64KB stage buffer,
so it loses on both core types. An unpinned `-count=5` run showed a misleading +1.73%;
that was core migration on the hybrid CPU, not a win. **Always pin before judging.**

This does not refute tnn-miner: it auto-tunes precisely because no kernel wins
everywhere. The tag is kept so the kernel can be re-measured on AMD, where the
`lookup` path may pay off. Regenerate tables with `go generate ./internal/astrobwt`.

## Ranked Miner Backlog

1. Use `-tags v114stats` to measure v114 group-count and equal-key merge
   distributions under real sustained runs.
2. If literal equal-key groups above the current `<=32` fast path are frequent,
   benchmark threshold variants before changing production code.
3. Revisit the stage-4 short-run cutoff near `stage4ShortRunMax = 25` only with
   a median microbench improvement and sustained `20 --pin --high`
   confirmation.
4. Add an optional `racedetector` smoke note only as a safety workflow; do not
   put it in `go.mod`.
5. Consider assembly only after a profile shows a byte-search or bulk-copy loop
   with enough work to amortize call/setup cost.
6. ~~Port libsais's scalar tricks into the v114 induction loops.~~ **Mostly does not
   apply** (assessed 2026-07-09): libsais's software prefetch exists to hide *random
   bucket scatter* in generic induced sorting over a large SA. v114 is structure-aware —
   `emitFullGroupRunGeneric` walks `pos` backwards one byte per column over a working set
   of `gc` (~4) uint32s, a sequential access the hardware prefetcher already covers, so
   there is nothing to prefetch. Branchless induction was already a measured discard, and
   the induction re-sort is an insertion sort over ~4 elements (nothing to unroll). Only
   MSB rank marking is untried, and v114 has no rank array. Do **not** swap the SA
   algorithm either: tnn-miner vendors libdivsufsort, the same family as v114, and
   SA-IS/GSACA/CaPS-SA wins are all large-input or multi-threaded regimes.
7. The honest remaining SA target is `writeFusedRunsToSA` (48.4% cum). Its three costs are
   the radix sort (restructure already dead, ledger rows 13-14), the arena `memmove`
   (specialization dead twice, above), and the group scan. A win here needs to *remove*
   work — e.g. emit records already in SA order so the final copy disappears — not to
   micro-tune the copy.

### Where the time actually goes (1T CPU profile, `BenchmarkHashV114`, 3000x)

Flat: `blockIntelSha` 20.5% | `writeFusedRunsToSA` 14.9% | `radixSortRunsByStoredKey` 14.0%
| `emitFullGroupRunGeneric` 13.5% | `runtime.memmove` 13.5% | RC4 `XORKeyStream` 4.7%.
Cumulative: `writeFusedRunsToSA` **48.4%** — the dominant SA component, ahead of the emit
stage (22.8%). `memmove` splits 220ms under `writeFusedRunsToSA` (line 300's arena copy,
11.6% of the hash) and 70ms under `appendOrderGroup` (3.3%).

### Small-copy specialization in the stage-5 writer — re-measured, rejected (again)

Line 300's `copy(saU32[outPos:], arena[begin:begin+count])` has `count >= 2` and typically
2-4, so `runtime.memmove` call overhead dominates the words moved. Replacing it with a
`switch count { case 2,3,4: explicit stores; default: copy }` (explicit stores, because the
compiler rewrites a range-copy loop back into `memmove`) **failed the gate**. Pinned P-core,
interleaved base/cand/cand/base, `-count=6` per leg, n=12 per arm:

| | base | cand |
|---|---|---|
| median | 630,033 ns | 654,937 ns |
| min | 589,884 ns | 608,199 ns |
| CoV | 7.86% | 10.41% |

Point estimate **-3.95%**. Bootstrap 95% CI on the relative delta is
**[-15.28%, +0.50%]** — it *includes zero*, and Mann-Whitney gives p=0.094 (Cliff's
d=+0.40, medium). So this single run does **not**, on its own, prove a regression. It does
decisively fail the **+2% gate**: the optimistic end of the CI is +0.50%, well short of +2%.
Direction and effect size agree with three prior independent confirmations of the same idea,
making this the 4th; the stage-5 writer is branch-sensitive
and an unpredictable branch on `count` costs more than the memmove call it removes.
Do not retry without a different mechanism (eliminate the copy — e.g. emit
records already in SA order — rather than specialize it). If it is ever revisited, use
n >= 20 per arm: at CoV ~8-10% this design cannot resolve a 2% effect.

### Native-order radix keys — measured, kept

The profile attributed 1.3-1.5% of total hash time to `radixOrderKey`, which
byte-swapped every 24-bit stage-5 key before sorting. The swap is unnecessary:
records now retain the native little-endian key and the three stable radix
passes run byte 2, byte 1, then byte 0. Equality grouping is unchanged.

Pinned P-core ABBA (`GOAMD64=v3`, `default.pgo`, 12 samples per arm) measured
608,597 ns/op baseline vs 594,757 ns/op candidate: **+2.33% throughput**, with
0 B/op and 0 allocs/op. The 20-thread-only sustained ABBA block
(`45s`, `--pin --high`, 20s cooldown) measured 18.735 vs 18.941 KH/s median:
**+1.10%**. The sustained sample is small (two legs per arm), but both the
micro and 20-thread point estimates are positive.

Evidence: the protocol and raw sustained readings are recorded in
[BENCHMARKING.md](BENCHMARKING.md).

The adjacent split-SHA experiment was rejected. Using `crypto/sha256` for the
large final buffer while retaining Minio for the 48-byte prologue reduced the
combined result to only +1.03% vs baseline; its second ABBA leg was effectively
flat. The component SHA benchmark did not transfer to the integrated hash, so
the change was reverted.

## 2026-08-03 Rust/Zig parity audit

The benchmark workload now matches the active Rust/Zig harnesses byte-for-byte.
An aligned x2 stage comparison on the i7-13700HX explains nearly the whole gap:

| Stage | Go x2 | Zig x2 |
|---|---:|---:|
| setup/prologue | ~1.6 us/hash | 1.4 us/hash |
| wolf operation loop | ~46.1 us/hash | 37.6 us/hash |
| descriptor emit + radix + materialize | **~433.7 us/hash** | **325.1 us/hash** |
| final SHA-256 x2 | ~93.3 us/hash | 97.4 us/hash |
| total | ~574.7 us/hash | 461.5 us/hash |

Go's microsecond figures are inferred from its RDTSC stage shares and measured
1.74 KH/s wall rate; Zig reports instrumented wall time directly. The important
result is robust to that conversion: Go's existing x2 SHA is already at parity,
while descriptor-SA work accounts for about 109 us/hash of a roughly 113
us/hash total deficit. A new SHA kernel cannot close this gap.

The current local pure-Zig builder uses an all-arena packed-run layout and
pdqsort for collisions; the current Rust builder mirrors that broad shape and
benefits from its standard stable slice sort. Neither property transfers as an
automatic Go win:

| Candidate | Result | Decision |
|---|---:|---|
| Full current Rust/Zig all-arena materializer port | -9.3% at pinned/high 1T | Reverted. Go's literal/merge special cases are cheaper under the Go compiler. |
| `slices.SortFunc` only for group-runs above 25 | +0.46% in the clean pair | Reverted below the +2% gate. |
| Unconditional eight-word current-layout arena copy | +1.47% center, paired +1.61%/+1.33% | Reverted below the +2% gate. |
| Per-worker HIGHEST priority + execution-throttle opt-out under `--high` | +0.48% at 1T; +0.09% at 20T | Reverted as neutral. |
| Fresh Go 1.26.5 PGO profile (60s, pinned/high 20T x1) | +0.73% at 1T x2; +0.17% at 20T x1 | Rejected; committed `default.pgo` retained. |

All hash-path candidates passed V114/SAIS differential, KAT, fallback, and
zero-allocation gates before timing. The materializer port and copy candidate
also passed focused checkptr validation. The small positive copy result is not
rounded into a win: the retention rule was set before testing at +2% with no
more than 0.5% regression at the other target.

The fresh-PGO gate used 45-second actual-elapsed legs, 20-second cooldowns,
and B-C-C-B order at both targets. Paired deltas straddled zero (1T:
-1.42%/+2.96%; 20T: -0.85%/+1.20%), which is exactly the noise pattern the
predeclared median/target gate is intended to reject.

## 2026-08-05 SA-stage campaign — calibration and premise checks

Campaign plan: stats-first, then a structural rewrite of the stage-5
materializer ("scatter positions, not records"). Before any implementation,
three premise checks ran on the `perf/sa-campaign` branch.

### Stage-bracket coverage verified (the 109 µs deficit is real)

The 2026-08-03 stage table inferred Go's per-stage microseconds from RDTSC
shares times a wall rate, which cannot reveal unbracketed time on its own. The
check: divide summed bracketed cycles/hash by the same run's wall ns/hash and
compare the implied TSC rate across two unrelated regimes.

| leg | cyc/hash (sum) | wall ns/hash-thread | implied TSC |
|---|---:|---:|---:|
| 1T x2, 120 s | 1,290,276 | 560,067 | **2.304 GHz** |
| 20T x1, 300 s | 2,476,408 | 1,075,269 | **2.303 GHz** |

Agreement to 0.04% across regimes with different SHA shares and contention
means the brackets cover essentially all wall time (any fixed unbracketed
fraction would have to scale identically in both regimes to fake this). The
descriptor-SA deficit premise stands. Stage shares this run: 20T x1 SA 77.6% /
SHA 16.1% / wolf 6.1%; 1T x2 SA 75.1% / SHA 16.6% / wolf 8.1%.

### v114 descriptor distributions under sustained load (backlog 1 done)

20T x1, 300 s, 5,581,504 hashes (1T x2 leg agrees on every share to two
decimals — positive control across different nonce streams):

- Template runs: 61.8/hash. Group-size shares: 1 g 26.7%, 2 g 17.2%, 3 g 12.5%,
  4 g 9.6%, 5-8 g 21.0%, 9-16 g 10.9%, **17-25 g 1.81%, 26+ g 0.29%**.
- Equal-key merge groups per hash: literal 2-4: 197.4, literal 5-8: 20.4,
  literal 9-16: 10.8, literal 17-32: 13.4, two-run: 79.4, large fallback: 9.6.
  Total ~331 collision groups/hash involving ~1,300 records — small against
  the ~45k records/hash estimate, so a fixup-style materializer pays its
  collision cost rarely.
- v114 fallback hashes: 0 in both legs.

**Backlog 2 CLOSED (population too small):** all-literal groups above the <=32
fast path are bounded above by large-fallback merges = 9.6/hash, below the
pre-registered >=18/hash trigger. No threshold candidate.

**Backlog 3 TRIGGERED:** runs of 17-25 groups are 1.81% of template runs,
above the pre-registered 1% trigger (26+ adds 0.29%). A `stage4ShortRunMax`
variant A/B (16/20/32/40) is owed; expectation stays modest — the column-255
sort is a small fraction of a run's emit work.

### Measurement instrument calibrated (A/A with a layout-null arm)

Micro, 20 alternating (base, layout-null) couples, each invocation a
pre-built test binary pinned by process affinity 0x1 at High priority:

- Within-couple pairing collapses the old 8-10% CoV to a ~0.15% standard
  error on the mean effect — the historical CoV was unpaired pooling plus
  rebuild/migration noise, not hashing noise.
- The semantically-null layout change measures **+0.28% [-0.03%, +0.58%]**:
  the layout-noise floor on this box/toolchain. Micro effects below ~0.6%
  cannot be attributed to code semantics; the +2% gate keeps 3-6x margin.

Sustained A/A (8-leg Thue-Morse, 240 s legs, 20T, steady-state window):
null effect **+0.275% ± 0.26 pp, 95% CI [-0.45%, +1.00%]**, one-sided lower
bound -0.28% — the instrument resolves the +2% gate with wide margin and
correctly rejects a null. The +0.275% point estimate reproduces the micro's
layout floor on an independent instrument; linear thermal drift -0.37%/leg
confirmed, quadratic negligible. Run: `bench-results/thue-morse/`
20260805-161156-aa-20t. See `scripts/bench-thue-morse.ps1` +
`scripts/analyze-thue-morse.py` for the design (quadratic-drift-balanced
order, steady-state [120 s, leg-end] window, drift-adjusted fit with 4
residual df) and `scripts/bench-micro-couples.ps1` for the paired micro
screen. Retention rule for this campaign: point >= +2%
at 20T sustained AND one-sided 95% lower bound > 0 there AND no demonstrated
regression beyond -0.5% at the secondary target.

### Ceiling probes (backlog 7 go/no-go) — GO

Two deliberately-wrong builds bound what a scatter-style materializer can
remove before any real implementation. Both were verified live by the
positive control (`TestV114DifferentialVsSAIS` FAILS on both), built with
`GOAMD64=v3` + `default.pgo`, and measured with 20 pinned P-core couples
against the same base binary:

| probe | removes | effect vs base |
|---|---|---:|
| B: flat-loop materializer, radix intact | group scan incl. all merges | **+12.61% [+12.05%, +13.17%]** |
| A: flat loop AND no pass 3/swap | scan + pass-3 record scatter | **+17.15% [+16.71%, +17.59%]** |

Attribution: scan side ~12.6%, pass-3 side ~4.5%, additive to within noise.
Both tripwires cleared (below the +20% "measurement is wrong" bound — the
theoretical ceiling was ~+19.6% — and far above the +1% "stage is
memmove-bound" kill signal). A real fused-scatter must keep the equal-key
merges (~8% of hash by the old profile's cumulative arithmetic) and pay
bucket-cursor bookkeeping (~2-3%), so the realistic candidate expectation is
**~+6-7% at 1T micro**, with the memory-traffic mechanism arguing for at
least as much at 20T. Probes were working-tree-only and are fully reverted;
this table is their record.

### Emit slice-header hoist — measured, rejected

The singleton append in `emitFullGroupRunGeneric` reloads the full three-word
`v.runs` header from memory per record (`-gcflags=-S` shows the ptr/len/cap
loads; the `appendOrderGroup` call in the other branch forces the round trip
at the loop merge point). Hoisting local `runs`/`arena` slices through the
column loop — threading them through `appendOrderGroup` and writing back on
success only — measured **-0.38% [-0.98%, +0.23%]** over 20 pinned P-core
couples. The reload is a same-address store-forwarded L1 hit costing ~1-2
cycles, and carrying two extra live slice headers through the
register-hungry induction re-sort costs at least as much back. The effect
sits inside the ±0.3% layout floor: a null, not a win. Reverted.

## Closed Questions

- *Is there a faster SACA the other miners know about?* No. tnn-miner — the fastest
  open AstroBWTv3 miner — vendors canonical libdivsufsort (`divsufsort.c`, `sssort.c`,
  `trsort.c`); it has no libsais. Same family as v114. SA gains must come from
  engineering, not algorithm swaps.
- *Would a lookup-table op kernel help?* No on Raptor Lake; see above.
- *AVX-512 multi-buffer SHA-256 (16-lane, minio/sha256-simd)?* Not on this host:
  Raptor Lake has no AVX-512. The "2x-interleaved SHA-NI is ~2x" figure is AMD-specific
  (Intel shows ~no gain); we already ship 2-way SHA-NI at ~1.3x, capped by Raptor Cove's
  single shared SHA port.
- *Zen4 scaled-index addressing penalty?* Cited from AMD's optimization guide; it is not
  evidence about Raptor Cove. Only relevant if we target AMD.

## Gates For Any Candidate

- `go test ./...`
- `go test -tags v114stats ./internal/astrobwt`
- `go test -run=^$ -bench='BenchmarkHash(V114|PairV114|SAIS)$' -benchmem -count=5 ./internal/astrobwt`
- `scripts\bench-matrix.ps1 -Candidate <name>` for sustained results.
- Keep a candidate only if it improves by at least 2% at either the 1T or 20T
  target and regresses by no more than 0.5% at the other target.
