//go:build arm64

package astrobwt

//go:noescape
func materializeOrigins(dst, src *uint32, count, rel uint32)
