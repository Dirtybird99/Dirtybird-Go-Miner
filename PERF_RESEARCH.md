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
kernel was then measured as part of the full Stage2 release candidate against
frozen main `67e661b` (`GOAMD64=v3`, committed `default.pgo`). The cumulative
x2 micro result over 20 alternating P-core couples was **+25.150%**, 95% CI
**[+24.348%, +25.958%]**, one-sided lower **+24.487%**.

The binding 20-thread x2 run used the full discarded-warmup, eight-leg
Thue-Morse protocol at 240 seconds/leg. Base steady legs were 19.6325,
19.7550, 19.8225, and 19.8425 KH/s (median 19.7888); candidate legs were
24.3575, 24.7500, 24.8225, and 24.8300 KH/s (median **24.7863**, +25.254%).
The drift-adjusted treatment was **+24.927%**, 95% CI **[+22.800%,
+27.090%]**, one-sided lower **+23.290%**. This decisively clears the relaxed
retention gate but remains about 9.7% below the separately measured C artifact,
so it is a proven Go release gain, not a C-parity claim.

Final review found that the AVX2 origin materializer rounds every call up to
an eight-lane load/store. The logical limits remain unchanged, but `order`
and `arena` now carry eight backing elements of padding, with a focused test at
both logical ends. Portable/v3 suites, `v114stats`, race, analyzer selftest,
arm64/s390x cross-compiles, release selftest, and 1,000,008 V114-vs-SAIS
executions all pass with zero mismatches and zero fallbacks. Frozen executable
SHA-256: base `C4D7AA20B96FB52C2F69C998DE42956ED4F5F8878125F5248AD03B70E190C083`,
candidate `F4B08A02F63B87B7526FE2A0CAEC332C0BDC55249CC1C532D799405BCD08FF97`.
Raw evidence is under ignored `bench-results/micro-couples/20260809-*` and
`bench-results/thue-morse/20260809-192028-v0.2.4-stage2-final-x2`.

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

## 2026-08-13 working-set campaign (kata-5)

Premise under test: the ~21% sustained gap to the Zig miner (24.79 vs 31.30
KH/s at 20T×120s) is memory pressure — this miner carried ~6.28 MiB of hot
scratch per thread (2× ScratchData + 2× v114Scratch) versus Zig's ~2.06 MiB,
and Zig documents its lane-shared SA scratch as a measured working-set halving
with a 12-16-thread break point.

**Change: share one `v114Scratch` across the two x2 lanes** (`hasher.go`; the
SA working scratch is lane-transient — every `buildSAv114` call rewrites it
before reading, and lane B starts only after lane A's suffix array is fully in
`sa_bytes`). Removes ~2.67 MiB/thread (~53 MiB at 20T). Also corrected the
stale `Hasher` comment that claimed the per-lane duplication mirrored the zig
miner — Zig duplicates Workers but SHARES its SA scratch.

Measured (both instruments, gates green incl. the full astrobwt suite):
- micro couples ×20 (`^BenchmarkHashPairV114$`, 600x, affinity 0x1):
  **+0.810%**, 95% CI [-0.169%, +1.797%], one-sided lower **+0.001%**.
- Thue-Morse 8-leg 240 s @ 20T: base median 24.7338, cand 24.7975 →
  **+0.258%** — below the ~0.6% attribution floor.

**Verdict: retained as a footprint/hygiene change, not a performance claim**
(positive sign on both instruments, no regression indication, halves pair-mode
scratch). **The working-set hypothesis for the Zig gap is REFUTED at this
size scale**: cutting 2.67 MiB/thread moved 20T by ~0.26%. A fresh 1T x2
datum (2.11 KH/s here vs ~2.56 derived for Zig) puts the gap at ~-17.6%
already at ONE thread — the deficit is predominantly load-independent
per-hash execution cost, not cache capacity. Right-sizing the merge vectors
(~1 MiB more) was skipped for the same reason; the cached-prefix retest
condition (slices winning) was not met.

### 2 MiB large pages under the v114 scratch — measured, KEPT

The capacity story being dead left TLB walk frequency — the mechanism the zig
miner actually cites (Gracemont's 48-entry L1 DTLB) and the one deployment
feature both faster siblings have that this miner lacked. `largePageAlloc`
(`largepage_windows.go`) enables `SeLockMemoryPrivilege` best-effort, lets
`VirtualAlloc(MEM_LARGE_PAGES)` adjudicate, and `newV114Scratch` carves its
eight integer-only backing arrays out of the region (64-byte aligned; slice
headers stay on the Go heap so the GC never sees the region; growth past a
capacity falls back to an ordinary heap append; ordinary allocation on any
failure). The sustained-bench summary line now prints `largepages=`.

- micro couples ×20 vs the shared-scratch base: **-0.158%**, 95% CI
  [-0.965%, +0.655%] — no 1T regression (one pinned thread barely exercises
  the page walkers).
- Thue-Morse 8-leg 240 s @ 20T vs the same base: median 24.7425 → **24.965
  KH/s, +0.899%**, every rank-paired leg positive (+0.76%…+1.62%).

Clears the relaxed retention gate at the 20T target with no demonstrated 1T
regression. Requires the "Lock pages in memory" user right; without it the
binary silently runs exactly as before.

**NOT shipped in v0.2.18 (2026-08-13):** `go vet`'s `unsafeptr` analyzer flags
the `uintptr`->`unsafe.Pointer` conversion of the `VirtualAlloc` return
(`largepage_windows.go:75`), which the CI test job runs. The +0.899% is real and
measured, but shipping needs a vet-clean allocator wrapper (or an asm stub that
returns `unsafe.Pointer`). Reverted the code from the ship branch; the finding
stands as a documented follow-up. What shipped is the scratch share (-53 MiB,
+0.26%) + the kata-7 comparator/merge BCE (+0.44%).

## 2026-08-13 profile-driven BCE campaign (kata-7) — gap LOCATED, ceiling measured

First actual pprof of the miner (`BenchmarkHashV114`, GOAMD64=v3, PGO): flat
share radix `radixSortRunsByStoredKey` 16.0%, `writeUniqueRunBatch` (asm) 15.2%,
`emitFullGroupRunGeneric` 11.5%, `compareSuffixesAfterKey`+inlined wrapper
~4.6% cum, `mergeSortedPositionsAfterKey` 2.3%. The gap is DIFFUSE — no smoking
gun; each pure-Go stage runs a few % behind Zig's LLVM output. A parallel deep-
research pass (ByteDance TangoLLVM) independently put the gc-vs-LLVM floor at
~5-9% on analogous pointer-chasing/integer code. Bounds-check enum
(`-d=ssa/check_bce/debug=1`) confirmed the surviving checks; `suffixLessAfterKey`
is already PGO-inlined.

Attacked the located BCE levers, each byte-exact (1,000,008-hash differential,
0 fallbacks; full suite + zero-alloc green):

- **Comparator raw-pointer BE-u64 loads** (`compareSuffixesAfterKey`: replace
  the per-call `v.data[a+3:]`/`v.data[b+3:]` slice headers + `bytes.Compare`
  hot path with `bits.ReverseBytes64(*(*uint64)(unsafe.Add(dp,off)))`) —
  **+0.947% micro** (CI [+0.395,+1.503], one-sided lower +0.491). The one real
  lever; reopened on the relaxed gate (the old inline-comparator candidate was
  killed under the +2% gate).
- **k-way merge inner loop** (`mergeSortedPositionsAfterKey` raw-pointer
  reads/writes) — stacked; comparator+merge **+0.439% sustained** 20T Thue-Morse
  (24.75 → 24.86, positive but floor-adjacent). KEPT (byte-exact, positive).
- **Radix scatter raw-pointer writes** — **NULL** (full stack fell to +0.549%
  micro, CI [-0.604,+1.714] crossing zero). Confirms the prior ledger null:
  this loop is memory-latency-bound on the random scatter to ~20k positions, so
  the bounds check hides behind the cache miss. **REVERTED.**

**Measured ceiling (the honest answer to "genuine parity"):** BCE recovers ~0.4-
0.9% per lever and the levers are exhausted (memory-bound loops null; the two
biggest flat functions are asm or memory-stalled). kata-7 = ~+0.44% sustained;
with kata-5 the two campaigns total ~+1.4% (24.79 → ~25.1). **Pure-Go parity
with C++ (27.5) / Zig (31.3) is NOT reachable on this workload** — the residual
is a real gc-vs-LLVM codegen floor (~5-9%, corroborated four ways this session:
this pprof, TangoLLVM, archsimd comparator 1.59× slower, goat/clang-22 radix
1.5% slower in a fair harness) plus cache-miss latency in the radix that no
in-source rewrite touches. Closing it further needs hand-Go-asm on the inner
loops (won't help the memory stalls) or a fundamentally lower-work SA
decomposition than the whole miner family uses.

## 2026-08-19 Go 1.27 `simd` assessed; arm64 SA kernels ported (correctness only)

Go 1.27 shipped `simd` (portable, vector-size-agnostic) and `simd/archsimd`
(arch-specific, revised amd64 API, new arm64 Neon). Both need
`GOEXPERIMENT=simd` and neither is under the Go 1 compatibility promise.

**amd64: no surface left, and the question is closed.** The vector-shaped
kernels are already hand-written AVX2 (`sa_v114_equal_amd64_v3.s`,
`sa_v114_materialize_amd64_v3.s`); `archsimd` on the comparator already measured
1.59x slower; the radix scatter is memory-latency-bound with a twice-recorded
null; `writeUniqueRunBatch` is already asm; and Raptor Lake has no AVX-512, so
`VPCONFLICTD` and vector scatter are unavailable. Nothing was changed on amd64.

**The portable package cannot express `buildEqualColumns` at all.** Its mask
types expose only `And`, `Or`, `String`, and `ToIntNs` (`$GOROOT/src/simd/
simd_stubs.go`); there is no movemask/bitmask extraction and no
`AnyTrue`/`AllTrue`, so the `VPMOVMSKB` step has no portable spelling. Only
`materializeOrigins` is expressible. That asymmetry is what selected hand asm.

**arm64 was running the scalar path.** Both kernels were gated `!amd64.v3`, so
every `linux/arm64`, `android-arm64`, and `darwin/arm64` release shipped scalar
Go for work that is `+2.051%` and `+2.505%` on amd64. Ported both to hand-written
Neon, no `GOEXPERIMENT`, no `go.mod` bump:

- `sa_v114_materialize_arm64.s` — `VDUP` + two `VADD V.S4` per 32-byte block,
  `VLD1.P`/`VST1.P`, same 8-element round-up the amd64 kernel relies on (the
  `orderCap`/`arenaCap` `+8` padding already covers it).
- `sa_v114_equal_arm64.s` — 16 `VMOVI $255` accumulators, `VCMEQ`+`VAND` over
  eight 32-byte chunks per group, then per accumulator `VAND` with a
  `[1,2,4,...,128]x2` weight vector and three self-`VADDP` rounds, which leaves
  the 16-bit column mask in bytes 0-1; `VMOV V.S[0]` extracts it and four masks
  are `ORR`-packed per output word. Byte-identical layout to the amd64
  `VPMOVMSKB` path.
- `sa_v114_uniquerun_stub.go` split out so the `!amd64.v3` stub is shared
  instead of duplicated per architecture.

**Correctness (arm64 verified under `qemu-aarch64-static`, byte-exactness only):**
full `internal/astrobwt` suite green, including `TestV114DifferentialVsSAIS`,
`TestDifferentialVsReference`, `TestV114FallbackRate`, `TestZeroAllocs`,
`TestV114ZeroAllocsAfterWarmup`, and `TestV114MillionHashGate` at 20k hashes.
amd64 `v1` and `v3` suites and the `v114stats` build unchanged and green.

New `sa_v114_kernel_diff_test.go` gates both kernels against an in-test scalar
reference: randomized over alphabets 1/2/3/16/256 and groupCounts 0-256, a
one-differing-column-at-a-time sweep across all 256 columns, and a
write-past-padding guard. It runs on every architecture, so it also re-validates
the AVX2 kernels. **Positive controls:** `VADD`->`VSUB` in the materialize kernel
and a one-bit shift error (`R6<<32` -> `R6<<33`) in the equal-columns packing
both fail these tests on arm64. `TestV114FastPaths` also caught both, but it
exercises a single fixed 3-group pattern, so it is not a sufficient gate for a
16-accumulator kernel on its own.

`.github/workflows/arm64-bench.yml` gained the four kernel gates (always) and
the million-hash differential (`mode: smoke` only, so ~1M paired hashes never
run immediately before a measured leg on a shared runner).

**PERFORMANCE IS UNMEASURED.** qemu is correct for byte-exactness and worthless
for throughput, which is exactly why `arm64-bench.yml` exists. No arm64 hashrate
claim is made here and none should be quoted until that workflow has been
dispatched against this change. The amd64 numbers above are the *motivation* for
expecting a win, not evidence of one on Neon: `VPMOVMSKB` has no
single-instruction Neon equivalent, so the equal-columns kernel in particular may
land below its `+2.051%` amd64 figure.

Two structural reasons to expect less than the amd64 figures, stated before
measuring so a small number is not talked up afterwards. `VPMOVMSKB` has no
single-instruction Neon equivalent (the weight-AND plus three `VADDP` rounds
replace one instruction), and Neon is 128-bit against AVX2's 256-bit, so both
kernels do twice the loop iterations per byte. `materializeOrigins` is the more
exposed of the two: its hot call site is gated on `groupCount >= 4`
(`sa_v114_emit.go:198`) while 1-3 group runs are 56.4% of the population, and
the counts that do reach it are typically 4-8 -- a single Neon iteration, which
is precisely where a 128-bit unit gives up the amd64 kernel's advantage. A null
on Kernel A would be an unsurprising result, not a bug.

## 2026-08-19 Go 1.27 amd64 re-probe -- three nulls, and the comparator explained

Test-only probes under `GOEXPERIMENT=simd`, gated `//go:build goexperiment.simd
&& amd64` in `sa_v114_simdprobe_amd64_test.go`, `sa_v114_cmpprobe_amd64_test.go`,
and `sa_v114_lcpstats_amd64_test.go`. No production file, no `go.mod` bump, no CI
change: `simd` is absent from `$GOROOT/api/*.txt` so vet's `stdversion` never
fires, and the import compiles with the directive still at `go 1.25.0`. Negative
control: with `GOEXPERIMENT` unset `go list` reports 0 of the 3 files, and the
`v1`/`v3` suites are unchanged. Every probe carries an equality gate against the
shipping kernel or `referenceEqualColumns`; all green.

**Structural finding: the portable `simd` package dispatches per call.** Each
function using it compiles to four clones -- `@simd0`, `@simd128`, `@simd256`,
`@simd512` -- behind a trampoline that loads `simd.maxVectorSize`, compares
against 0x80/0x100/0x200, and **CALLs** the clone (`go tool objdump` on
`probeMaterializeSIMD`). It is not inlined and not devirtualized. `archsimd`
emits one direct symbol with no dispatch. That difference alone disqualifies the
portable package for small hot kernels.

**Probe 1, `materializeOrigins` under portable `simd`** -- median ns/op,
`GOMAXPROCS=1`, `-benchtime=3000000x -count=10`, judged counts 4/5/8:

| n | asm | portable simd | scalar |
|---|---|---|---|
| 4 | 1.74 | 3.03 | 3.75 |
| 5 | 1.55 | 2.89 | 3.16 |
| 8 | 1.65 | 3.04 | 4.23 |
| 512 (diag) | 19.4 | 36.0 | 104.3 |

**~1.8x slower than the asm at every size.** It does beat scalar, so it would be
the right tool on an architecture with no hand kernel, but the gap persists at
n=512, so it is not only the trampoline.

**Probe 2, `buildEqualColumns` under `archsimd`** -- judged group counts:

| groups | asm | archsimd |
|---|---|---|
| 3 | 6.22 | 6.28 |
| 4 | 8.44 | 8.71 |
| 5 | 9.82 | 10.82 |
| 8 | 16.18 | 18.44 |
| 64 (diag) | 119.7 | 156.2 |

Within 1-14% at the judged sizes and never faster. **Methodology warning for
anyone re-running this:** the first revision held the eight mask accumulators in
a `[8]archsimd.Mask8x32` array and measured **3.6x slower**. archsimd's own doc
warns against putting vector types in aggregates; eight named variables
recovered the entire gap. That first number was a bug in the probe, not a fact
about archsimd.

**Probe 3, the comparator -- the pre-registered judged arm was WRONG, and
measuring it is what produced the answer.** The arm was first set to common
prefixes {0,2,7,15} on the reading that "the walks are cheap". New
`TestMeasureComparatorPrefixLengths` measured the actual population instead:
100,557,233 equal-key adjacent suffix-array pairs drawn from 2000 real hashes
via `astroBWTv3Stream`.

**This is a proxy, not the comparator's exact call population.** It counts every
adjacent SA pair sharing a 3-byte key -- roughly 50k per hash -- while `:246-256`
puts the merge comparator at ~331 groups over ~1500-1700 records per hash, and
`constantRunOrder` short-circuits 99.98% of large literal groups without calling
it at all. So the sample is ~30x broader and over-represents exactly the
repeated-byte runs the closed form removes. The null is insensitive to any
reweighting because the kernel wins or ties at **both** ends of the sweep
(1.72-1.84 vs archsimd 3.31-3.40 below 8 bytes, and 2.6x at 4096).

| common prefix after the 3-byte key | share |
|---|---|
| <8 (the BE-u64 word resolves) | 7.50% |
| 8-31 | 17.21% |
| 32-63 | 19.36% |
| 64-255 | 53.42% |
| >=256 | 2.51% |

Mean **96.15 bytes**, and **75.29%** are long enough for a 32-byte scan to
reach. The judged arm was moved to {32,64,96,128} on this evidence before
re-benchmarking. Two arms were tried: a pure archsimd single-pass scan, and a
hybrid keeping the shipping big-endian word and replacing only the
`bytes.Compare` tail.

| common | kernel | hybrid | archsimd |
|---|---|---|---|
| 32 | 4.74 | 4.53 | 4.59 |
| 64 | 5.61 | 5.91 | 6.08 |
| 96 | 5.33 | 6.88 | 8.85 |
| 128 | 6.61 | 7.62 | 8.11 |
| 256 (2nd) | 7.56 | 11.64 | 12.88 |
| 4096 (diag) | 66.6 | 151.5 | 172.8 |

c64/c96/c128 were re-measured at `-benchtime=8000000x -count=21` after the first
pass looked non-monotonic; kernel c96 replicates tightly at 5.33 (range
4.95-5.65) and really is faster than kernel c64 at 5.61, presumably a size-class
branch inside the runtime's memcmp. Recorded as measured rather than smoothed.

Parity at c32-c64, the kernel **wins by 29% at c96 and 15% at c128** where the
distribution actually sits, and the kernel is **2.6x faster** once prefixes get
long. **Mechanism: the comparator was never a scalar loop.** Its tail is
`bytes.Compare`, which is the Go runtime's hand-tuned AVX2 memcmp with wide
unrolled strides; a straightforward 32-byte archsimd loop cannot approach it.
This also *explains* rather than contradicts `:466-470` -- the walks are long
but cheap precisely because the runtime already vectorises them, which is why
per-call overhead dominated the +5.4%.

**No escalation.** The pre-registered trigger was parity-or-better on the judged
arm; nothing met it, so no candidate was built and no sustained instrument was
run. The probes are kept as test-only files: they cost nothing in a normal build
and they are the evidence for the closed question below. They are **pinned to the
1.27 `archsimd` API**, which is outside the Go 1 compatibility promise and is
expected to move in 1.28, so the next person to set `GOEXPERIMENT=simd` should
expect to fix them up or delete them rather than trust that they still build.

## 2026-08-19 v114 scratch carved from one contiguous region -- micro NULL, kept

`newV114Scratch` allocated eight separate buffers with `make()`. It now
allocates ONE `[]byte` and carves all eight out of it (`sa_v114.go`,
`carveV114Scratch`). Construction allocations drop 8 -> 1; the per-hash path is
untouched.

**Why, given the null.** This is not a speed change, it is the prerequisite for
one, plus two defects fixed:

- The reverted large-page allocator (`8796f4a`) had **two divergent construction
  paths** -- a carve out of the VirtualAlloc region and eight `make()`s. CI never
  holds `SeLockMemoryPrivilege`, so only the `make()` branch ever ran and the
  carve's hand-counted byte offsets shipped with **zero test coverage**. One path
  means every CI leg now executes it.
- The region was deliberately never freed. Fine for a process-lifetime miner,
  wrong for `pkg/engine`: `Start` creates `Threads+1` Hashers and `Stop`
  (`engine.go:293-296`) releases none, so a pool-failover restart loop leaked
  ~2.8 MiB per thread per cycle with `MaxThreads = 255`. A GC-tracked `[]byte`
  dies with its Hasher.
- It **isolates contiguity from 2 MiB page size**. The +0.899% at
  `:691-719` conflated them; large pages are now a one-line swap of the
  `make([]byte, v114ScratchBytes)` over a carve that is already tested.

**Micro screen: +0.009%, 95% CI [-0.458%, +0.477%]**, one-sided lower bound
-0.377% (20 couples, affinity 0x1, `-pgo=default.pgo`, GOAMD64=v3,
`bench-results/micro-couples/20260819-203732-carve`). A null inside the ~0.6%
attribution floor, which is the expected and desired result -- contiguity alone
should not move a 1-thread number, and 1T barely exercises the page walkers.
`armsIdentical=False` confirmed before reading the number.

**Sustained 20T, 8-leg Thue-Morse, 240 s legs (run later the same evening at
5.7% mean idle load): +0.321%, 95% CI [-0.622%, +1.274%], one-sided 95% lower
bound -0.404%** (drift-adjusted fit, df=4,
`bench-results/thue-morse/20260819-212527-carve`). Also a null, and it does not
clear the +0.6% retention gate. Both instruments agree: contiguity alone is
worth nothing measurable, which is the expected result and leaves the 2 MiB
page size as the whole of the +0.899%.

**Read the medians and you get the wrong sign.** Raw medians were -0.208%; the
drift-adjusted fit is +0.321%. The box ramped **+2.84% from leg 1 to leg 8**
(24.6475 -> 25.3475 KH/s), an order of magnitude larger than the effect, and
leg 1 was a base leg. The Thue-Morse order keeps the treatment orthogonal to
the linear and quadratic drift terms, which is the entire point of using it --
`bench-thue-morse.ps1` prints medians with an explicit note not to decide on
them, and this run is a concrete example of why. Always run
`analyze-thue-morse.py`; its `--selftest` recovers a planted +3.045% at df=4.

**Safety, since a wrong suffix array is a wrong share.** Segment reservation goes
through a bounds-checked reslice `base[off:off+n]`, which validates the whole
extent -- strictly stronger than the prior art's `&base[off]`, which checked only
the first byte and would let `unsafe.Slice` fabricate a 283 KB segment over one
valid byte. Element counts are stated once (`u32(n)`/`run(n)` derive bytes), and
an end-of-carve equality assertion catches a future ninth buffer. Sizes come from
`unsafe.Sizeof`, not the old `(8+8+4+4+4+4)` literal. No `uintptr` appears, so
`unsafeptr` -- the analyzer that caused the revert -- has nothing to flag.

New `sa_v114_carve_test.go` (no build tags, every platform and CI leg): layout,
a tagged no-aliasing fill at full cap, and containment re-checked after 200 real
hashes -- the last asserts only order-independent properties because
`radixSortRunsByStoredKey` swaps `runs`/`radixTmp` and `mergeEqualKeyRuns` swaps
`runLens`/`nextLens`. **Positive controls:** reusing an offset, under-sizing the
budget by one cache line, and dropping the 64-byte rounding each fail these
tests; the aliasing test names the culprit
(`order[0] ... tag 1, clobbered by segment tag 8`).

Gates: vet; `go test` at `GOAMD64=v1` and `v3`; `-tags v114stats`;
`go test -race`; cross-builds for linux/darwin/android arm64 and the build-only
s390x leg; and the full arm64 suite under `qemu-aarch64-static` reporting
`V114 GATE: 20016/20000 hashes matched, 0 fallbacks`.

**One measured hypothesis retired.** The old "64-byte aligned" justification buys
nothing: over 200 allocations at `GOAMD64=v3`, seven of the eight buffers already
land on 4096-byte boundaries under plain `make()`, because they exceed 32 KiB and
come from Go's large-object allocator. Only `order` (2080 B) was a small object.
Do not spend a campaign on alignment.

## 2026-08-19 the release-path asm had never been vet-checked

`go vet ./...` passes in CI because `ci.yml:27` and `release.yml:63` run it with
**no `GOAMD64` set**, which excludes every `//go:build amd64.v3` file from the
build. `release.sh` ships `GOAMD64=v3`. So the two asm kernels that actually run
in release binaries were never analyzed. Running it surfaced three asmdecl
diagnostics in `sa_v114_materialize_amd64_v3.s`, all pre-existing on a clean
HEAD, all now fixed (`cceb34c`):

- `ret0+64(FP)` / `ret1+72(FP)` matched nothing: asmdecl names unnamed results
  `ret`, `ret1`, `ret2` (`appendComponentsRecursive`), never `ret0`. The offsets
  were right and `TestV114FastPaths` already asserted both values, so this was
  cosmetic. Results are now named `nextGroup`/`nextOut`.
- `VPBROADCASTD rel+20(FP)` read a 4-byte value into a 32-byte register;
  asmdecl checks operand size against the declared type and does not model
  broadcast. No stdlib `.s` broadcasts straight from `FP` -- the idiom loads
  through a GPR first, which is now what this does, at the cost of one hoisted
  move outside the loop.

Byte-exactness verified at `GOAMD64=v3` (`TestMaterializeOriginsMatchesReference`,
`materialize_padded_boundary`, the carve tests), with a positive control
(broadcasting `count` instead of `rel`) failing both.

**The generalisable finding is the gap, not the three fixes.** A vet invocation
that does not set the build's own `GOAMD64` cannot see that build's assembly.
Same shape as the two other blind spots found this session: the arm64 SA kernels
that no CI leg executed, and the large-page carve that no CI leg reached. If a
release build sets a flag, the analysis leg has to set it too.

## 2026-08-20 Go 1.27.0 toolchain (Q1 parity), PGO refresh under 1.27 (Q2 RETAINED), follow-on arms

Go 1.27.0 has been the local toolchain since 2026-08-18 while CI pinned 1.26.x
and release.yml pinned 1.26.5, so every release binary and every sustained
number before this entry is a 1.26.5 artifact. This entry moves the floor
(`go.mod` 1.25.0 -> 1.27.0, the five workflow pins, README/SECURITY/script.sh)
and measures what the move could change: the toolchain itself (Q1), the
profile it compiles against (Q2), and four opted-in follow-on arms (E1-E3,
E5; E4 was conditional on a Q1 regression that did not happen). Every block
was pre-registered in the vault (`02-projects/go-miner/go127-prereg-2026-08-20.md`)
before it ran, including the two replication rules below.

**What 1.27 changes for this code.** The release notes have nothing for a
zero-alloc, asm-adjacent hot loop: the size-specialized allocator never runs
per hash, `simd`/`archsimd` were re-probed null on 2026-08-19, and the hot
runtime primitives (`memmove_amd64.s`, `bytealg/compare_amd64.s`,
`equal_amd64.s`, `indexbyte_amd64.s`) are byte-identical between 1.26.6 and
1.27.0. The unlisted backend work is what could move it: new `known bits` and
`loop invariant` SSA passes, a generalized `loopbce`, the regalloc `regMask`
rewrite, CSE of loads across non-aliasing stores, and new generic/AMD64 rules
(carry/borrow via `SETB`, flags->bool->flags round-trip removal). Measured on
this package at v3 + the old `default.pgo`: hot-path text 9,589 -> 9,494
instructions over the 12 hot functions (`go tool objdump`); `astroBWTv3Stream`
-69 (the op kernel loses most of its `PUSHFQ` flag spills), `buildSAv114` -16,
`writeFusedRunsToSA` -15, `mergeEqualKeyRuns` -13, `tryWriteTwoRuns` **+17**
(LICM hoists two `ANDL` masks into spills; `-d=ssa/loop_invariant/off`
restores it and touches nothing else). `-d=ssa/check_bce/debug=1`: **400
remaining bounds checks under both toolchains, identical per file.** Inliner
budgets and PGO thresholds are unchanged; `-d=pgodebug=1` with the old
profile gives the same 19 hot functions, 12 hot-budget inlines and 2 hot-big
refusals (`hot-callsite-thres-from-CDF=0.0929`) under 1.26.5 and 1.27.0. Prior
for Q1, stated before measuring: |delta| <~ 0.5%, sign unknown. The go.mod
directive bump itself changes no codegen: the hot-function instruction
streams of `internal/astrobwt` test binaries built under `go 1.25.0` and
`go 1.27.0` are identical (9,830 instructions); only the main package's
`DefaultGODEBUG` string differs (the 1.25 set
`cryptocustomrand=1,tlssecpmlkem=0,urlstrictcolons=0` goes away; none of the
1.27 defaults touches a path this miner exercises).

**Why Q2 had a positive prior.** `default.pgo` was captured 2026-07-08 (122 s,
20T) and never regenerated. `appendOrderGroup` (5.44% flat / 8.34% cum) and
`radixOrderKey` (1.46%) no longer exist, so ~7% of its flat weight was
orphaned; it predated the AstroX campaign, so `constantRunOrder`,
`buildEqualColumns`, `materializeOrigins` and `writeUniqueRunBatch` carried no
weight; and `suffixLessAfterKey` was never inlined into its callers. Earlier
refreshes: 2026-08-03 +0.73%/1T, +0.17%/20T (4-leg 45 s protocol); 2026-08-09
+1.22% [-1.78, +4.32]; 2026-08-09 x2 +0.77%, LB +0.19%.

**Arms.** Source `f8f5ac2`, `go.mod` left at 1.25.0 for the Q1/Q2 arms,
`GOAMD64=v3`, `CGO_ENABLED=0`, `-trimpath -ldflags "-s -w"`; Q1 base built with
`GOTOOLCHAIN=go1.26.5` (the release pin), everything else go1.27.0. Fresh
profile: `gm_go1270_default.exe --sustained --secs 120 -t 20 --sa v114
--pair=false --pin --high --cpuprofile` (24.79 KH/s steady; sha256
`aad3ab999c134daa...`), the same shape as the old profile's provenance; a
merged 20T+1T variant was screened and not promoted. Every arm's `go version
-m` first line and sha256 are in `env.txt` of each run dir;
`armsIdentical=False` on every judged block, and a deliberate identical-arms
control block made the detector fire (`armsIdentical=True`). Layout-null A/A
arm = one unused `//go:noinline` func kept alive through a package var (an
unreferenced one is dead-stripped and yields a byte-identical binary).

**20T Thue-Morse (8 legs x 240 s + discarded warm-up, drift-adjusted fit, df=4):**

| block | point | SE | 95% CI | one-sided LB | drift |
|---|---:|---:|---|---:|---:|
| A/A layout-null | +0.220% | 0.50 pp | [-1.147%, +1.605%] | -0.831% | +0.39%/leg |
| Q1 go1.26.5 -> go1.27.0 | **+0.160%** | 0.16 pp | **[-0.292%, +0.614%]** | -0.187% | -0.98%/leg |
| Q2 default.pgo -> fresh, block 1 | -0.441% | 0.38 pp | [-1.492%, +0.622%] | -1.249% | +0.50%/leg |
| Q2 default.pgo -> fresh, block 2 (pre-registered replication) | -0.130% | 0.32 pp | [-1.009%, +0.755%] | -0.806% | +0.07%/leg |
| **Q2 pooled (inverse-variance, df=8)** | **-0.257%** | 0.24 pp | **[-0.818%, +0.306%]** | -0.710% | |
| E1 asyncpreemptoff=1 | -0.007% | 0.34 pp | [-0.945%, +0.941%] | -0.728% | +0.17%/leg |
| E2 large pages, block 1 | +0.460% | 0.63 pp | [-1.269%, +2.219%] | -0.871% | +0.34%/leg |
| E2 large pages, block 2 (pre-registered replication) | -0.133% | 0.24 pp | [-0.793%, +0.532%] | -0.640% | +0.40%/leg |
| **E2 pooled (inverse-variance, df=8)** | **-0.058%** | 0.22 pp | **[-0.571%, +0.458%]** | -0.472% | |
| E3 `-d=alignhot=0` | -0.316% | 0.12 pp | [-0.659%, +0.029%] | -0.580% | +0.60%/leg |

Steady rates 24.7-25.5 KH/s on every arm (the session's absolute level; not
comparable to other days). The A/A sits inside the instrument-OK band at the
upper edge of the SE range (0.5 pp).

**1T micro (20 couples, 600x; P-core 0x1 / E-core 0x10000):**

| block | point | 95% CI | one-sided LB |
|---|---:|---|---:|
| A/A layout-null, P (06:38 / post-idle 09:02) | +0.346% / +0.137% | [-0.565, +1.264] / [-0.432, +0.709] | -0.407% / -0.333% |
| Q1 go1.26.5 -> go1.27.0, P / E | +0.233% / +0.186% | [-0.789, +1.265] / [-0.189, +0.562] | -0.612% / -0.124% |
| **Q2 fresh vs default.pgo, P / E (post-idle)** | **+1.419% / +0.489%** | **[+0.667, +2.175] / [+0.104, +0.876]** | **+0.798% / +0.171%** |
| Q2 (06:47 contaminated window), P / E | +1.623% / +0.548% | [+0.193, +3.074] / [-0.358, +1.462] | screen only |
| Q2 merged 20T+1T profile, P | +1.335% | [+0.196, +2.487] | screen only |
| ref -pgo=off -> default.pgo / -> fresh, P | +1.042% / +0.671% | [-0.721, +2.837] / [-0.542, +1.900] | -0.417% / -0.332% |
| E2 large pages (vs `-tags nolargepages`), P / E | +0.888% / +0.200% | [+0.108, +1.674] / [-0.116, +0.516] | +0.243% / -0.061% |
| E3 `-d=alignhot=0` / `pgoinlinecdfthreshold=95` / `pgoinlinebudget=4000` / `GOEXPERIMENT=newinliner`, P | -0.167% / **-0.681%** / -0.506% / -0.181% | [-0.891, +0.562] / [-1.250, -0.110] / [-1.300, +0.295] / [-1.193, +0.842] | all below floor |
| identical-arms control (same exe both arms, 10:02) | -0.493% | [-1.342, +0.362] | detector fired |

Two protocol findings of the day, recorded because they change how the
numbers above must be read. (1) The 06:47-06:50 micro window was
non-stationary: those blocks ran 90 s after the arm builds and ~5 min after
the 20T profile capture, a P-core-1 (`0x4`) re-screen minutes later gave A/A
-0.62% [-1.45%, +0.21%] with ~18% within-block ns/op swings, and the three
PGO pairs from the window are not transitive; the post-idle 09:02 re-run is
the evidence, the 06:47 rows are screens. (2) The E micro screens at 10:02
ran right after a 20T block and the identical-arms control in that window
read -0.49% -- same lesson, so the E2/E3 micro rows are screens too. Micro
screens need the same idle cooldown as legs; it is now written into the
gates below.

**Decisions.**
- **Q1: parity -> Go 1.27 adopted** (commits `302e137` build: toolchain
  floor; `35be37b` ci: workflow pins). The tightest block of the session puts
  the 1.26.5 -> 1.27.0 delta at +0.16% with a +/-0.45 pp CI -- exactly the
  prior. No throughput claim attaches to the toolchain; arm64-bench.yml's
  pin bump starts a new measurement epoch there.
- **Q2: RETAINED, fresh profile committed as `default.pgo` (`ed9d2c7`).** At
  1T the fresh profile is real and above the floor (P-core LB +0.798%); at 20T
  the two independent blocks pool to -0.257% [-0.818%, +0.306%], i.e. no
  demonstrated regression (point and CI upper above -0.5%). The relaxed gate
  keeps it; the pooling rule was fixed in the vault before block 2 ran, after
  block 1 alone had put the "no regression" clause on a 0.06 pp margin.
  Mechanism, from `-d=pgodebug=1`: threshold 0.0929 -> 0.0883, hot functions
  19 -> 22, hot-budget inlines 12 -> 33 -- `suffixLessAfterKey` (cost 337) is
  now inlined into `mergeSortedPositionsAfterKey`, `tryWriteLiteralGroup` and
  `tryWriteTwoRuns`, and `constantRunOrder`, `appendOriginGroup`,
  `mergeEqualKeyRuns` and the emit family into their callers. That is the
  per-call comparator overhead the 2026-08-06 campaign bounded (+1.25% for the
  walk alone, +4.1 pp per-call share of the +5.4% ceiling), captured by the
  profile instead of by hand, and a compute-bound 1T win not transferring to
  20T is the documented pattern here. `-pgo=off` -> old profile screened at
  +1.0% [-0.7, +2.8] and -> fresh at +0.7% [-0.5, +1.9] in the contaminated
  window: PGO is worth about a percent at 1T whichever profile drives it.
- **E5 (inline-header comparator): not built.** The pre-registered gate was
  "check whether the fresh profile already inlines the comparator"; it does,
  at all three call sites. Nothing is left for a hand-inlined header to
  recover. Closed.
- **E1 (`asyncpreemptoff=1`): null, closed.** `//go:debug asyncpreemptoff=1`
  is rejected by the go command (runtime-only GODEBUG, not in the go:debug
  table); the arm was built with what the directive compiles to,
  `-ldflags=-X=runtime.godebugDefault=asyncpreemptoff=1` (the runtime's own
  `parsegodebug` applies it; string-proofed: `asyncpreemptoff=1` present in
  the cand binary, absent in base). -0.007% [-0.945%, +0.941%] at 20T. The
  10 ms sysmon handoff itself is not removable by GODEBUG.
- **E3 compile knobs: all closed.** `-d=alignhot=0` is slightly negative at
  20T (-0.316% [-0.659%, +0.029%], SE 0.12 pp) -- PGO hot-block alignment is
  worth keeping; `pgoinlinecdfthreshold=95` regresses at 1T (-0.68%
  [-1.25%, -0.11%]); `pgoinlinebudget=4000` leans negative; `newinliner`
  null. Shipping any of them would also have needed a `-gcflags` line in
  release.sh/build-release.ps1.
- **E2 (2 MiB large pages, vet-clean re-implementation of 8796f4a): not
  shipped.** Block 1 read +0.46% with the widest CI of the day (SE 0.63 pp),
  which put the verdict on the instrument rather than on the arm, so one
  replication was pre-registered with the rule "ship iff the pooled 20T LB
  clears +0.6%". Block 2: -0.133% [-0.793%, +0.532%]; pooled -0.058%
  [-0.571%, +0.458%], LB -0.472%. The 2026-08-13 +0.899% (every leg positive)
  does not reproduce in this epoch: that figure came from the pre-carve tree
  under 1.26.5, and nothing in the mechanism argument survives a null this
  tight -- the radix passes stream the same 4 KiB of records per hash
  whichever way the pages are mapped, and the per-thread working set already
  sits inside L2. Committed as `fc15ec9` and reverted in the following
  commit, so the vet-clean allocator is one `git revert` away for a host or
  epoch where page walks are back on the profile. Dead end until then.

**E2 implementation notes (what is on the branch regardless of the verdict).**
`largepage_windows.go` (`//go:build windows && !nolargepages`) enables
`SeLockMemoryPrivilege` best-effort, lets `VirtualAlloc(MEM_LARGE_PAGES)`
adjudicate, and returns the region as `[]byte` plus a `VirtualFree` closure.
The `uintptr` -> `unsafe.Pointer` step that `go vet`'s `unsafeptr` flagged
last time is gone: the address word is read back as a pointer
(`*(*unsafe.Pointer)(unsafe.Pointer(&addr))`), which is vet-clean and
checkptr-clean (`go test -race` over the whole astrobwt suite passes with
large pages active). `carveV114Scratch` takes the region when present and a
heap `[]byte` otherwise -- one carve, one layout test -- and registers
`runtime.AddCleanup(v, release)` so `pkg/engine` restart loops cannot leak the
off-heap region (the 22d198d concern). `-tags nolargepages` builds the heap
path on Windows, which is also the A/B base. Tests: `TestLargePageRegionContract`
(exact length, 2 MiB alignment, zeroed, writable, carve caps identical across
backings, heap fallback where the right is absent -- every CI runner) and
`TestLargePageCarveMatchesHeapHash`. Gates before any measured leg: vet at
v1/v3 silent, astrobwt suite at v1/v3/v114stats/nolargepages, race,
`V114_GATE_HASHES=1000008` million-hash 1000008/1000008 0 fallbacks,
selftest PASS, `--sustained` reports `largepages=true` on this host.

Run dirs: `bench-results/micro-couples/20260820-*`, `bench-results/thue-morse/20260820-*`
(each with `analysis.txt`), profiles + diagnostics + provenance in
`bench-results/pgo-refresh-20260820-064046/`. Ledger: `sandbox/ledger.tsv`
campaign section. Vault: pre-registration, one benchmark note per block
(`02-projects/go-miner/benchmarks/2026-08-20-*.md`), campaign log
`go127-toolchain-pgo-campaign-2026-08-20.md`.

## 2026-08-21 Thread sweep (E0), arXiv/go.dev lever hunt, and the x2 default (E9)

Two questions closed on one idle box: where the Go miner's scaling actually
bends, and whether the literature has anything left for the hot loops. The
sweep answered the first with numbers and turned up the session's only real
win, which had nothing to do with the literature.

**E0 -- the thread sweep the miner never had.** `scripts/bench-thread-sweep.ps1`
(new) runs each arm as a cold 60 s `--sustained` leg at 1/2/4/8/12/16/20
threads, mirror order per point (`go-x1 go-x2 zig-x2 zig-x2 go-x2 go-x1`), 30 s
cooldowns, stray check before every leg, per-logical-processor Actual Frequency
sampled throughout; median of the two legs per (arm, threads).

| threads | go-x1 KH/s | eff vs 1T | go-x2 KH/s | eff vs 1T | x2 vs x1 |
|---:|---:|---:|---:|---:|---:|
| 1 | 2.115 | 100.0% | 2.145 | 100.0% | +1.4% |
| 2 | 4.115 | 97.3% | 4.380 | 102.1% | +6.4% |
| 4 | 7.525 | 88.9% | 7.955 | 92.7% | +5.7% |
| 8 | 12.565 | 74.3% | 12.545 | 73.1% | -0.2% |
| 12 | 17.715 | 69.8% | 19.560 | 76.0% | +10.4% |
| 16 | 24.000 | 70.9% | 24.805 | 72.3% | +3.4% |
| 20 | 25.355 | 59.9% | 26.350 | 61.4% | +3.9% |

Three readings. (1) **There is no E-cluster cliff in this miner.** x1
efficiency *rises* across 12->16T, the step where all eight E-cores engage and
where the Zig sibling lost 11 pp in 2026-08 before its working set was halved.
The pre-registered trigger for an SoA radix restructure (a Go-only drop >= 5 pp
there) never fired, so that candidate is closed unbuilt -- consistent with the
two nulls that bracket it (shared scratch +0.26%, large pages pooled -0.06%):
the hot set already fits. (2) **The largest step is 4->8T** (-14.6 pp), when the
P-cores fill, not when the E-cores do; the same step appears in the other arm.
(3) **16->20T costs -11.0 pp**: the four SMT siblings add 1.355 KH/s, 339 H/s
each against a 1,500 H/s average, i.e. a sibling is worth ~23% of a primary.
They are still worth taking -- 20T beats 16T by +5.6% (x1) / +6.2% (x2) -- which
answers the "quiet 16T operating point" question in the negative.

Two cautions travel with the table. The Zig column (`bench2_pgo.exe T 60 1`) is
an **unvalidated arm**: the third argument's meaning is undocumented here, so
that binary's pipeline mode and pinning for this invocation are unverified and
its numbers sit ~23% below the figures the ledger records for the Zig
comparator; no cross-miner claim is made from this run. And the frequency log
(12,792 samples, 24 instances) is recorded but **not interpreted**: the
instances labelled E-core read *higher* than the P-core ones, which is
impossible on Raptor Lake, so the instance->core-type mapping is wrong. Only the
trend is safe: average clock falls ~8% from 1T to 20T and is within ~3% across
arms at equal thread count, so no arm was clock-starved. Mid-count legs are
noisy (4T x1 read 8.06 and 6.99 in the two mirror positions); the 20T points are
tight in both positions (x1 25.29/25.42, x2 26.34/26.36), which is the part the
next experiment rests on.

**The arXiv/go.dev lever hunt -- 20 candidates, 20 refutations.** A
source-restricted research pass (5 arXiv readers via the export API, 4 readers
over go.dev and the Go 1.27.0 source tree, no WebSearch) produced 139 findings
and 20 candidates; every adversarial verification that ran refuted its
candidate. Four refutations are worth keeping as closures rather than opinions:

- *All-uniform-column* and *period-256* closed forms: the trigger population is
  **exactly zero**. The wolf loop rescrambles whenever
  `step_3[pos1]-step_3[pos2] <= 0x40` (`pow.go:2444-2459`), so a k>=3 template
  run with every column uniform cannot occur; the proposed fast path could never
  fire.
- *k-way vvv closed form* (extending the retained `constantRunOrder` rule to the
  large-fallback merge, from arXiv 1710.01896's repetition detection): refuted on
  mechanism. Arena runs are not position-monotone at a uniform column -- their
  order is induced from later non-uniform columns -- so per-chunk chains would
  compare same-column positions across chunks, i.e. long LCPs (53.4% of
  equal-key adjacent pairs share 64-255 bytes) in place of today's short
  cross-column compares. Net <= 0; three votes reached this independently.
- *Call-free comparator tail*: the tail is long, not short, so the runtime's
  AVX2 memcmp is the right tool -- the same conclusion the 2026-08-19 archsimd
  probe reached from the other side.
- *SMT anti-phase turnstile*: with SA at 77.6% of hash, perfect anti-phasing
  moves at most ~5 pp of wall time and only four of eight P-cores carry a
  sibling at 20T; the handshake's idle cost is the same order as the gain.

The rest fell to ceiling arithmetic below the gate (split radix histograms, LSD
pass elision, branchless merge select, `-funcalign`, `PCALIGN`, `licm-off`,
hot-big audit, per-core-type lane counts, CPU-Sets pinning) or were duplicates
of E0. Seven candidates are **unverified rather than cleared** because a usage
limit killed 34 of the 54 planned votes; they are listed with the readers' own
priors in the vault note so they are not lost -- the largest are a
`materializeOrigins` inline SWAR for count<=8 (drop the ABI0 round trip on the
dominant path) and moving the fourteen port-5-only `PALIGNR` per block off p5 in
the 2-way SHA-NI kernel.

**E9 -- the x2 default.** The sweep's incidental finding is the one that pays:
`-pair` leads at every thread count and is off by default on amd64. The comment
justifying that default (`main.go:71-73`, "costs ~1% at high thread counts from
the doubled working set") is stale twice: the -1.1%@20T measurement behind it
predates the shared-scratch change, and `Hasher.HashPair` runs its two lanes
**sequentially through one `v114Scratch`** (`hasher.go:54-62`), so x2 never
doubled the hot set -- only the final SHA-256 is batched 2-way. Arms differ only
in `defaultPairMode`, carry distinct sha256s, and each self-reports its pipeline
in the bench header (`pipeline=x1` vs `pipeline=x2`).

Measured, 20T Thue-Morse, 8 legs x 240 s + discarded warm-up, drift-adjusted
(df=4): **+3.771%, 95% CI [+3.302%, +4.241%], one-sided 95% lower bound
+3.411%**, SE 0.169 pp, fitted drift +0.736%/leg. Every candidate leg
(25.5275, 25.5575, 25.740, 25.7525 KH/s) sits above every base leg (24.3925,
24.725, 24.860, 24.875) -- the arms do not overlap, which no other block this
campaign managed. With the sweep's +1.4% at 1T the relaxed gate is met at both
targets, so the default flipped (`defaultPairMode` now returns
`PairHashSupported()`, commit `cb97c44`): x2 wherever the interleaved kernel
exists, `-pair=false` still overrides. This is the largest retained effect in
the ledger and it is a one-line default change; the code and README comments
that justified the old default have been corrected rather than deleted, since
the -1% figure was true before the shared-scratch commit.

Run dirs: `bench-results/thread-sweep/20260821-020339-e0-go-vs-zig`,
`bench-results/thue-morse/<stamp>-e9-x1-vs-x2default`. Vault:
`research/2026-08-21-arxiv-godev-levers.md`,
`benchmarks/2026-08-21-e0-thread-sweep.md`, pre-registration entries of
2026-08-20 22:05 (E0) and 2026-08-21 02:35 (E9). Research journal with every
finding and vote: `subagents/workflows/wf_9018347f-334/journal.jsonl`.

## Closed Questions

- *Does the Go miner have a thread-scaling problem?* No. Measured 2026-08-21 on
  an idle box: per-thread efficiency 1T->20T is 59.9% (x1) / 61.4% (x2), the
  same shape the siblings show, with **no E-cluster cliff at 12->16T** (x1
  efficiency rises 69.8% -> 70.9%). The largest step is 4->8T, when the P-cores
  fill. The four P-core SMT siblings at 16->20T are worth ~23% of a primary
  thread each, but still worth taking: 20T beats 16T by +5.6% (x1) / +6.2% (x2),
  so a "quiet" 16-thread operating point costs ~6%.
- *Should x2 (`-pair`) be the amd64 default?* Yes, since 2026-08-21: +3.771%
  [+3.302%, +4.241%] at 20T and +1.4% at 1T. The old opt-in rationale (a
  doubled working set) stopped being true when the two lanes started sharing one
  v114 scratch.
- *Is there anything left in the suffix-array / radix / SMT literature?* Not for
  this workload at this size. A source-restricted arXiv + go.dev pass produced
  20 candidates and every adversarial verification that ran refuted its
  candidate. Two are structural and permanent: the "all-uniform column" and
  "period-256" closed forms have a trigger population of **exactly zero**
  because the wolf loop rescrambles at `step_3[pos1]-step_3[pos2] <= 0x40`
  (`pow.go:2444-2459`), and extending the DivSufSort repetition rule (arXiv
  1710.01896) to the k-way merge is mechanism-inverted -- arena runs are not
  position-monotone at a uniform column, so it would trade short cross-column
  compares for long same-column ones. Unverified rather than cleared (their
  votes died on a usage limit): a `materializeOrigins` inline SWAR for
  count<=8, `writeUniqueRunBatch` consuming count>8 in-kernel, and moving the
  fourteen port-5-only `PALIGNR` per block off p5 in the 2-way SHA-NI kernel.

- *Does a refreshed PGO profile help?* Yes at 1T, neutral at 20T, and it now
  ships: 2026-08-20 fresh go1.27.0 profile +1.42% [+0.67, +2.18] on a P-core,
  +0.49% [+0.10, +0.88] on an E-core, pooled 20T -0.26% [-0.82, +0.31] over two
  blocks; retained under the relaxed gate (commit `ed9d2c7`). The earlier
  refreshes (08-03 +0.73%/1T +0.17%/20T, 08-09 +1.22% [-1.78, +4.32], 08-09
  x2 +0.77% LB +0.19%) were the same effect measured too loosely. The
  mechanism is inlining structure (the comparator call sites), so the next
  refresh is due when the hot call graph changes again, not on a calendar.
- *Does Go 1.27's compiler change hashrate?* No: go1.26.5 -> go1.27.0 on
  identical source is +0.16% [-0.29%, +0.61%] at 20T and +0.2% at 1T; the
  backend diff trims ~1% of hot-path instructions with no BCE change and
  leaves the PGO hot set identical. Adopted as maintenance.
- *asyncpreemptoff, alignhot=0, pgoinline knobs, newinliner?* All null or
  negative at 20T/1T (2026-08-20); `//go:debug` cannot even express
  asyncpreemptoff. Closed.
- *Is there a faster SACA the other miners know about?* No. tnn-miner — the fastest
  open AstroBWTv3 miner — vendors canonical libdivsufsort (`divsufsort.c`, `sssort.c`,
  `trsort.c`); it has no libsais. Same family as v114. SA gains must come from
  engineering, not algorithm swaps.
- *Would a lookup-table op kernel help?* No on Raptor Lake; see above.
- *Does Go 1.27's `simd`/`archsimd` package help on amd64?* No. Every vector-shaped
  kernel is already hand-written AVX2, `archsimd` measured 1.59x slower on the
  comparator, and the remaining hot loops are memory-latency-bound or already asm.
  The portable `simd` package also cannot express a movemask, so it could not
  replace `buildEqualColumns` even at parity. **Re-confirmed by direct measurement
  under the 1.27 API, 2026-08-19:** portable `simd` is 1.8x slower than the asm on
  `materializeOrigins` (it compiles a per-call dispatch trampoline to four clones),
  `archsimd` is 1-14% slower on `buildEqualColumns`, and on the comparator the
  shipping kernel wins by 13-24% across the measured prefix distribution because
  its `bytes.Compare` tail is already the runtime's AVX2 memcmp. Closed.
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
- Toolchain: go1.27.0 (CI `1.27.x`, release `1.27.0`) since 2026-08-20;
  numbers measured under 1.26.5 are a different epoch (measured parity
  +0.16% [-0.29%, +0.61%] at 20T, but do not bridge across it for anything
  finer than that).
- Retention (relaxed by the user, 2026-08-06; previously "at least 2% at
  either target"): keep a candidate that is provably positive at either
  target — one-sided 95% lower bound above the ~0.6% attribution floor on
  the paired instruments — provided the other target shows no demonstrated
  regression beyond 0.5% (point >= -0.5% and CI upper bound not below
  -0.5%). Correctness gates are unchanged and non-negotiable.
- Micro screens get the same idle cooldown as sustained legs (>= 3 min after
  any build, test burn or 20T block): on 2026-08-20 an identical-arms control
  read -0.49% and a P-core-1 A/A read -0.62% when run minutes after a 20T
  block. A marginal verdict is settled by ONE pre-registered replication
  block pooled by inverse variance, never by re-running until it passes.
