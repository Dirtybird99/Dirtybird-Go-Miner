//go:build amd64.v4

package astrobwt

// AVX512MiningPath reports that the build includes the AVX-512 equal-column
// classifier used by normal mining.
const AVX512MiningPath = true
