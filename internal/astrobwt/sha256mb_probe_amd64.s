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


// The shipped kernel's round constants and byte-swap mask are file-static
// (<> symbols), so they are not linkable from here; these are copies. They
// are the SHA-256 constants, so they cannot drift from the originals.

DATA probeShufMask<>+0x00(SB)/8, $0x0405060700010203
DATA probeShufMask<>+0x08(SB)/8, $0x0c0d0e0f08090a0b
GLOBL probeShufMask<>(SB), RODATA|NOPTR, $16
DATA probeK256<>+0x00(SB)/4, $0x428A2F98
DATA probeK256<>+0x04(SB)/4, $0x71374491
DATA probeK256<>+0x08(SB)/4, $0xB5C0FBCF
DATA probeK256<>+0x0c(SB)/4, $0xE9B5DBA5
DATA probeK256<>+0x10(SB)/4, $0x3956C25B
DATA probeK256<>+0x14(SB)/4, $0x59F111F1
DATA probeK256<>+0x18(SB)/4, $0x923F82A4
DATA probeK256<>+0x1c(SB)/4, $0xAB1C5ED5
DATA probeK256<>+0x20(SB)/4, $0xD807AA98
DATA probeK256<>+0x24(SB)/4, $0x12835B01
DATA probeK256<>+0x28(SB)/4, $0x243185BE
DATA probeK256<>+0x2c(SB)/4, $0x550C7DC3
DATA probeK256<>+0x30(SB)/4, $0x72BE5D74
DATA probeK256<>+0x34(SB)/4, $0x80DEB1FE
DATA probeK256<>+0x38(SB)/4, $0x9BDC06A7
DATA probeK256<>+0x3c(SB)/4, $0xC19BF174
DATA probeK256<>+0x40(SB)/4, $0xE49B69C1
DATA probeK256<>+0x44(SB)/4, $0xEFBE4786
DATA probeK256<>+0x48(SB)/4, $0x0FC19DC6
DATA probeK256<>+0x4c(SB)/4, $0x240CA1CC
DATA probeK256<>+0x50(SB)/4, $0x2DE92C6F
DATA probeK256<>+0x54(SB)/4, $0x4A7484AA
DATA probeK256<>+0x58(SB)/4, $0x5CB0A9DC
DATA probeK256<>+0x5c(SB)/4, $0x76F988DA
DATA probeK256<>+0x60(SB)/4, $0x983E5152
DATA probeK256<>+0x64(SB)/4, $0xA831C66D
DATA probeK256<>+0x68(SB)/4, $0xB00327C8
DATA probeK256<>+0x6c(SB)/4, $0xBF597FC7
DATA probeK256<>+0x70(SB)/4, $0xC6E00BF3
DATA probeK256<>+0x74(SB)/4, $0xD5A79147
DATA probeK256<>+0x78(SB)/4, $0x06CA6351
DATA probeK256<>+0x7c(SB)/4, $0x14292967
DATA probeK256<>+0x80(SB)/4, $0x27B70A85
DATA probeK256<>+0x84(SB)/4, $0x2E1B2138
DATA probeK256<>+0x88(SB)/4, $0x4D2C6DFC
DATA probeK256<>+0x8c(SB)/4, $0x53380D13
DATA probeK256<>+0x90(SB)/4, $0x650A7354
DATA probeK256<>+0x94(SB)/4, $0x766A0ABB
DATA probeK256<>+0x98(SB)/4, $0x81C2C92E
DATA probeK256<>+0x9c(SB)/4, $0x92722C85
DATA probeK256<>+0xa0(SB)/4, $0xA2BFE8A1
DATA probeK256<>+0xa4(SB)/4, $0xA81A664B
DATA probeK256<>+0xa8(SB)/4, $0xC24B8B70
DATA probeK256<>+0xac(SB)/4, $0xC76C51A3
DATA probeK256<>+0xb0(SB)/4, $0xD192E819
DATA probeK256<>+0xb4(SB)/4, $0xD6990624
DATA probeK256<>+0xb8(SB)/4, $0xF40E3585
DATA probeK256<>+0xbc(SB)/4, $0x106AA070
DATA probeK256<>+0xc0(SB)/4, $0x19A4C116
DATA probeK256<>+0xc4(SB)/4, $0x1E376C08
DATA probeK256<>+0xc8(SB)/4, $0x2748774C
DATA probeK256<>+0xcc(SB)/4, $0x34B0BCB5
DATA probeK256<>+0xd0(SB)/4, $0x391C0CB3
DATA probeK256<>+0xd4(SB)/4, $0x4ED8AA4A
DATA probeK256<>+0xd8(SB)/4, $0x5B9CCA4F
DATA probeK256<>+0xdc(SB)/4, $0x682E6FF3
DATA probeK256<>+0xe0(SB)/4, $0x748F82EE
DATA probeK256<>+0xe4(SB)/4, $0x78A5636F
DATA probeK256<>+0xe8(SB)/4, $0x84C87814
DATA probeK256<>+0xec(SB)/4, $0x8CC70208
DATA probeK256<>+0xf0(SB)/4, $0x90BEFFFA
DATA probeK256<>+0xf4(SB)/4, $0xA4506CEB
DATA probeK256<>+0xf8(SB)/4, $0xBEF9A3F7
DATA probeK256<>+0xfc(SB)/4, $0xC67178F2
GLOBL probeK256<>(SB), RODATA|NOPTR, $256

// func probeBlocks2NoSched(state0 *[8]uint32, p0 *byte, state1 *[8]uint32, p1 *byte, nblocks int)
//
// WRONG BY DESIGN. Identical to ·sha256Blocks2NI with every SHA256MSG1,
// SHA256MSG2 and schedule PALIGNR removed, so the message words are stale
// after the first four rounds. It computes a digest nobody wants; its only
// output is a cycle count that prices the message schedule against the round
// chain. Never call it from anything but the probe test.
//
// Dropping the schedule drops two things at once: the schedule uops, and the
// dependency that makes each group's X0 staging wait on the previous group's
// SHA256MSG2. So the difference it measures is an upper bound on what
// reordering the schedule could recover, since a reordering keeps that
// dependency and only moves the uops.
//
// The prologue, the per-block state saves, the state add-back, the loop
// counter and the epilogue are unchanged from the shipped kernel.
TEXT ·probeBlocks2NoSched(SB), NOSPLIT, $64-40
	MOVQ state0+0(FP), DI
	MOVQ p0+8(FP), SI
	MOVQ state1+16(FP), R8
	MOVQ p1+24(FP), R9
	MOVQ nblocks+32(FP), DX
	TESTQ DX, DX
	JZ   probeDone2
	LEAQ probeK256<>(SB), AX
	MOVOU probeShufMask<>(SB), X13
	MOVOU (DI), X1
	MOVOU 16(DI), X2
	PSHUFD $0xB1, X1, X1
	PSHUFD $0x1B, X2, X2
	MOVO  X1, X14
	PALIGNR $8, X2, X1
	PBLENDW $0xF0, X14, X2
	MOVOU (R8), X3
	MOVOU 16(R8), X4
	PSHUFD $0xB1, X3, X3
	PSHUFD $0x1B, X4, X4
	MOVO  X3, X15
	PALIGNR $8, X4, X3
	PBLENDW $0xF0, X15, X4

probeLoop2:
	MOVOU X1, 0(SP)
	MOVOU X2, 16(SP)
	MOVOU X3, 32(SP)
	MOVOU X4, 48(SP)
	// laneA group 0 (rounds 0-3)
	MOVOU 0(SI), X5
	PSHUFB X13, X5
	MOVO  X5, X0
	PADDD 0(AX), X0
	SHA256RNDS2 X0, X1, X2
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X2, X1
	// laneB group 0 (rounds 0-3)
	MOVOU 0(R9), X9
	PSHUFB X13, X9
	MOVO  X9, X0
	PADDD 0(AX), X0
	SHA256RNDS2 X0, X3, X4
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X4, X3
	// laneA group 1 (rounds 4-7)
	MOVOU 16(SI), X6
	PSHUFB X13, X6
	MOVO  X6, X0
	PADDD 16(AX), X0
	SHA256RNDS2 X0, X1, X2
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X2, X1
	// laneB group 1 (rounds 4-7)
	MOVOU 16(R9), X10
	PSHUFB X13, X10
	MOVO  X10, X0
	PADDD 16(AX), X0
	SHA256RNDS2 X0, X3, X4
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X4, X3
	// laneA group 2 (rounds 8-11)
	MOVOU 32(SI), X7
	PSHUFB X13, X7
	MOVO  X7, X0
	PADDD 32(AX), X0
	SHA256RNDS2 X0, X1, X2
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X2, X1
	// laneB group 2 (rounds 8-11)
	MOVOU 32(R9), X11
	PSHUFB X13, X11
	MOVO  X11, X0
	PADDD 32(AX), X0
	SHA256RNDS2 X0, X3, X4
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X4, X3
	// laneA group 3 (rounds 12-15)
	MOVOU 48(SI), X8
	PSHUFB X13, X8
	MOVO  X8, X0
	PADDD 48(AX), X0
	SHA256RNDS2 X0, X1, X2
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X2, X1
	// laneB group 3 (rounds 12-15)
	MOVOU 48(R9), X12
	PSHUFB X13, X12
	MOVO  X12, X0
	PADDD 48(AX), X0
	SHA256RNDS2 X0, X3, X4
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X4, X3
	// laneA group 4 (rounds 16-19)
	MOVO  X5, X0
	PADDD 64(AX), X0
	SHA256RNDS2 X0, X1, X2
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X2, X1
	// laneB group 4 (rounds 16-19)
	MOVO  X9, X0
	PADDD 64(AX), X0
	SHA256RNDS2 X0, X3, X4
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X4, X3
	// laneA group 5 (rounds 20-23)
	MOVO  X6, X0
	PADDD 80(AX), X0
	SHA256RNDS2 X0, X1, X2
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X2, X1
	// laneB group 5 (rounds 20-23)
	MOVO  X10, X0
	PADDD 80(AX), X0
	SHA256RNDS2 X0, X3, X4
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X4, X3
	// laneA group 6 (rounds 24-27)
	MOVO  X7, X0
	PADDD 96(AX), X0
	SHA256RNDS2 X0, X1, X2
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X2, X1
	// laneB group 6 (rounds 24-27)
	MOVO  X11, X0
	PADDD 96(AX), X0
	SHA256RNDS2 X0, X3, X4
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X4, X3
	// laneA group 7 (rounds 28-31)
	MOVO  X8, X0
	PADDD 112(AX), X0
	SHA256RNDS2 X0, X1, X2
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X2, X1
	// laneB group 7 (rounds 28-31)
	MOVO  X12, X0
	PADDD 112(AX), X0
	SHA256RNDS2 X0, X3, X4
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X4, X3
	// laneA group 8 (rounds 32-35)
	MOVO  X5, X0
	PADDD 128(AX), X0
	SHA256RNDS2 X0, X1, X2
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X2, X1
	// laneB group 8 (rounds 32-35)
	MOVO  X9, X0
	PADDD 128(AX), X0
	SHA256RNDS2 X0, X3, X4
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X4, X3
	// laneA group 9 (rounds 36-39)
	MOVO  X6, X0
	PADDD 144(AX), X0
	SHA256RNDS2 X0, X1, X2
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X2, X1
	// laneB group 9 (rounds 36-39)
	MOVO  X10, X0
	PADDD 144(AX), X0
	SHA256RNDS2 X0, X3, X4
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X4, X3
	// laneA group 10 (rounds 40-43)
	MOVO  X7, X0
	PADDD 160(AX), X0
	SHA256RNDS2 X0, X1, X2
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X2, X1
	// laneB group 10 (rounds 40-43)
	MOVO  X11, X0
	PADDD 160(AX), X0
	SHA256RNDS2 X0, X3, X4
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X4, X3
	// laneA group 11 (rounds 44-47)
	MOVO  X8, X0
	PADDD 176(AX), X0
	SHA256RNDS2 X0, X1, X2
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X2, X1
	// laneB group 11 (rounds 44-47)
	MOVO  X12, X0
	PADDD 176(AX), X0
	SHA256RNDS2 X0, X3, X4
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X4, X3
	// laneA group 12 (rounds 48-51)
	MOVO  X5, X0
	PADDD 192(AX), X0
	SHA256RNDS2 X0, X1, X2
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X2, X1
	// laneB group 12 (rounds 48-51)
	MOVO  X9, X0
	PADDD 192(AX), X0
	SHA256RNDS2 X0, X3, X4
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X4, X3
	// laneA group 13 (rounds 52-55)
	MOVO  X6, X0
	PADDD 208(AX), X0
	SHA256RNDS2 X0, X1, X2
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X2, X1
	// laneB group 13 (rounds 52-55)
	MOVO  X10, X0
	PADDD 208(AX), X0
	SHA256RNDS2 X0, X3, X4
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X4, X3
	// laneA group 14 (rounds 56-59)
	MOVO  X7, X0
	PADDD 224(AX), X0
	SHA256RNDS2 X0, X1, X2
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X2, X1
	// laneB group 14 (rounds 56-59)
	MOVO  X11, X0
	PADDD 224(AX), X0
	SHA256RNDS2 X0, X3, X4
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X4, X3
	// laneA group 15 (rounds 60-63)
	MOVO  X8, X0
	PADDD 240(AX), X0
	SHA256RNDS2 X0, X1, X2
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X2, X1
	// laneB group 15 (rounds 60-63)
	MOVO  X12, X0
	PADDD 240(AX), X0
	SHA256RNDS2 X0, X3, X4
	PSHUFD $0x0E, X0, X0
	SHA256RNDS2 X0, X4, X3
	// add-back both lanes (X0/X14/X15 are dead after round 63)
	MOVOU 0(SP), X0
	PADDD X0, X1
	MOVOU 16(SP), X0
	PADDD X0, X2
	MOVOU 32(SP), X14
	PADDD X14, X3
	MOVOU 48(SP), X15
	PADDD X15, X4
	ADDQ $64, SI
	ADDQ $64, R9
	SUBQ $1, DX
	JNE  probeLoop2

	PSHUFD $0x1B, X1, X1
	PSHUFD $0xB1, X2, X2
	MOVO  X1, X14
	PBLENDW $0xF0, X2, X1
	PALIGNR $8, X14, X2
	MOVOU X1, (DI)
	MOVOU X2, 16(DI)
	PSHUFD $0x1B, X3, X3
	PSHUFD $0xB1, X4, X4
	MOVO  X3, X15
	PBLENDW $0xF0, X4, X3
	PALIGNR $8, X15, X4
	MOVOU X3, (R8)
	MOVOU X4, 16(R8)
probeDone2:
	RET
