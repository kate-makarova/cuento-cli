package main

import "fmt"

// ─── RESET PIPELINE ───────────────────────────────────────────────────────────

// ghUpdateOnBranches updates a file on each branch that exists, silently skipping missing ones.
func ghUpdateOnBranches(repo, path, commitMsg, content string, branches []string) error {
	for _, branch := range branches {
		if !ghBranchExists(repo, branch) {
			fmt.Printf("   skipping branch %s (not found)\n", branch)
			continue
		}
		if err := ghUpdateFile(repo, path, branch, commitMsg, content); err != nil {
			return fmt.Errorf("branch %s: %w", branch, err)
		}
	}
	return nil
}

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
				return ghUpdateOnBranches(cfg.BackendFork, ".github/workflows/deploy.yml",
					"Reset deployment workflow", backendWorkflow(cfg.ProjectName), []string{"main", "release"})
			},
		},
		{
			name: "Update frontend workflow",
			fn: func() error {
				return ghUpdateOnBranches(cfg.FrontendFork, ".github/workflows/main.yml",
					"Reset deployment workflow", frontendWorkflow(cfg.ProjectName), []string{"main", "release"})
			},
		},
		{
			name: "Disable dev pipelines in forks",
			fn: func() error {
				if err := ghUpdateOnBranches(cfg.BackendFork, ".github/workflows/deploy-dev.yml",
					"Disable dev pipeline in fork", devWorkflowDisabled, []string{"main"}); err != nil {
					return fmt.Errorf("backend: %w", err)
				}
				return ghUpdateOnBranches(cfg.FrontendFork, ".github/workflows/deploy-dev.yml",
					"Disable dev pipeline in fork", devWorkflowDisabled, []string{"main"})
			},
		},
		{
			name: "Update Sonic workflow",
			fn: func() error {
				if err := ghUpdateOnBranches(cfg.BackendFork, ".github/workflows/sonic.yml",
					"Reset Sonic install workflow", sonicWorkflow, []string{"main", "release"}); err != nil {
					return fmt.Errorf("sonic workflow: %w", err)
				}
				if err := ghUpdateOnBranches(cfg.BackendFork, ".github/sonic/sonic.cfg",
					"Reset Sonic config", sonicCfgFile, []string{"main", "release"}); err != nil {
					return fmt.Errorf("sonic config: %w", err)
				}
				return ghUpdateOnBranches(cfg.BackendFork, ".github/sonic/sonic.service",
					"Reset Sonic service file", sonicServiceFile, []string{"main", "release"})
			},
		},
		{
			name: "Update Qdrant workflow",
			fn: func() error {
				if err := ghUpdateOnBranches(cfg.BackendFork, ".github/workflows/qdrant.yml",
					"Reset Qdrant install workflow", qdrantWorkflow, []string{"main", "release"}); err != nil {
					return fmt.Errorf("qdrant workflow: %w", err)
				}
				return ghUpdateOnBranches(cfg.BackendFork, ".github/qdrant/qdrant.service",
					"Reset Qdrant service file", qdrantServiceFile, []string{"main", "release"})
			},
		},
	}, 0, nil)

	fmt.Println()
}
