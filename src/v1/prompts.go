package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"golang.org/x/term"
)

// ─── Prompts ──────────────────────────────────────────────────────────────────

var reader = bufio.NewReader(os.Stdin)

// nonInteractive is set when the CLI runs a command from its arguments. Commands
// must never wait for input, so any prompt reached in that mode is an error.
var nonInteractive bool

var (
	errNonInteractive = errors.New("input required, but the CLI is running a command non-interactively")
	errInputClosed    = errors.New("input closed before an answer was given")
)

func checkInteractive() error {
	if nonInteractive {
		return errNonInteractive
	}
	return nil
}

// readLine returns the next trimmed line. A final line without a newline still
// counts; closed input with nothing left is an error rather than an empty answer,
// so required prompts cannot loop forever.
func readLine() (string, error) {
	if err := checkInteractive(); err != nil {
		return "", err
	}
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", errInputClosed
	}
	return strings.TrimSpace(line), nil
}

func mustReadLine() string {
	line, err := readLine()
	if err != nil {
		fatalExit(red("  ✗ " + err.Error()))
	}
	return line
}

func mustBeInteractive() {
	if err := checkInteractive(); err != nil {
		fatalExit(red("  ✗ " + err.Error()))
	}
}

func promptDefault(q, def string) string {
	mustBeInteractive()
	fmt.Printf("%s%s [%s]: ", cyan("? "), q, yellow(def))
	line := mustReadLine()
	if line == "" {
		return def
	}
	return line
}

func promptRequired(q string) string {
	mustBeInteractive()
	for {
		fmt.Print(cyan("? ") + q + ": ")
		line := mustReadLine()
		if line != "" {
			return line
		}
		fmt.Println(yellow("  This field is required."))
	}
}

func promptPassword(q string) string {
	mustBeInteractive()
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
	return mustReadLine()
}

func promptPasswordDefault(q, saved string) string {
	mustBeInteractive()
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
	val := mustReadLine()
	if val == "" {
		return saved
	}
	return val
}

func confirm(q string) bool {
	mustBeInteractive()
	fmt.Print(cyan("? ") + q + " [y/N]: ")
	return strings.EqualFold(mustReadLine(), "y")
}
