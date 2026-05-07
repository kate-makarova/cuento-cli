package main

import (
	"fmt"
	"os"
	"runtime"
)

// ─── Step runner ──────────────────────────────────────────────────────────────

type step struct {
	name   string
	fn     func() error
	always bool // run even when skipping (e.g. connect to server)
}

// fatalExit prints an error and exits. On Windows it pauses so the user can
// read the message before the console window closes.
func fatalExit(msg string) {
	fmt.Println(msg)
	if runtime.GOOS == "windows" {
		fmt.Println("\nPress Enter to exit...")
		_, _ = reader.ReadString('\n')
	}
	os.Exit(1)
}

// runSteps executes steps sequentially. startFrom (1-indexed) skips earlier steps
// unless they are marked always. onStepDone is called with the 1-indexed step number
// after each step completes (use nil when progress tracking is not needed).
func runSteps(steps []step, startFrom int, onStepDone func(int)) {
	passed, skipped := 0, 0
	for i, s := range steps {
		stepNum := i + 1
		skip := stepNum < startFrom && !s.always
		if skip {
			fmt.Printf("\n▶  %s %s\n", s.name, cyan("(already done)"))
			skipped++
			continue
		}
		label := s.name
		if stepNum < startFrom && s.always {
			label += " (prerequisite)"
		}
		fmt.Printf("\n▶  %s\n", bold(label))
		if err := s.fn(); err != nil {
			fatalExit(red("   ✗ "+err.Error()) + red("\nStopped."))
		}
		fmt.Println(green("   ✓ Done"))
		passed++
		if onStepDone != nil && stepNum >= startFrom {
			onStepDone(stepNum)
		}
	}
	fmt.Println()
	fmt.Println(bold("─────────── Done ───────────"))
	if skipped > 0 {
		fmt.Printf("  %s  %d steps completed, %d skipped\n", green("✓"), passed, skipped)
	} else {
		fmt.Printf("  %s  %d steps completed\n", green("✓"), passed)
	}
	fmt.Println("────────────────────────────")
}
