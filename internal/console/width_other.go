//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package console

func terminalWidth() (int, bool) { return 0, false }
