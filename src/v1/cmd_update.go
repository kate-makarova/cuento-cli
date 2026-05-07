package main

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// ─── UPDATE ───────────────────────────────────────────────────────────────────

func runUpdate(app *AppConfig, projectName string, saved *ProjectConfig) {
	if saved == nil {
		fatalExit(red("  ✗ No saved config for project " + projectName))
	}

	cfg := &Config{}
	cfg.ProjectName = projectName
	cfg.GitHubToken = saved.GitHubToken
	cfg.GitHubUser = saved.GitHubUser
	cfg.BackendFork = saved.GitHubUser + "/" + projectName + "-backend"
	cfg.FrontendFork = saved.GitHubUser + "/" + projectName + "-frontend"
	cfg.ServerIP = saved.ServerIP
	cfg.SSHUser = saved.SSHUser
	cfg.SSHPass = saved.SSHPass
	cfg.SudoPass = saved.SudoPass
	cfg.DBRootPass = saved.DBRootPass
	cfg.DBName = saved.DBName
	cfg.DBUser = saved.DBUser
	cfg.DBPass = saved.DBPass

	if cfg.GitHubToken != "" {
		cmd := exec.Command("gh", "auth", "login", "--with-token")
		cmd.Stdin = strings.NewReader(cfg.GitHubToken + "\n")
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		_ = cmd.Run()
	}

	user, err := localOutput("gh", "api", "user", "--jq", ".login")
	if err != nil || user == "" {
		fatalExit(red("  ✗ Could not resolve GitHub user: " + err.Error()))
	}
	cfg.GitHubUser = user
	cfg.BackendFork = cfg.GitHubUser + "/" + projectName + "-backend"
	cfg.FrontendFork = cfg.GitHubUser + "/" + projectName + "-frontend"

	var remote *Remote

	// SQL diff and commit SHAs are computed mid-flow; captured here for later steps.
	var migrationSQL string
	var mainGoChanged bool
	var pipelineChanged bool
	var newBackendSHA, newFrontendSHA string

	runSteps([]step{
		{
			name: "Read last deployed backend commit",
			fn: func() error {
				content, err := ghReadFile(cfg.BackendFork, deployedCommitFile, trackBranch)
				if err != nil {
					return fmt.Errorf("could not read %s — has this project been set up with 'create'? %w",
						deployedCommitFile, err)
				}
				lastSHA := strings.TrimSpace(content)
				newBackendSHA, err = ghGetLatestCommit(upstreamBackend, trackBranch)
				if err != nil {
					return err
				}
				fmt.Printf("   Deployed : %s\n", lastSHA)
				fmt.Printf("   Latest   : %s\n", newBackendSHA)

				if lastSHA == newBackendSHA {
					fmt.Println(green("   Already up to date — no SQL migration needed."))
					return nil
				}

				// Fetch SQL at both commits and generate migration
				fmt.Println("   Comparing SQL schemas...")
				oldSQL, err := ghReadFile(upstreamBackend, sqlFile, lastSHA)
				if err != nil {
					return fmt.Errorf("fetching SQL at deployed commit: %w", err)
				}
				newSQL, err := ghReadFile(upstreamBackend, sqlFile, newBackendSHA)
				if err != nil {
					return fmt.Errorf("fetching SQL at latest commit: %w", err)
				}

				migrationSQL = generateMigration(oldSQL, newSQL)
				if migrationSQL == "" {
					fmt.Println(green("   SQL schema unchanged — no migration needed."))
				} else {
					fmt.Println(yellow("   Migration SQL generated:"))
					fmt.Println()
					for _, line := range strings.Split(strings.TrimSpace(migrationSQL), "\n") {
						fmt.Println("   " + line)
					}
					fmt.Println()
				}

				// Check if main.go or .github/ changed
				changedFiles, err := localOutput("gh", "api",
					fmt.Sprintf("repos/%s/compare/%s...%s", upstreamBackend, lastSHA, newBackendSHA),
					"--jq", ".files[].filename")
				if err == nil {
					for _, f := range strings.Split(changedFiles, "\n") {
						f = strings.TrimSpace(f)
						if f == "main.go" {
							mainGoChanged = true
						}
						if strings.HasPrefix(f, ".github/") {
							pipelineChanged = true
						}
					}
				}

				return nil
			},
		},
		{
			name: "Read last deployed frontend commit",
			fn: func() error {
				content, err := ghReadFile(cfg.FrontendFork, deployedFrontendCommitFile, trackBranch)
				if err != nil {
					// File doesn't exist yet (project set up before this feature).
					// Record current upstream commit and skip diff detection.
					fmt.Println(yellow("   Commit file not found — initialising frontend tracking."))
					newFrontendSHA, err = ghGetLatestCommit(upstreamFrontend, trackBranch)
					if err != nil {
						return err
					}
					fmt.Printf("   Recording %s\n", newFrontendSHA)
					return ghUpdateFile(cfg.FrontendFork, deployedFrontendCommitFile, trackBranch,
						"Record last deployed upstream commit", newFrontendSHA+"\n")
				}
				lastSHA := strings.TrimSpace(content)
				newFrontendSHA, err = ghGetLatestCommit(upstreamFrontend, trackBranch)
				if err != nil {
					return err
				}
				fmt.Printf("   Deployed : %s\n", lastSHA)
				fmt.Printf("   Latest   : %s\n", newFrontendSHA)
				if lastSHA == newFrontendSHA {
					fmt.Println(green("   Already up to date."))
					return nil
				}
				changedFiles, err := localOutput("gh", "api",
					fmt.Sprintf("repos/%s/compare/%s...%s", upstreamFrontend, lastSHA, newFrontendSHA),
					"--jq", ".files[].filename")
				if err == nil {
					for _, f := range strings.Split(changedFiles, "\n") {
						if strings.HasPrefix(strings.TrimSpace(f), ".github/") {
							pipelineChanged = true
							break
						}
					}
				}
				return nil
			},
		},
		{
			name: "Connect to server",
			fn: func() error {
				client, err := connectSSH(cfg.ServerIP, "22", cfg.SSHUser, cfg.SSHPass)
				if err != nil {
					return err
				}
				remote = &Remote{client: client, sudoPass: cfg.SudoPass}
				return nil
			},
		},
		{
			name: "Run database migration",
			fn: func() error {
				if migrationSQL == "" {
					fmt.Println("   Nothing to migrate.")
					return nil
				}
				if !confirm("   Apply the migration SQL shown above?") {
					fmt.Println(yellow("   Skipped by user."))
					return nil
				}
				return remote.runWithInput(
					fmt.Sprintf(`MYSQL_PWD='%s' mysql -u %s %s`, cfg.DBPass, cfg.DBUser, cfg.DBName),
					migrationSQL,
				)
			},
		},
		{
			name: "Merge upstream into backend fork",
			fn: func() error {
				return ghMergeUpstream(cfg.BackendFork, trackBranch)
			},
		},
		{
			name: "Merge upstream into frontend fork",
			fn: func() error {
				return ghMergeUpstream(cfg.FrontendFork, trackBranch)
			},
		},
		{
			name: "Record new deployed backend commit hash",
			fn: func() error {
				fmt.Printf("   Recording %s\n", newBackendSHA)
				return ghUpdateFile(cfg.BackendFork, deployedCommitFile, trackBranch,
					"Update last deployed upstream commit", newBackendSHA+"\n")
			},
		},
		{
			name: "Record new deployed frontend commit hash",
			fn: func() error {
				fmt.Printf("   Recording %s\n", newFrontendSHA)
				return ghUpdateFile(cfg.FrontendFork, deployedFrontendCommitFile, trackBranch,
					"Update last deployed upstream commit", newFrontendSHA+"\n")
			},
		},
	}, 0, nil)

	if remote != nil {
		remote.client.Close()
	}

	if mainGoChanged {
		fmt.Println(yellow("  ⚠  main.go changed — new API endpoints may have been added."))
		fmt.Println(yellow("     Consider running \"Import default permissions\" again to cover them."))
		fmt.Println(yellow("     (INSERT IGNORE is safe to run multiple times — existing rows are skipped.)"))
		fmt.Println()
	}
	if pipelineChanged {
		fmt.Println(yellow("  ⚠  .github/ changed — deployment workflows may have been updated upstream."))
		fmt.Println(yellow("     Consider running \"Reset deployment pipeline\" to apply the changes."))
		fmt.Println()
	}
	fmt.Println()
}
