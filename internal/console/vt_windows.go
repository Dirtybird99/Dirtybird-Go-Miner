//go:build windows

package console

import "golang.org/x/sys/windows"

// enableVT turns on ANSI escape processing for stderr. Returns false when
// stderr is not a console (piped/redirected).
func enableVT() bool {
	h := windows.Handle(windows.Stderr)
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return false
	}
	if err := windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		return false
	}
	return true
}
