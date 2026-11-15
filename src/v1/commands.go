package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
)

// ─── Non-interactive commands ─────────────────────────────────────────────────

const cliVersion = "2.1.0"

const usageText = `Usage:
  cuento-cli                         interactive menu
  cuento-cli list                    list saved projects
  cuento-cli update --project NAME [--apply-migration]
  cuento-cli diagnostics --project NAME [--json]
  cuento-cli reset-pipeline --project NAME
  cuento-cli version
  cuento-cli help

Commands never prompt. Credentials come from the saved project config.
Exit codes: 0 success, 1 the operation failed, 2 invalid usage.
`

type command struct {
	name           string
	project        string
	applyMigration bool
	asJSON         bool
}

// usageError marks mistakes in the command line itself (exit code 2).
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func parseCommand(args []string, projects map[string]*ProjectConfig) (command, error) {
	if len(args) == 0 {
		return command{}, usageError{"no command given"}
	}
	cmd := command{name: args[0]}
	switch cmd.name {
	case "help", "-h", "--help":
		cmd.name = "help"
		return cmd, noExtraArgs(args[1:])
	case "version", "--version":
		cmd.name = "version"
		return cmd, noExtraArgs(args[1:])
	case "list":
		return cmd, noExtraArgs(args[1:])
	case "update", "diagnostics", "reset-pipeline":
	default:
		return command{}, usageError{fmt.Sprintf("unknown command %q", cmd.name)}
	}

	fs := flag.NewFlagSet(cmd.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cmd.project, "project", "", "project name")
	switch cmd.name {
	case "update":
		fs.BoolVar(&cmd.applyMigration, "apply-migration", false, "apply the database migration if one is needed")
	case "diagnostics":
		fs.BoolVar(&cmd.asJSON, "json", false, "print the report as JSON")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return command{}, usageError{fmt.Sprintf("%s: %v", cmd.name, err)}
	}
	if err := noExtraArgs(fs.Args()); err != nil {
		return command{}, err
	}
	if cmd.project == "" {
		return command{}, usageError{fmt.Sprintf("%s: --project is required", cmd.name)}
	}
	if _, ok := projects[cmd.project]; !ok {
		return command{}, usageError{fmt.Sprintf("unknown project %q (see cuento-cli list)", cmd.project)}
	}
	return cmd, nil
}

func noExtraArgs(args []string) error {
	if len(args) > 0 {
		return usageError{fmt.Sprintf("unexpected argument %q", args[0])}
	}
	return nil
}

// runCommand runs one command from the command line and returns the exit code.
// Failures inside a command exit with status 1 through fatalExit.
func runCommand(args []string, app *AppConfig) int {
	nonInteractive = true

	cmd, err := parseCommand(args, app.Projects)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		fmt.Fprint(os.Stderr, "\n"+usageText)
		return 2
	}

	switch cmd.name {
	case "help":
		fmt.Print(usageText)
		return 0
	case "version":
		fmt.Println(cliVersion)
		return 0
	case "list":
		names := make([]string, 0, len(app.Projects))
		for name := range app.Projects {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Println(name)
		}
		return 0
	}

	saved := app.Projects[cmd.project]
	if saved.SetupStep > 0 {
		fmt.Fprintf(os.Stderr, "error: setup of project %q was interrupted; resume it from the interactive menu\n", cmd.project)
		return 1
	}
	switch cmd.name {
	case "update":
		runUpdate(app, cmd.project, saved, flagMigrationDecision(cmd.applyMigration))
	case "diagnostics":
		runDiagnosticsReport(cmd.project, saved, cmd.asJSON)
	case "reset-pipeline":
		runResetPipeline(app, cmd.project, saved)
	}
	return 0
}
