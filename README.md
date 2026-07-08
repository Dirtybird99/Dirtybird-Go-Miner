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
| macOS (Apple Silicon) | `Dirtybird-Go-Miner-macos-arm64-vX.Y.Z.tar.gz` |
| HiveOS / MMPOS | `dirtybird-go-miner-vX.Y.Z.hiveos_mmpos.amd64.tar.gz` |

Verify downloads with `SHA256SUMS.txt`.

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
interactively.

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
| `-v`, `--version` | print version |

Run `go-miner -h` for the advanced benchmarking/tuning flags.

## Build from source

Go 1.25+:

```
GOAMD64=v3 go build -pgo=default.pgo -trimpath -ldflags "-s -w" -o go-miner .
```

`GOAMD64=v3` and the committed PGO profile (`default.pgo`, collected on the
mining workload) are the max-performance defaults on x86-64; both are optional.
Cross-compile with the usual `GOOS`/`GOARCH`, e.g.
`GOOS=linux GOARCH=arm64 go build .` — non-x86 targets use the portable
fallbacks automatically. `scripts/release.sh vX.Y.Z` reproduces the full
release asset set.

## Correctness

- Unconditional startup KAT: `pow("a")` must match the family vector or the
  miner refuses to run.
- `go test ./...` runs the gates: 11 hash vectors, differential fuzz against
  the verbatim derohe reference (`internal/refpow`), a 5,000-hash v114-vs-SAIS
  differential (an opt-in 1,000,008-hash version gates releases), zero-allocation
  checks on the hash hot path, difficulty-check fuzz against a big.Int oracle,
  and race-detector runs on the worker pipeline.
- The fast SA declines gracefully: any input it can't handle falls back to the
  SAIS reference for that hash.

## Performance

Laptop hashrates are thermal-state dependent — compare miners only in the same
session, over several minutes. Same-session on an i7-13700HX (20 threads,
pinned, HIGH priority, idle machine): **~12.0 KH/s**, within ~10% of the
family's clang-built Zig miner on the same box (the remaining gap is LLVM
codegen; measured, not guessed). Machine-specific, not a universal claim.

## License

MIT — see [LICENSE](LICENSE). Third-party attributions (DERO reference hash
core, Go stdlib SA-IS derivation, module dependencies) are listed in
[THIRD-PARTY-LICENSES](THIRD-PARTY-LICENSES); the ported hash core carries the
DERO Foundation license (`internal/astrobwt/LICENSE-DERO.txt`).
