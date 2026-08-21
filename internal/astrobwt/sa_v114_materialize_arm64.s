//go:build arm64

#include "textflag.h"

TEXT ·materializeOrigins(SB), NOSPLIT, $0-24
	MOVD	dst+0(FP), R0
	MOVD	src+8(FP), R1
	MOVWU	count+16(FP), R2
	MOVWU	rel+20(FP), R3
	VDUP	R3, V0.S4
loop:
	VLD1.P	32(R1), [V1.S4, V2.S4]
	VADD	V0.S4, V1.S4, V1.S4
	VADD	V0.S4, V2.S4, V2.S4
	VST1.P	[V1.S4, V2.S4], 32(R0)
	SUBSW	$8, R2, R2
	BGT	loop
	RET
