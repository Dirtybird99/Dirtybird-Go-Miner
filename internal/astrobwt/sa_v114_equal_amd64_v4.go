//go:build amd64.v4

package astrobwt

//go:noescape
func buildEqualColumns(first *byte, groupCount uint32, out *[4]uint64)
