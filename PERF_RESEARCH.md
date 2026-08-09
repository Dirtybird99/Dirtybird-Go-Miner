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

1. ~~Use `-tags v114stats` to measure v114 group-count and equal-key merge
   distributions under real sustained runs.~~ **DONE 2026-08-05** — see the
   campaign section below for the distributions.
2. ~~If literal equal-key groups above the current `<=32` fast path are
   frequent, benchmark threshold variants before changing production code.~~
   **CLOSED 2026-08-05: population too small** (all-literal >32 groups are
   bounded above by 9.6 large-fallback merges/hash, below the pre-registered
   18/hash trigger).
3. ~~Revisit the stage-4 short-run cutoff near `stage4ShortRunMax = 25`.~~
   **CLOSED 2026-08-05: all four pre-registered variants (16/20/32/40) are
   micro nulls** — best +0.13% [-0.56%, +0.83%] (df=11 criticals; the
   originally-quoted [-0.53%, +0.80%] used df=19), every CI excludes the +2%
   gate; the trigger fired (17-25-group runs are 1.81% of template runs) but
   the population's work share is too small to matter. Binary-distinctness
   positive control passed (all five arms hash differently). Keep 25.
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
7. ~~The honest remaining SA target is `writeFusedRunsToSA` (48.4% cum)...~~
   **CLOSED 2026-08-05.** The "emit in SA order" idea was implemented as a
   fused byte0-scatter materializer, passed every correctness gate, and was
   REJECTED: 1T null, 20T sustained -1.17% [-1.94%, -0.39%]. See the campaign
   section — the scan's apparent cost is the equal-key merges, which
   correctness requires, and the scatter loses the sequential SA write
   stream. The only remaining stage-5 surface is the merge comparator
   itself; nothing bookkeeping-shaped is left here.

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
  Total ~331 collision groups/hash involving ~1,500-1,700 records (bucket
  midpoints) — small against
  the ~45k records/hash estimate, so a fixup-style materializer pays its
  collision cost rarely.
- v114 fallback hashes: 0 in both legs.

**Backlog 2 CLOSED (population too small):** all-literal groups above the <=32
fast path are bounded above by large-fallback merges = 9.6/hash, below the
pre-registered >=18/hash trigger. No threshold candidate.

**Backlog 3 TRIGGERED:** runs of 17-25 groups are 1.81% of template runs,
above the pre-registered 1% trigger (26+ adds 0.29%). A `stage4ShortRunMax`
variant A/B (16/20/32/40) was owed (since run and CLOSED — all four null;
see the backlog list); expectation stayed modest — the column-255
sort is a small fraction of a run's emit work.

### Measurement instrument calibrated (A/A with a layout-null arm)

Micro, 20 alternating (base, layout-null) couples, each invocation a
pre-built test binary pinned by process affinity 0x1 at High priority:

- Within-couple pairing collapses the old 8-10% CoV to a ~0.15% standard
  error on the mean effect — the historical CoV was unpaired pooling plus
  rebuild/migration noise, not hashing noise.
- The semantically-null layout change measures **+0.28% [-0.03%, +0.58%]**:
  the attribution floor on this box/toolchain. Micro effects below ~0.6%
  cannot be attributed to code semantics; the +2% gate keeps 3-6x margin.
  CAVEAT (review): the campaign's couples ran base-then-cand every couple,
  which perfectly aliases a constant position effect (cold-core turbo,
  first-touch) with the arm effect — so the +0.28% may be layout, position,
  or both; historical runs cannot distinguish. The script now alternates arm
  order per couple, making the position component average out. Treat the
  floor as an attribution limit, not necessarily a layout property.

Sustained A/A (8-leg Thue-Morse, 240 s legs, 20T, steady-state window):
null effect **+0.275% ± 0.26 pp, 95% CI [-0.45%, +1.00%]**, one-sided lower
bound -0.28% — the instrument resolves the +2% gate with wide margin and
correctly rejects a null. The +0.275% point estimate is consistent with the
micro's floor (both CIs also cover zero — read as compatibility, not
replication). Drift is session-specific, not a box property: this session
fitted -0.37%/leg linear, while the candidate session two hours later fitted
+0.365%/leg — opposite sign, which is exactly why the design balances drift
rather than assuming its shape. Run: `bench-results/thue-morse/`
20260805-161156-aa-20t. See `scripts/bench-thue-morse.ps1` +
`scripts/analyze-thue-morse.py` for the design (quadratic-drift-balanced
order, steady-state window, drift-adjusted fit with 4 residual df) and
`scripts/bench-micro-couples.ps1` for the paired micro screen.
WINDOW ERRATUM (review): the recorded runs' steady-state window was
[90 s, leg-end], not the documented [120 s, leg-end] — the t=120 s
checkpoint's interval covers [90 s, 120 s] ramp and was included. Refits on
the strict window change no verdict (A/A +0.275% → +0.270%; the candidate
-1.171% → -1.087%, CI still excluding zero). The script now parses checkpoint
end times and excludes it.
TSC anchor (review, measured directly): this box's invariant TSC is
2.3040 GHz (38.4 MHz crystal x 60), so the implied-TSC agreement below is an
ABSOLUTE bound — unbracketed wall time is 0.009-0.041%, not merely equal
across regimes. Retention rule for this campaign: point >= +2%
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

Attribution in ONE unit space — work share removed, converted from the
speedups (share = 1 − 1/(1+s)): probe B removed **11.19%** of hash work,
probe A **14.64%**, so the pass-3 increment is **3.45%** by definition
(A − B is the increment, not an independence test — additivity was never
probed separately). The pre-registered tripwires were speedup-space numbers:
below the +20% "measurement is wrong" bound (the reconstructed work-share
ceiling, 14.9% flat + 14.0%/3 ≈ 19.6%, corresponds to a +24.3% speedup, so
the tripwire was in fact looser than it read) and far above the +1% kill
signal.

**Post-hoc correction (found in review):** the go/no-go arithmetic below
originally mixed speedup-space and work-share numbers, and used a "~8% of
hash" merge-cost prior from the old profile's cumulative arithmetic. That
prior is SUPERSEDED by this campaign's own measurement — the candidate's 1T
null against probe B implies the merges are ~11.2% of hash work, nearly the
whole scan-side removal. Computed consistently even on the optimistic 8%
prior, the candidate expectation was **~+0.5% to +1.5% at 1T** — borderline
against the +2% gate BEFORE implementation, not the "~+6-7%" recorded at the
time. The decision to implement was made on the inconsistent arithmetic; the
measured rejection below stands on its own regardless. Probes were
working-tree-only and are fully reverted; this table is their record.

### Fused-scatter materializer ("scatter positions, not records") — measured, REJECTED, backlog 7 closed

The full candidate was implemented (commit `1f64e23`, reverted in history —
kept for a possible AMD revisit): byte0 histogram accumulates position counts,
two radix passes instead of three, the third pass replaced by a materializing
scatter through 256 per-bucket cursors, equal-key collisions chain-linked and
repaired by a fixup pass reusing the existing merge helpers. Every correctness
gate passed first try: full suite, tagged suite + a fixup-branch coverage
test, `-race`, and the million-hash differential (0 mismatches, 0 fallbacks).

Measured against its parent commit (`GOAMD64=v3`, `default.pgo`):

| instrument | effect |
|---|---:|
| 1T micro, 20 couples, P-core | **-0.01% [-0.55%, +0.53%]** (null) |
| 1T micro, 12 couples, E-core | +0.13% [-0.18%, +0.44%] (null; df=11 criticals) |
| 1T micro, 12 couples, P-core, `-pgo=off` both arms | +0.37% [-0.55%, +1.29%] (null, df=11; the +0.38 pp shift vs the PGO pair has SE ~0.49 pp — not distinguishable from zero, and either way not load-bearing) |
| **20T sustained, 8-leg Thue-Morse, steady-state** | **-1.17% [-1.94%, -0.39%] — significant regression** |

**The attribution finding (why the +17% ceiling did not survive):** the
ceiling probes deleted the equal-key merges along with the scan; the real
candidate must keep them. The 1T null against probe B's +12.6% means the
scan's apparent cost was almost entirely the ~331 merge groups/hash (suffix
compares over wolf's highly repetitive output), not the bookkeeping around
them — the branch ladder, key compares, and record re-read are nearly free
next to them. And at 20T the scatter actively hurts: the old writer emits one
strictly sequential, hardware-prefetched SA write stream, while the scatter
turns that into 256-way scattered RFOs and the fixup adds copy-out traffic —
exactly the axis L3 contention punishes.

Consequences, all closed:
- **Backlog 7 is CLOSED.** The removable-looking stage-5 surface is merge
  work that correctness requires; bookkeeping restructures cannot pay.
- **B2 (collision-flag precompute) and B3 (offset-array split) are dead** by
  the same finding — both remove strictly less than the full scan removal
  that already measured null at 1T, and keep the regressive scatter (B3) or
  nothing (B2) at 20T.
- The only surface left in stage 5 is the **merge comparator itself**
  (~12% of hash across ~331 groups): any future candidate must cheapen
  suffix comparison, not descriptor bookkeeping.

### Campaign close-out notes

- **Underpowered-gate hypothesis for older ledger rows:** every pre-2026-08-05
  sustained verdict was produced by a 4-leg ABBA design whose null
  distribution (inferred — the campaign's A/A used the 8-leg design, whose
  measured SE bounds the ABBA design from below) spans roughly ±1 pp and
  whose CI on the repo's one KEPT candidate spanned zero. Some past
  "confident" dead ends may have been real sub-2% effects in either
  direction. Under the standing strict +2% gate this changes no decision,
  but do not cite old point estimates as precise magnitudes.
- **The "+1.47% unconditional eight-word arena copy" row** records no
  protocol and no site; the checkptr mention suggests the stage-5 sa copy
  (the unsafe alias side) rather than the emit-side arena append, but this
  could not be pinned down from the repo. It remains shelved under the
  user's strict-gate policy either way.
- **Campaign net result:** no perf candidate retained. The durable
  deliverables are the corrected comparator-aligned harness, the calibrated
  paired instruments (couple-based micro, Thue-Morse sustained, A/A
  layout-null floor ~0.3%), five backlog items closed with data, and the
  attribution finding that stage-5's remaining cost is merge-comparator
  work, not bookkeeping.

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

## 2026-08-06 merge-comparator campaign — no candidate retained, surface closed

The follow-on to the SA-stage campaign, against the one surface it left open:
the equal-key merge comparator. Structure: measure -> bound -> build, with
pre-registered kill rules at every step. All three candidates died by their
own rules; the campaign's product is the measured map of the surface.

### Step 1 — direct measurement (v114stats extension, zero untagged cost)

New tagged-only counters (compares per merge path and per literal bucket, an
overhead-calibrated RDTSC bracket over the multi-record merge arm, and a
low-entropy discriminator for large groups). A/A of the instrumented source
built UNTAGGED vs base: +0.01% [-0.73%, +0.76%] — the V114StatsEnabled
guards const-fold away. Sustained legs (20T x1 300 s; 1T x2 120 s):

- **Merge-branch share S = 12.6% of hash at 20T, 9.7% at 1T** (bracket
  cycles / anchored 2.304 GHz TSC). KILL X (S < 4%) passed with 3x margin —
  and the campaign-1 confound worry dissolves: the 11.2% subtraction was
  about right, and the share GROWS at 20T.
- Compares/hash: literal 3,383 + k-way 2,864 + two-run ~320 ≈ 6,600 — 1.5x
  the estimate, with the k-way path 3x larger than modeled (~298
  compares/group). (A first-run counter bug — the two-run flush was missing
  — was found by its own zero and fixed.)
- The 17-32 literal bucket runs 173 compares/group ≈ n²/4: inputs NOT
  near-sorted, so binary insertion was not auto-killed by sortedness.
- **Low-entropy hypothesis CONFIRMED overwhelmingly**: 99.98% of large
  literal groups have repeated-byte (vvv) keys, 96.6% are delta-1 position
  chains (q = 0.97), extreme-member LCP < 8 in 99.997% — the constant-run
  signature, with members clustered at run tails. K-way groups show the
  same shape (79% vvv, 67% contiguous).

### Step 2 — ceiling probes (wrong-by-design, positive controls failed as required)

| probe | replaces | effect (20 couples, P-core) |
|---|---|---:|
| C1: no memcmp fallthrough | the walk after an 8-byte tie | **+1.25% [+0.86%, +1.65%]** |
| C2: comparator -> `a < b` | everything | **+5.39% [+4.67%, +6.13%]** |

Comparator-only share C = 5.39/105.39 = **5.1% of hash**; the walks are
cheap and the **call/load overhead is +4.1 pp** of the +5.4 — per-call cost
dominates, as the structural analysis predicted. Kill-rule outcomes:
- KILL Y1 (C2 < +3%): passed.
- **Candidate #1 (inline-friendly comparator header): KILLED** — needs
  C >= 5.5%, measured 5.1%; the C2-C1 recompute puts its optimistic EV at
  the gate, not above it.
- **Candidate #3 (binary insertion >= 17): KILLED** — needs C >= 9%.
- **Candidate #2 (constant-run closed form): proceeded** — needs
  C >= 4.5/q = 4.66%, and q = 0.97 fired.

### Step 3 — constant-run closed form: correct, real, and TOO SMALL

Implemented (commit `5bddae4`, reverted in history): groups of 17-32 whose
shared key is one repeated byte partition into delta-1 chains; within a
maximal run of c ending at e, the first difference between two members
always pits c against data[e+1], so one byte test orders the whole chain
ascending (t > c) or descending (t < c, or run reaching logicalLen);
chains merge with real compares. Proof on the function; edge cases
unit-tested; 2,000-case fuzz against the production comparator; full
suite, tagged suite, -race, million-hash differential all green (0
mismatches, 0 fallbacks).

Measured, micro screen vs its parent commit:
- First run CONTAMINATED by my own protocol violation (launched seconds
  after the million-hash gate's 5.5-minute all-core burn, no cooldown):
  +0.98% [-0.40%, +2.37%] with the first three couples wildly out of
  family. Recorded, discarded, and the lesson appended below.
- Clean re-run after 3-minute cooldown (pre-declared as binding):
  **+1.29% [+0.67%, +1.92%], one-sided lower bound +0.78%** — a real
  effect, above the attribution floor, and BELOW the pre-registered
  promotion rule (point >= +1.5%). **KILLED.** Comparator work is
  compute-bound; campaign 1 established such wins do not grow at 20T, so
  a +1.3% 1T effect cannot plausibly clear the +2% sustained gate.

### Campaign verdict

The comparator surface is now CLOSED for this host/toolchain: total
elimination of the comparator is worth +5.4% at 1T, no implementable
candidate captures more than ~+1.3%, and the strict gate needs +2%. What
would reopen it: a materially different CPU (the AMD caveat from the LUT
kernel applies here too), a relaxed gate, or an emit-side change that
removes merge WORK rather than compare cost — the last of which is the
restructure class campaign 1 closed. Protocol lesson appended to the
methodology: **never start a measured leg immediately after a correctness
burn — cooldown first, always** (the contaminated screen above is the
in-repo example).

### 2026-08-06 amendment: gate relaxed by the user; closed form RETAINED

The user relaxed the retention policy (see Gates For Any Candidate) and
directed keeping the constant-run closed form. The candidate was restored
verbatim (revert of the revert — no hand edits to the gated content) and
re-passed the full gate list on the restored tree (suite, tagged suite,
-race, million-hash differential). The one open measurement, the 20T
sustained no-regression check, ran as an 8-leg Thue-Morse block (240 s
legs, cooldown before the block per the lesson above):

- Drift-adjusted treatment: **+3.04% ± 1.52 pp, 95% CI [-1.09%, +7.34%]**,
  one-sided lower bound -0.14%.
- Pre-declared retention rule — no demonstrated regression beyond -0.5%
  (point >= -0.5% AND CI upper bound not below -0.5%) — **SATISFIED**;
  combined with the proven micro effect (+1.29% [+0.67%, +1.92%]), the
  candidate is **RETAINED**.
- HONESTY CAVEAT: this block's SE (1.52 pp) is ~5x the instrument's
  calibrated noise — legs 6-7 (both base) sat >1 KH/s below family,
  indicating a mid-block disturbance. The block therefore establishes
  no-regression, NOT a confirmed 20T improvement; the +3.0% point estimate
  is directionally consistent with the micro win but unproven. A clean
  re-measurement on an idle box would tighten it; retention does not
  depend on it.

## 2026-08-09 AstroX equality-parity campaign

Starting point: clean `main` at
`ffeacc964a94452d1e42db093bc677580605c412`, Go 1.26.5,
`GOAMD64=v3`, committed `default.pgo`, Intel i7-13700HX. The candidate set
came from the four changes in Dirtybird-C-Miner commit `8e94918`: uniform
columns, cached two-run prefixes, already-sorted run reuse, and four-unique
materialization. Go already had sorted-run reuse and bottom-up merging, so
that item needed no port.

Instrumentation over a 1-thread production hash stream found generic columns
were overwhelmingly uniform: 73,691,023/80,177,408 3-byte key columns
(**91.91%**) and 74,945,801/79,864,215 predecessor columns (**93.84%**).
The retained Go-native path therefore checks the suffix-sorted endpoint keys,
emits one existing arena run when they match, and preserves the current order
when every predecessor byte matches. It adds no dependency, assembly, or
untagged instrumentation cost. A constant-column SA test covers 3, 8, and 26
groups against SAIS.

Pinned P-core micro screens used 20 alternating couples at 600 hashes/arm.
Each row compares only against its direct parent:

| Candidate | Effect and 95% CI | Decision |
|---|---:|---|
| In-loop uniform-column shortcut | **+3.608% [+2.637%, +4.589%]**, one-sided lower +2.805% | Retained |
| Cached active two-run prefixes | -0.177% [-0.708%, +0.358%] | Reverted |
| Four adjacent unique records | **-2.230% [-2.806%, -1.652%]** | Reverted |
| Precomputed portable 64-bit equality mask | +0.479% [-0.334%, +1.299%] | Reverted below the 0.6% attribution floor |
| Fresh 60-second 20T x1 PGO profile | +1.221% [-1.784%, +4.319%] | Rejected; `default.pgo` retained |

The binding 20-thread x1 test was an uninterrupted 8-leg, 240-second
Thue-Morse block after a discarded warm-up, with 20-second cooldowns. Base
steady legs were 18.5900, 18.6675, 18.7800, and 18.7825 KH/s (median
18.7238); candidate legs were 19.2750, 19.5850, 19.6325, and 19.7750 KH/s
(median 19.6088, **+4.727%**). The drift-adjusted treatment was
**+4.604%**, SE 0.584 pp, 95% CI **[+2.995%, +6.238%]**, one-sided lower
**+3.366%**. This clears both the attribution floor and the prior +2%
primary-target rule.

Frozen executable SHA-256 provenance: base
`C2341E5FC0ECC953B101F5976D6FD51460BEA7B51A699FA28333698D23CC6267`,
candidate `5B846791B7FEB108386B5E1C5079CBDBDF6199FDD2AF5BE59388B950D8942205`.
Raw local evidence is under ignored `bench-results/micro-couples/20260809-*`
and `bench-results/thue-morse/20260809-143549-equality-final`.

### Cross-miner diagnostic after retention

Two balanced `Go-Rust-Zig-Zig-Rust-Go` blocks used 30-second legs,
20-second cooldowns, explicit pinned/HIGH scheduling, and checked each
miner's reported x1/x2 mode. These short blocks locate the remaining gap;
they are not promoted as official sustained rankings.

| Pipeline / threads | Go median | Rust median | Zig median |
|---|---:|---:|---:|
| x1 / 1T | 1.735 KH/s | 2.125 KH/s | 2.340 KH/s |
| x1 / 20T | 20.290 KH/s | 24.475 KH/s | 25.525 KH/s |
| x2 / 1T | 1.850 KH/s | 2.215 KH/s | 2.465 KH/s |
| x2 / 20T | 20.885 KH/s | 25.590 KH/s | 26.385 KH/s |

x2 was the best measured mode for all three. Go remains about **18.4%**
behind Rust and **20.8%** behind Zig at 20T, so the retained improvement is
real but does not establish parity. The harness initially failed closed
because current Rust reports `pipeline=1way|2way`; it now normalizes those
spellings to x1/x2, with one-second smoke blocks proving both modes.

The exact Dirtybird-C-Miner `8e94918` PGO artifact was also run through its
own deterministic trainer at 20 threads: 20-second warm-up, 120.082 measured
seconds, 3,296,452 hashes, **27.452 KH/s**. This confirms the C all-time-high
direction on this host and exceeds the commit message's 27.119 KH/s median,
but it is not placed in the matched table because the C trainer is a different
harness. Raw cross-miner evidence is under ignored
`bench-results/head-to-head-{x1,x2}-final`.

### 2026-08-09 x2 parity continuation

The next campaign retained five direct-parent improvements on the same
i7-13700HX/Go 1.26.5/`GOAMD64=v3` stack: fixed group origins (+3.254% micro),
unconditional short-run 32-byte copies (+1.835%), AVX2 origin materialization
(+2.505%), AVX2 uniform-column detection (+2.051%), and an inlined common
equal-column append (+1.045%). The last item produced a four-leg 20T x2
B-C-C-B steady median of 24.500 versus 23.795 KH/s (+2.96%).

An amd64 kernel that consumes whole stretches of unique literal/short-arena
runs then improved x2 micro by **+1.903%**, 95% CI **[+0.867%, +2.949%]**,
one-sided lower **+1.046%**. Its pre-bounds-check prototype produced a
four-leg steady median of 24.940 versus 24.155 KH/s (+3.25%); the final safe
kernel still needs the campaign's long exact-binary sustained block before
that 20T estimate is promoted. Normal and `v114stats` suites, focused race,
`GOAMD64=v1`, Linux arm64, and Linux s390x gates pass. The retained local head
is `2da586c`; it has not been pushed or released because exact C parity is not
yet established.

The current best observed 20T x2 steady intervals are **24.81-25.07 KH/s**,
about 35% above the immutable v0.2.2 x1 baseline (18.45 KH/s) but still about
9% below the exact C artifact (27.452 KH/s). A post-change stage profile puts
66.91% in V114 SA construction: radix 18.01%, unique-run kernel 15.85%, and
generic emission 16.38% cumulative; x2 SHA is 20.65% and remains out of scope.

New dead ends, all reverted: fresh x2 PGO (+0.765%, lower +0.186%), raw-pointer
radix scatter (+0.235%, CI crossing zero), literal-pair specialization
(-0.931%), deferred origin materialization (-9.320%), and four-literal kernel
unrolling (-3.481%). Earlier stage-2 rejects also include all-arena retry
(-5.0% sustained), emit-time histograms (+0.57% sustained), 12-bit radix,
and variable-width copy branches. These results reinforce two host-specific
rules: contiguous materialized positions beat deferred gathers, and compact
assembly loops beat speculative unrolling.

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
- Retention (relaxed by the user, 2026-08-06; previously "at least 2% at
  either target"): keep a candidate that is provably positive at either
  target — one-sided 95% lower bound above the ~0.6% attribution floor on
  the paired instruments — provided the other target shows no demonstrated
  regression beyond 0.5% (point >= -0.5% and CI upper bound not below
  -0.5%). Correctness gates are unchanged and non-negotiable.
