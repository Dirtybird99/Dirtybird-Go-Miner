//go:build amd64.v3

package astrobwt

//go:noescape
func materializeOrigins(dst, src *uint32, count, rel uint32)
