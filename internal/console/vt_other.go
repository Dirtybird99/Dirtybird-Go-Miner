//go:build !windows

package console

import "os"

// enableVT reports whether stderr looks like a terminal (VT support assumed).
func enableVT() bool {
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
