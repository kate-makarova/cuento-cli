package main

import "runtime"

// ─── ANSI colours ─────────────────────────────────────────────────────────────

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
)

// colorsEnabled is set at startup based on whether the terminal supports ANSI.
// Windows CMD and PowerShell on versions before Windows 10 do not support ANSI
// escape codes and will render them as raw text.
var colorsEnabled = runtime.GOOS != "windows"

func bold(s string) string {
	if colorsEnabled {
		return colorBold + s + colorReset
	}
	return s
}
func green(s string) string {
	if colorsEnabled {
		return colorGreen + s + colorReset
	}
	return s
}
func red(s string) string {
	if colorsEnabled {
		return colorRed + s + colorReset
	}
	return s
}
func yellow(s string) string {
	if colorsEnabled {
		return colorYellow + s + colorReset
	}
	return s
}
func cyan(s string) string {
	if colorsEnabled {
		return colorCyan + s + colorReset
	}
	return s
}
