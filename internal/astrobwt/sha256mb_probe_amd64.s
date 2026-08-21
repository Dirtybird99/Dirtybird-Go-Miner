//go:build amd64 && shaprobe

// Probe support. Compiled only under -tags shaprobe, test-only, never shipped.
// PURE LEGACY-SSE like the kernel it probes: no VEX/AVX instruction here.

#include "textflag.h"

// func probeRdtscp() uint64
// RDTSCP serializes against earlier instructions, which RDTSC does not; the
// probe brackets a whole kernel call so the extra ~30 cycles are noise.
// The value is an invariant-TSC tick count, not a core cycle count: the TSC
// advances at a fixed base rate while the core clock turbos above it, so an
// absolute reading needs the core:TSC ratio before it means cycles.
TEXT ·probeRdtscp(SB), NOSPLIT, $0-8
	RDTSCP
	SHLQ $32, DX
	ORQ  DX, AX
	MOVQ AX, ret+0(FP)
	RET
