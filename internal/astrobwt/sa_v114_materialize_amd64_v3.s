//go:build amd64.v3

#include "textflag.h"

TEXT ·materializeOrigins(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), AX
	MOVQ src+8(FP), CX
	MOVL count+16(FP), DX
	VPBROADCASTD rel+20(FP), Y0
loop:
	VMOVDQU (CX), Y1
	VPADDD Y0, Y1, Y1
	VMOVDQU Y1, (AX)
	ADDQ $32, AX
	ADDQ $32, CX
	SUBL $8, DX
	JG loop
	VZEROUPPER
	RET
