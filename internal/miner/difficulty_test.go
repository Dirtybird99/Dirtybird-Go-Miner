package miner

import (
	"math/big"
	"math/rand"
	"testing"
)

// Verbatim derohe cmd/dero-miner/difficulty.go semantics as the oracle.
func oracleCheckPowHashBig(pow [32]byte, diff uint64) bool {
	buf := pow // HashToBig reverses in place; work on a copy
	for i := 0; i < 16; i++ {
		buf[i], buf[31-i] = buf[31-i], buf[i]
	}
	bigPow := new(big.Int).SetBytes(buf[:])
	oneLsh256 := new(big.Int).Lsh(big.NewInt(1), 256)
	target := new(big.Int).Div(oneLsh256, new(big.Int).SetUint64(diff))
	return bigPow.Cmp(target) <= 0
}

func TestMeetsTargetVsOracle(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	diffs := []uint64{1, 2, 3, 20000, 976_500_000, 1 << 40, ^uint64(0)}
	var pow [32]byte
	for i := 0; i < 10000; i++ {
		rnd.Read(pow[:])
		// mix in hashes with leading zero bytes (the interesting region)
		for z := 0; z < i%8; z++ {
			pow[31-z] = 0
		}
		diff := diffs[i%len(diffs)]
		if i%3 == 0 {
			diff = rnd.Uint64()
			if diff == 0 {
				diff = 1
			}
		}
		target := ComputeTarget(diff)
		got := MeetsTarget(&pow, &target)
		want := oracleCheckPowHashBig(pow, diff)
		if got != want {
			t.Fatalf("mismatch: pow=%x diff=%d got=%v want=%v", pow, diff, got, want)
		}
	}
}

func TestMeetsTargetEdges(t *testing.T) {
	// diff=1: everything passes
	target := ComputeTarget(1)
	all := [32]byte{}
	for i := range all {
		all[i] = 0xff
	}
	if !MeetsTarget(&all, &target) {
		t.Fatal("diff=1 must accept the all-ones hash")
	}

	// exact equality passes (<=)
	target = ComputeTarget(20000)
	var pow [32]byte
	for i := 0; i < 32; i++ { // pow is little-endian: reverse of target
		pow[31-i] = target[i]
	}
	if !MeetsTarget(&pow, &target) {
		t.Fatal("hash exactly equal to target must pass")
	}
	if !oracleCheckPowHashBig(pow, 20000) {
		t.Fatal("oracle disagrees on the equality case")
	}

	// one above the target fails
	pow[0]++ // little-endian LSB... increment the most significant reversed byte instead:
	pow[0]--
	// bump the big end: target[0] maps to pow[31]
	if pow[31] != 0xff {
		pow2 := pow
		pow2[31]++
		if MeetsTarget(&pow2, &target) != oracleCheckPowHashBig(pow2, 20000) {
			t.Fatal("disagreement just above target")
		}
	}
}
