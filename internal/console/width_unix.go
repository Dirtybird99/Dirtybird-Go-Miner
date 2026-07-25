//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package console

import (
	"os"

	"golang.org/x/sys/unix"
)

func terminalWidth() (int, bool) {
	size, err := unix.IoctlGetWinsize(int(os.Stderr.Fd()), unix.TIOCGWINSZ)
	if err != nil || size.Col == 0 {
		return 0, false
	}
	return int(size.Col), true
}
