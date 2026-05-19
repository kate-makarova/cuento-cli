package main

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
)

// ─── Upstream repos ───────────────────────────────────────────────────────────

const (
	upstreamBackend            = "kate-makarova/cuento-backend"
	upstreamFrontend           = "kate-makarova/cuento-frontend"
	trackBranch                = "release"
	deployedCommitFile         = "deployments/last-deployed-commit"
	deployedFrontendCommitFile = "deployments/last-deployed-commit"
	sqlFile                    = "src/Install/default_tables.sql"
)

// ─── Banner & main ────────────────────────────────────────────────────────────

func printBanner() {
	fmt.Println()
	fmt.Println(bold(colorBlue + "╔══════════════════════════════════════╗" + colorReset))
	fmt.Println(bold(colorBlue + "║          Cuento CLI 1.5.0            ║" + colorReset))
	fmt.Println(bold(colorBlue + "╚══════════════════════════════════════╝" + colorReset))
	fmt.Println()
}

func main() {
	printBanner()

	app := loadConfig()

	for {
		// Build sorted list of existing project names.
		projects := make([]string, 0, len(app.Projects))
		for name := range app.Projects {
			projects = append(projects, name)
		}
		sort.Strings(projects)

		if len(projects) > 0 {
			fmt.Println(bold("Select a project:"))
		} else {
			fmt.Println(bold("No existing projects found."))
		}
		fmt.Println()

		for i, name := range projects {
			fmt.Printf("  %d. %s\n", i+1, name)
		}
		addNewIdx := len(projects) + 1
		exitIdx := len(projects) + 2
		fmt.Printf("  %d. Add new project\n", addNewIdx)
		fmt.Printf("  %d. Exit\n", exitIdx)
		fmt.Println()

		choice := promptRequired("Enter choice")
		fmt.Println()

		n, err := strconv.Atoi(choice)
		if err != nil || n < 1 || n > exitIdx {
			fmt.Println(red("Invalid choice."))
			continue
		}

		if n == exitIdx {
			return
		}

		if n == addNewIdx {
			runCreate(app, 0, nil)
			app = loadConfig()
			continue
		}

		name := projects[n-1]
		saved := app.Projects[name]

		// If setup was interrupted mid-way, offer to resume before showing the normal menu.
		if saved.SetupStep > 0 {
			fmt.Printf(yellow("  ⚠  Setup was interrupted (completed %d steps).\n"), saved.SetupStep-1)
			if confirm("Resume setup from where it left off?") {
				resumeCfg := &Config{
					ProjectName:  name,
					GitHubToken:  saved.GitHubToken,
					GitHubUser:   saved.GitHubUser,
					BackendFork:  saved.GitHubUser + "/" + name + "-backend",
					FrontendFork: saved.GitHubUser + "/" + name + "-frontend",
					ServerIP:     saved.ServerIP,
					SSHUser:      saved.SSHUser,
					SSHPass:      saved.SSHPass,
					SudoPass:     saved.SudoPass,
					Domain:       saved.Domain,
					DBRootPass:   saved.DBRootPass,
					DBName:       saved.DBName,
					DBUser:       saved.DBUser,
					DBPass:       saved.DBPass,
				}
				if saved.SSHPrivKey != "" {
					resumeCfg.SSHPrivateKey, _ = base64.StdEncoding.DecodeString(saved.SSHPrivKey)
				}
				if saved.SSHPubKey != "" {
					resumeCfg.SSHPublicKey, _ = base64.StdEncoding.DecodeString(saved.SSHPubKey)
				}
				runCreate(app, saved.SetupStep, resumeCfg)
				app = loadConfig()
				continue
			}
			fmt.Println()
		}

		fmt.Printf(bold("Project: %s\n"), name)
		fmt.Println()
		fmt.Println("  1. Update")
		fmt.Println("  2. Add user")
		fmt.Println("  3. Import default permissions")
		fmt.Println("  4. Reset deployment pipeline")
		fmt.Println("  5. Database diagnostics")
		fmt.Println("  6. Search engine diagnostics")
		fmt.Println()

		action := promptRequired("Enter choice")
		fmt.Println()

		switch action {
		case "1":
			runUpdate(app, name, saved)
		case "2":
			runAddUser(app, name, saved)
		case "3":
			runImportPermissions(app, name, saved)
		case "4":
			runResetPipeline(app, name, saved)
		case "5":
			runDiagnostics(app, name, saved)
		case "6":
			runSonicDiagnostics(app, name, saved)
		default:
			fmt.Println(red("Invalid choice."))
		}
	}
}
