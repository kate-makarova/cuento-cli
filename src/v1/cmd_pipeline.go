package main

import "fmt"

// ─── RESET PIPELINE ───────────────────────────────────────────────────────────

func runResetPipeline(app *AppConfig, projectName string, saved *ProjectConfig) {
	if saved == nil {
		fatalExit(red("  ✗ No saved config for project " + projectName))
	}

	cfg := &Config{}
	cfg.ProjectName = projectName
	cfg.GitHubToken = saved.GitHubToken

	runSteps([]step{
		{
			name: "Authenticate with GitHub",
			fn:   func() error { return authGitHub(cfg) },
		},
		{
			name: "Update backend workflow",
			fn: func() error {
				for _, branch := range []string{"main", "release"} {
					if err := ghUpdateFile(cfg.BackendFork, ".github/workflows/deploy.yml",
						branch, "Reset deployment workflow", backendWorkflow); err != nil {
						return fmt.Errorf("branch %s: %w", branch, err)
					}
				}
				return nil
			},
		},
		{
			name: "Update frontend workflow",
			fn: func() error {
				for _, branch := range []string{"main", "release"} {
					if err := ghUpdateFile(cfg.FrontendFork, ".github/workflows/main.yml",
						branch, "Reset deployment workflow", frontendWorkflow); err != nil {
						return fmt.Errorf("branch %s: %w", branch, err)
					}
				}
				return nil
			},
		},
		{
			name: "Disable dev pipelines in forks",
			fn: func() error {
				if err := ghUpdateFile(cfg.BackendFork, ".github/workflows/deploy-dev.yml",
					"main", "Disable dev pipeline in fork", devWorkflowDisabled); err != nil {
					return fmt.Errorf("backend: %w", err)
				}
				return ghUpdateFile(cfg.FrontendFork, ".github/workflows/deploy-dev.yml",
					"main", "Disable dev pipeline in fork", devWorkflowDisabled)
			},
		},
		{
			name: "Update Sonic workflow",
			fn: func() error {
				if err := ghUpdateFile(cfg.BackendFork, ".github/workflows/sonic.yml",
					"main", "Reset Sonic install workflow", sonicWorkflow); err != nil {
					return fmt.Errorf("sonic workflow: %w", err)
				}
				if err := ghUpdateFile(cfg.BackendFork, ".github/sonic/sonic.cfg",
					"main", "Reset Sonic config", sonicCfgFile); err != nil {
					return fmt.Errorf("sonic config: %w", err)
				}
				return ghUpdateFile(cfg.BackendFork, ".github/sonic/sonic.service",
					"main", "Reset Sonic service file", sonicServiceFile)
			},
		},
	}, 0, nil)

	fmt.Println()
}
