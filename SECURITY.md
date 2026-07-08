# Security Policy

This policy covers **Dirtybird Go Miner** (https://github.com/Dirtybird99/Dirtybird-Go-Miner).

## Reporting a Vulnerability

If you discover a security vulnerability in this project, please report it responsibly.

**Do NOT open a public GitHub issue for security vulnerabilities.**

Instead, please use one of the following methods:

1. **GitHub Security Advisories**: Use the "Report a vulnerability" button on the Security tab of the [Dirtybird99/Dirtybird-Go-Miner](https://github.com/Dirtybird99/Dirtybird-Go-Miner/security) repository
2. **Email**: Contact the maintainer directly

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| latest  | :white_check_mark: |

## Response Timeline

- **Acknowledgment**: Within 48 hours
- **Initial assessment**: Within 1 week
- **Fix/release**: Depends on severity

## Scope

This policy applies to the latest version of the software on the `main` branch, built
from source with Go 1.25+ (`GOAMD64=v3 go build -pgo=default.pgo`) to produce the
`go-miner` binary.

Please note:

- The fast paths need an x86-64 CPU with SHA-NI; other CPUs run the portable
  fallbacks. Reports about unrelated hardware or toolchain versions are out of scope.
- This is a CPU miner that connects to a DERO daemon/pool over the network. When
  reporting, please describe the configuration (daemon address, threads, and build
  flags) so the issue can be reproduced.
- Do not include real wallet addresses or private network details in public reports.
