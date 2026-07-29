# fakegetwork — a stand-in derod for lifecycle testing

A minimal DERO getwork daemon: it accepts the `/ws/<wallet>` upgrade, pushes jobs,
and **counts what actually arrives**.

## Why

The four Dirtybird miners carry ~215 unit tests between them and every CI pipeline
is green. None of them exercises the miner's **lifecycle** — connect, mine, submit,
lose the connection, redial. Every defect found in the 2026-07 sweep lived in that
gap:

| defect | what the unit tests said |
|---|---|
| C++ died with SIGPIPE (exit 141) on *any* disconnect, so no reconnect code could run on Linux/Android | green |
| C++ submit mailbox destroyed ~86% of found shares (380 delivered vs 2671) | green |
| C++ replayed shares from a dead session onto the fresh connection | green |
| Go replayed 17 stale shares per reconnect | green |
| Status row was never verified to *appear*, only to be suppressed | green |

None of these were findable without running the miner. That is what this is for.

It also counts **server-side**. A miner's own counters cannot be evidence for a bug
in that miner's submit path.

## Use

```bash
go run ./tools/fakegetwork -difficulty 20 -stats /tmp/run.json -duration 30s

# miners that accept plaintext
./go-miner   -d ws://127.0.0.1:31500 -w <wallet> -t 2
./zig-miner  -d ws://127.0.0.1:31500 -w <wallet>

# miners that dial TLS unconditionally
./dero-miner          -d 127.0.0.1:31501 -w <wallet>   # Rust
./dirtybird-miner-cpu -d 127.0.0.1:31501 -t 2 -V       # C++
```

Stats land on stdout and, with `-stats`, as JSON:

```json
{ "upgrades": 1, "jobs_sent": 48, "submits": 2671, "stale": 0, "duration_s": 25 }
```

## Traps this encodes

Each of these cost a wrong measurement to find. They are enforced or documented in
code so they are not rediscovered.

**Difficulty 1 is rejected.** Two independent reasons. The C++ miner computes
`target = 2^256 / difficulty` into 32 bytes; at difficulty 1 that needs 33 and the
low 32 are all zero, so `check_hash` rejects *everything* and the miner finds
nothing. Separately, at difficulty 1 every hash wins, so workers regenerate stale
shares faster than any queue can drain — which **inverts** reconnect results and
made a correct fix look like a regression (2499 "stale" after the fix vs 1024
before). Use `-difficulty 20` to flood shares, `-difficulty 8000` to space them
~3 s apart for reconnect work.

**`blob[0] & 0xf` must be `1`.** The Go miner enforces derohe's miniblock version
nibble (`internal/miner/state.go`) and refuses anything else as *"unsupported
miniblock version"*. Zig and Rust do **not** check it and will mine a malformed
job happily. A blob built without this is silently refused by exactly one of the
four, which reads as "Go is broken".

**Both listeners are served.** Rust and C++ dial TLS unconditionally; Zig and Go
accept `ws://`. A plaintext-only harness cannot reach half the family.

**The certificate is self-signed and stays that way.** A real derod mints a fresh
self-signed ECDSA P-256 cert at every start — empty subject, no SANs — and all four
miners dial with verification disabled. Making this CA-valid would make the harness
*less* representative.

## `stale` is not zero in a healthy run

A steady-state run with no disconnect at all still reports a small `stale` count
(observed: 3 out of 2339 submissions over 20 s at difficulty 20). That is **not** a
defect. `stale` means "the jobid does not match the one this connection is issuing
*right now*", and jobs rotate every 500 ms — so a share found against job N and
submitted microseconds after job N+1 went out counts as stale. It is the ordinary
job-rotation race, and the daemon would reject those shares too.

Two consequences for tests:

- **Do not assert `stale == 0` on a steady-state run.** It will flake.
- **For reconnect tests (C3), reduce the share rate** — `-difficulty 8000` spaces
  finds ~3 s apart, so rotation races effectively vanish and any `stale` that
  appears is genuinely a share carried across a reconnect. Compare arms rather
  than trusting an absolute: pre-fix Go produced 17, pre-fix C++ 66, both fixed
  builds 0.

## Reading results honestly

Two rules, both learned by getting them wrong:

**Check the control before the number.** A run where the miner never reconnected
reported a clean `"stale": 0` and looked like a pass. Assert `upgrades >= 1` (and
for reconnect scenarios, `>= 2`) *before* believing any count.

**Prove the harness can produce a non-zero.** A zero is not evidence until the same
scenario has been shown to produce a non-zero against a known-bad build. Where a
pre-fix binary exists, run it as the control arm.

Related failure modes worth remembering when scripting around this: `grep -c` counts
*lines*, not occurrences, and in-place status repaints share one line; `cmd | head`
reports `head`'s exit status, not `cmd`'s; and WSL reclaims `/tmp` between separate
invocations, so build and measure in one shell.
