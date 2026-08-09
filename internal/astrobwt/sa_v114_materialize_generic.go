//go:build !amd64.v3

package astrobwt

import "unsafe"

func materializeOrigins(dst, src *uint32, count, rel uint32) {
	d := unsafe.Slice(dst, count)
	s := unsafe.Slice(src, count)
	for i := range d {
		d[i] = s[i] + rel
	}
}
