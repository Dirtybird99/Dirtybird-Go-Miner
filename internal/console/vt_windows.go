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

func terminalWidth() (int, bool) {
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(windows.Handle(windows.Stderr), &info); err != nil {
		return 0, false
	}
	width := int(info.Window.Right-info.Window.Left) + 1
	// conhost wraps at the screen *buffer* width when that is narrower than
	// the window.
	if buf := int(info.Size.X); buf > 0 && buf < width {
		width = buf
	}
	return width, width > 0
}
