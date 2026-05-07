package main

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strings"

	"golang.org/x/term"
)

// ─── Prompts ──────────────────────────────────────────────────────────────────

var reader = bufio.NewReader(os.Stdin)

func promptDefault(q, def string) string {
	fmt.Printf("%s%s%s [%s]: ", cyan("? "), q, colorReset, yellow(def))
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func promptRequired(q string) string {
	for {
		fmt.Print(cyan("? ") + q + ": ")
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
		fmt.Println(yellow("  This field is required."))
	}
}

func promptPassword(q string) string {
	// term.ReadPassword uses raw console mode which is unreliable on old Windows
	// (8.1 and earlier). Fall back to plain input on Windows; the token is not
	// echoed on Unix-like systems but will be visible on legacy Windows consoles.
	if runtime.GOOS != "windows" && term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Print(cyan("? ") + q + ": ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err == nil {
			return string(b)
		}
	}
	fmt.Print(cyan("? ") + q + " (input visible): ")
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line)
}

func promptPasswordDefault(q, saved string) string {
	hint := ""
	if saved != "" {
		hint = " [saved, Enter to keep]"
	}
	if runtime.GOOS != "windows" && term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Print(cyan("? ") + q + hint + ": ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err == nil {
			if string(b) == "" {
				return saved
			}
			return string(b)
		}
	}
	fmt.Print(cyan("? ") + q + hint + " (input visible): ")
	line, _ := reader.ReadString('\n')
	val := strings.TrimSpace(line)
	if val == "" {
		return saved
	}
	return val
}

func confirm(q string) bool {
	fmt.Print(cyan("? ") + q + " [y/N]: ")
	line, _ := reader.ReadString('\n')
	return strings.EqualFold(strings.TrimSpace(line), "y")
}
