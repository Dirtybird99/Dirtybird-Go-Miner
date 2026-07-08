package main

import (
	"encoding/hex"
	"fmt"

	"go-miner/internal/astrobwt"
)

const katHash = "54e2324ddacc3f0383501a9e5760f85d63e9bc6705e9124ca7aef89016ab81ea"

// kat verifies AstroBWTv3("a") — the family's unconditional startup gate.
func kat() error {
	got := fmt.Sprintf("%x", astrobwt.Sum([]byte("a")))
	if got != katHash {
		return fmt.Errorf("KAT failed: pow(\"a\") = %s, want %s — refusing to mine with a broken hash", got, katHash)
	}
	return nil
}

var selftestVectors = []struct{ in, out string }{
	{"a", katHash},
	{"ab", "faeaff767be60134f0bcc5661b5f25413791b4df8ad22ff6732024d35ec4e7d0"},
	{"abc", "715c3d8c61a967b7664b1413f8af5a2a9ba0005922cb0ba4fac8a2d502b92cd6"},
	{"abcd", "74cc16efc1aac4768eb8124e23865da4c51ae134e29fa4773d80099c8bd39ab8"},
	{"abcde", "d080d0484272d4498bba33530c809a02a4785368560c5c3eac17b5dacd357c4b"},
	{"abcdef", "813e89e0484cbd3fbb3ee059083af53ed761b770d9c245be142c676f669e4607"},
	{"abcdefg", "3972fe8fe2c9480e9d4eff383b160e2f05cc855dc47604af37bc61fdf20f21ee"},
	{"abcdefgh", "f96191b7e39568301449d75d42d05090e41e3f79a462819473a62b1fcc2d0997"},
	{"abcdefghi", "8c76af6a57dfed744d5b7467fa822d9eb8536a851884aa7d8e3657028d511322"},
	{"abcdefghij", "f838568c38f83034b2ff679d5abf65245bd2be1b27c197ab5fbac285061cf0a7"},
}

func runSelftest() int {
	h := astrobwt.New()
	for _, v := range selftestVectors {
		got := fmt.Sprintf("%x", h.Hash([]byte(v.in)))
		if got != v.out {
			fmt.Printf("FAIL pow(%q) = %s want %s\n", v.in, got, v.out)
			return 1
		}
	}
	// 48-byte miniblock vector (the real input shape)
	data, _ := hex.DecodeString("419ebb000000001bbdc9bf2200000000635d6e4e24829b4249fe0e67878ad4350000000043f53e5436cf610000086b00")
	got := fmt.Sprintf("%x", h.Hash(data))
	if got != "c392762a462fd991ace791bfe858c338c10c23c555796b50f665b636cb8c8440" {
		fmt.Printf("FAIL 48-byte vector = %s\n", got)
		return 1
	}
	fmt.Printf("selftest pow(a): %s PASS (%d vectors)\n", katHash, len(selftestVectors)+1)
	return 0
}
