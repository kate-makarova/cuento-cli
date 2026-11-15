package main

import (
	"errors"
	"testing"
)

func TestParseCommand_Valid(t *testing.T) {
	t.Parallel()
	projects := map[string]*ProjectConfig{"dominion": {}}

	tests := []struct {
		name string
		args []string
		want command
	}{
		{"help", []string{"help"}, command{name: "help"}},
		{"help flag", []string{"--help"}, command{name: "help"}},
		{"version", []string{"version"}, command{name: "version"}},
		{"list", []string{"list"}, command{name: "list"}},
		{"update", []string{"update", "--project", "dominion"}, command{name: "update", project: "dominion"}},
		{"update with migration", []string{"update", "--project=dominion", "--apply-migration"}, command{name: "update", project: "dominion", applyMigration: true}},
		{"diagnostics", []string{"diagnostics", "--project", "dominion"}, command{name: "diagnostics", project: "dominion"}},
		{"diagnostics json", []string{"diagnostics", "--project", "dominion", "--json"}, command{name: "diagnostics", project: "dominion", asJSON: true}},
		{"reset pipeline", []string{"reset-pipeline", "--project", "dominion"}, command{name: "reset-pipeline", project: "dominion"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseCommand(tt.args, projects)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("expected %+v, got %+v", tt.want, got)
			}
		})
	}
}

func TestParseCommand_UsageErrors(t *testing.T) {
	t.Parallel()
	projects := map[string]*ProjectConfig{"dominion": {}}

	tests := []struct {
		name string
		args []string
	}{
		{"no command", []string{}},
		{"unknown command", []string{"deploy"}},
		{"missing project", []string{"update"}},
		{"unknown project", []string{"update", "--project", "nowhere"}},
		{"unknown flag", []string{"update", "--project", "dominion", "--yes"}},
		{"flag of another command", []string{"update", "--project", "dominion", "--json"}},
		{"extra argument", []string{"reset-pipeline", "--project", "dominion", "now"}},
		{"argument after list", []string{"list", "all"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseCommand(tt.args, projects)
			var usage usageError
			if !errors.As(err, &usage) {
				t.Errorf("expected a usage error, got %v", err)
			}
		})
	}
}

func TestRunCommand_ExitCodes(t *testing.T) {
	app := &AppConfig{Projects: map[string]*ProjectConfig{
		"dominion":    {},
		"interrupted": {SetupStep: 4},
	}}
	defer func() { nonInteractive = false }()

	tests := []struct {
		name string
		args []string
		want int
	}{
		{"version", []string{"version"}, 0},
		{"list", []string{"list"}, 0},
		{"unknown command", []string{"deploy"}, 2},
		{"unknown project", []string{"update", "--project", "nowhere"}, 2},
		{"interrupted setup", []string{"update", "--project", "interrupted"}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runCommand(tt.args, app); got != tt.want {
				t.Errorf("expected exit code %d, got %d", tt.want, got)
			}
			if !nonInteractive {
				t.Error("expected commands to switch prompts off")
			}
		})
	}
}
