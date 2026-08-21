//go:build amd64.v4

package astrobwt

// AVX512MiningPath reports that the v4 build includes the AVX-512
// equal-column classifier.
const AVX512MiningPath = true
