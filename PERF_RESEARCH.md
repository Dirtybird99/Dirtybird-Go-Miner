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
- Keep a candidate only if median `BenchmarkHashV114` improves by at least 2%
  and sustained `20 --pin --high` does not regress.
