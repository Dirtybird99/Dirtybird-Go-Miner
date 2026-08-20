//go:build !amd64.v3

package astrobwt

const uniqueRunBatchAvailable = false

func writeUniqueRunBatch(_ *uint32, _ *stage5Run, _ *uint32, _, groupStart, outPos, _, _ int) (int, int) {
	return groupStart, outPos
}
