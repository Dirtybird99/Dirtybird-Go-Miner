# Dirtybird Go Miner

A pure-Go AstroBWTv3 CPU miner for DERO. Sibling of the family's
[C++](https://github.com/Dirtybird99/Dirtybird-C-Miner),
[Zig](https://github.com/Dirtybird99/Dirtybird-Zig-Miner) and
[Rust](https://github.com/Dirtybird99/Dirtybird-Rust-Miner) miners.

- **Zero dev fee.** Every hash is yours.
- **Consensus-correct.** Startup is gated on the `pow("a")` KAT
  (`54e2324ddacc3f0383501a9e5760f85d63e9bc6705e9124ca7aef89016ab81ea`); the fast
  suffix array is verified byte-identical to the reference over a
  1,000,008-hash differential.
- **Pure Go, zero cgo.** One `go build`, one static binary, cross-compiles to
  anything Go targets.
- **Fast paths:** SHA-NI final hash and a pure-Go port of the family's "v1.14
  descriptor" structure-aware suffix array (~75% of hash time lives there).

## Downloads

Grab the latest [release](https://github.com/Dirtybird99/Dirtybird-Go-Miner/releases):

| Platform | Asset |
|---|---|
| Windows x64 | `Dirtybird-Go-Miner-win64-vX.Y.Z.zip` |
| Linux x64 | `Dirtybird-Go-Miner-amd64-vX.Y.Z.tar.gz` |
| Linux arm64 | `Dirtybird-Go-Miner-arm64-vX.Y.Z.tar.gz` |
| Android (Termux) | `Dirtybird-Go-Miner-android-arm64-vX.Y.Z.tar.gz` |
| macOS (Apple Silicon) | `Dirtybird-Go-Miner-macos-arm64-vX.Y.Z.tar.gz` |
| HiveOS / MMPOS | `dirtybird-go-miner-vX.Y.Z.hiveos_mmpos.amd64.tar.gz` |

Verify downloads with `SHA256SUMS.txt`.

### HiveOS / MMPOS

Create a flight sheet with miner `Custom`:

| Field | Value |
|---|---|
| Miner name | `dirtybird-go-miner` |
| Installation URL | the `hiveos_mmpos.amd64.tar.gz` asset URL from the latest release |
| Hash algorithm | `astrobwt` |
| Wallet and worker template | `%WAL%.%WORKER_NAME%` |

The miner name must match exactly: it is `CUSTOM_NAME` in `config/h-manifest.conf` and the top-level directory inside the archive, and HiveOS looks the package up by that name. Anything else fails to install.

There is no stats API, so `h-stats.sh` scrapes hashrate, accepted/rejected miniblocks and uptime from the status line in the log. `config/test-h-stats.sh` pins that parser and runs in CI.

## Quick start

```
go-miner -w <your-dero-wallet> -t 20
```

or run `start.bat` (Windows) / `bash script.sh` (Linux/macOS), or drop a
`config.json` beside the binary (same keys as the DeroLuna/family miners):

```json
{
  "daemon-address": "community-pools.mysrv.cloud:10300",
  "wallet": "dero1q...",
  "threads": 0
}
```

Precedence: CLI flags > config.json > built-in defaults. If you don't set a
wallet you mine to the bundled community wallet. `--setup` edits config.json
interactively and offers Community Pools, Rabid Mining, dero-node.net solo,
the DERO Foundation solo/full-block node, or a validated custom endpoint.
Pools are the phone-friendly choice because they pay smaller shares more
often; solo nodes have longer reward intervals.

## Android (Termux)

Install [Termux](https://f-droid.org/en/packages/com.termux/) (F-Droid build),
then:

```
curl -fsSL https://raw.githubusercontent.com/Dirtybird99/Dirtybird-Go-Miner/main/scripts/termux-setup.sh | bash
```

The script installs the latest arm64 release into `~/dirtybird-go-miner`,
prompts for pool/wallet/threads (defaulting to all-cores-minus-one), acquires
a wake-lock so Android Doze doesn't pause mining, and restarts the miner if it
crashes. Re-run with `--reconfigure` to change settings, `--update` to
upgrade, `--uninstall` to remove.

Notes for phones: `-pin`/`-high` have no effect on Android — use the thread
count to balance hashrate, temperature, and battery. The 2-way batched final
hash is on by default (`--pair=false` disables it). Mining on
battery drains it fast; keep the device plugged in and ventilated.

## Usage

| Flag | Meaning |
|---|---|
| `-d` | daemon/pool `[ws://\|wss://]host:port` (bare host:port = TLS; solo daemon port 10100, pools 10300) |
| `-w` | DERO wallet address to mine to |
| `-t` | mining threads (0 = all logical CPUs, max 255) |
| `-c`, `--config-file` | config.json path (default: beside the binary) |
| `-V` | verbose (adds submit-funnel counters to the status line) |
| `--setup` | interactively write config.json, then exit |
| `--selftest` | verify hash vectors and exit (0=PASS, 1=FAIL) |
| `--bench` | offline AstroBWTv3 benchmark and exit |
| `--statbench` | offline real-worker/status-line benchmark (`--secs N`) |
| `-v`, `--version` | print version |

Run `go-miner -h` for the advanced benchmarking/tuning flags. Reproducible
performance methodology and research notes live in [BENCHMARKING.md](BENCHMARKING.md)
and [PERF_RESEARCH.md](PERF_RESEARCH.md).

On Windows amd64 systems with up to 64 logical CPUs, topology-aware
P-core-first pinning is on by default; `--pin=false` opts out. Linux stays
unpinned by default, but `--pin` now pins one allowed CPU from every physical
core before SMT siblings and respects HiveOS/container CPU masks. Unsupported
requests fail closed to an unpinned run; missing topology falls back to the
allowed CPU order. `--high` remains opt-in. The x2 `--pair` path is on by
default wherever the 2-way kernel exists
(amd64 with SHA-NI, arm64 with SHA2); `--pair=false` opts out.

## Build from source

Go 1.27+:

```
GOAMD64=v3 go build -pgo=default.pgo -trimpath -ldflags "-s -w" -o go-miner .
```

`GOAMD64=v3` and the committed PGO profile (`default.pgo`, collected on the
mining workload) are the max-performance defaults on x86-64; both are optional.
Cross-compile with the usual `GOOS`/`GOARCH`, e.g.
`GOOS=linux GOARCH=arm64 go build .` — non-x86 targets use the portable
fallbacks automatically. `scripts/release.sh vX.Y.Z` reproduces the full
release asset set. The `GOAMD64=v4` AVX-512 classifier remains a diagnostic
candidate, not a release default; use the AMD workflow in
[BENCHMARKING.md](BENCHMARKING.md#amd-linux--hiveos-scaling) to validate it on
native hardware.

## Correctness

- Unconditional startup KAT: `pow("a")` must match through the selected SA
  backend or the miner refuses to run. `--selftest` covers both SAIS and V114.
- `go test ./...` runs the gates: 11 hash vectors, differential fuzz against
  the verbatim derohe reference (`internal/refpow`), a 5,000-hash v114-vs-SAIS
  differential (an opt-in 1,000,008-hash version gates releases), zero-allocation
  checks on the hash hot path, and difficulty-check fuzz against a big.Int oracle.
  Release CI separately runs the worker pipeline under `go test -race`.
- The fast SA declines gracefully: any input it can't handle falls back to the
  SAIS reference for that hash.

## Performance

Laptop hashrates are thermal-state dependent — compare miners only in the same
session, over several minutes. The 2026-08-03 diagnostic audit on the
i7-13700HX still places Go about **21-23% behind** the active local Zig x2
build at 1 and 20 threads; this is not an official promoted score. The old
“within ~10%” claim was not reproducible and has been removed. The exact
profiles, rejected candidates, and evidence boundary are in
[PERF_RESEARCH.md](PERF_RESEARCH.md).

## License

MIT — see [LICENSE](LICENSE). Third-party attributions (DERO reference hash
core, Go stdlib SA-IS derivation, module dependencies) are listed in
[THIRD-PARTY-LICENSES](THIRD-PARTY-LICENSES); the ported hash core carries the
DERO Foundation license (`internal/astrobwt/LICENSE-DERO.txt`).
