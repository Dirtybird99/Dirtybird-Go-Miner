//go:build amd64.v4

#include "textflag.h"

// func buildEqualColumns(first *byte, groupCount uint32, out *[4]uint64)
//
// Each iteration compares one 256-byte template row with the first row.
// Four ZMM comparisons replace the v3 kernel's eight YMM comparisons while
// four k-register accumulators retain the 256 column-equality bits.
TEXT ·buildEqualColumns(SB), NOSPLIT, $0-24
	MOVQ first+0(FP), AX
	MOVL groupCount+8(FP), CX
	MOVQ out+16(FP), DX
	MOVQ $-1, R8
	KMOVQ R8, K1
	KMOVQ R8, K2
	KMOVQ R8, K3
	KMOVQ R8, K4
	SUBL $1, CX
	JLE done
	LEAQ 256(AX), BX
loop:
	VMOVDQU8 0(BX), Z0
	VPCMPEQB 0(AX), Z0, K0
	KANDQ K0, K1, K1
	VMOVDQU8 64(BX), Z0
	VPCMPEQB 64(AX), Z0, K0
	KANDQ K0, K2, K2
	VMOVDQU8 128(BX), Z0
	VPCMPEQB 128(AX), Z0, K0
	KANDQ K0, K3, K3
	VMOVDQU8 192(BX), Z0
	VPCMPEQB 192(AX), Z0, K0
	KANDQ K0, K4, K4
	ADDQ $256, BX
	SUBL $1, CX
	JG loop
done:
	KMOVQ K1, R8
	KMOVQ K2, R9
	KMOVQ K3, R10
	KMOVQ K4, R11
	MOVQ R8, 0(DX)
	MOVQ R9, 8(DX)
	MOVQ R10, 16(DX)
	MOVQ R11, 24(DX)
	VZEROUPPER
	RET
