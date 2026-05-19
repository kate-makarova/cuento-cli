package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ─── Common config collectors ─────────────────────────────────────────────────

func collectGitHub(cfg *Config, saved *ProjectConfig) {
	fmt.Println(bold("GitHub"))
	fmt.Println()
	fmt.Println(yellow("  Personal Access Token scopes required: repo · workflow · read:org (from admin:org) · admin:public_key"))
	fmt.Println(yellow("  Create one at: https://github.com/settings/tokens/new"))
	fmt.Println()
	var savedToken string
	if saved != nil {
		savedToken = saved.GitHubToken
	}
	cfg.GitHubToken = promptPasswordDefault("GitHub Personal Access Token", savedToken)
	fmt.Println()
}

func collectServer(cfg *Config, saved *ProjectConfig) {
	fmt.Println(bold("Server"))
	fmt.Println()
	savedIP, savedUser, savedSSH, savedSudo := "", "root", "", ""
	if saved != nil {
		savedIP = saved.ServerIP
		savedUser = saved.SSHUser
		savedSSH = saved.SSHPass
		savedSudo = saved.SudoPass
	}
	cfg.ServerIP = promptDefault("Server IP address", savedIP)
	cfg.SSHUser = promptDefault("SSH user", savedUser)
	cfg.SSHPass = promptPasswordDefault("SSH password", savedSSH)
	fmt.Println()
	cfg.SudoPass = cfg.SSHPass
	if saved != nil && savedSudo != savedSSH {
		// Previously had a different sudo pass — ask again.
		cfg.SudoPass = promptPasswordDefault("Sudo password", savedSudo)
	} else if !confirm("Use the same password for sudo?") {
		cfg.SudoPass = promptPasswordDefault("Sudo password", savedSudo)
	}
	fmt.Println()
}

func collectDatabase(cfg *Config, saved *ProjectConfig) {
	fmt.Println(bold("Database"))
	fmt.Println()
	defRoot, defName, defUser, defPass := "root", "cuento", "cuento", "cuento_password"
	if saved != nil {
		if saved.DBRootPass != "" {
			defRoot = saved.DBRootPass
		}
		if saved.DBName != "" {
			defName = saved.DBName
		}
		if saved.DBUser != "" {
			defUser = saved.DBUser
		}
		if saved.DBPass != "" {
			defPass = saved.DBPass
		}
	}
	cfg.DBRootPass = promptDefault("MariaDB root password", defRoot)
	cfg.DBName = promptDefault("Database name", defName)
	cfg.DBUser = promptDefault("Database user", defUser)
	cfg.DBPass = promptDefault("Database password", defPass)
	fmt.Println()
	if cfg.DBRootPass == "root" || cfg.DBPass == "cuento_password" {
		fmt.Println(yellow("  ⚠  Warning: default passwords — change these for production!"))
		fmt.Println()
	}
}

func runUpdateToken(app *AppConfig, projectName string, saved *ProjectConfig) {
	if saved == nil {
		fatalExit(red("  ✗ No saved config for project " + projectName))
	}

	fmt.Println(yellow("  Personal Access Token scopes required: repo · workflow · read:org (from admin:org) · admin:public_key"))
	fmt.Println(yellow("  Create one at: https://github.com/settings/tokens/new"))
	fmt.Println()

	newToken := promptPasswordDefault("New GitHub Personal Access Token", "")
	if newToken == "" {
		fmt.Println(red("  ✗ Token cannot be empty."))
		return
	}
	fmt.Println()

	cfg := &Config{
		ProjectName: projectName,
		GitHubToken: newToken,
		GitHubUser:  saved.GitHubUser,
		ServerIP:    saved.ServerIP,
		SSHUser:     saved.SSHUser,
		SSHPass:     saved.SSHPass,
		SudoPass:    saved.SudoPass,
		Domain:      saved.Domain,
		DBRootPass:  saved.DBRootPass,
		DBName:      saved.DBName,
		DBUser:      saved.DBUser,
		DBPass:      saved.DBPass,
	}

	fmt.Print("▶  " + bold("Authenticating with GitHub") + "\n")
	if err := authGitHub(cfg); err != nil {
		fmt.Println(red("   ✗ " + err.Error()))
		return
	}

	saveConfig(app, projectName, cfg)
	fmt.Println(green("   ✓ Token updated and saved"))
	fmt.Println()
}

func authGitHub(cfg *Config) error {
	// Validate the token directly before storing it, bypassing any cached gh credentials.
	cmd := exec.Command("gh", "api", "user", "--jq", ".login")
	cmd.Env = append(os.Environ(), "GH_TOKEN="+cfg.GitHubToken)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("token validation failed (token may be expired or lack required scopes): %w", err)
	}
	cfg.GitHubUser = strings.TrimSpace(string(out))
	if cfg.GitHubUser == "" {
		return fmt.Errorf("could not resolve GitHub username — check token scopes")
	}

	// Store the validated token in gh's credential store.
	login := exec.Command("gh", "auth", "login", "--with-token")
	login.Stdin = strings.NewReader(cfg.GitHubToken + "\n")
	login.Stdout = os.Stdout
	login.Stderr = os.Stderr
	if err := login.Run(); err != nil {
		return err
	}

	cfg.BackendFork = cfg.GitHubUser + "/" + cfg.ProjectName + "-backend"
	cfg.FrontendFork = cfg.GitHubUser + "/" + cfg.ProjectName + "-frontend"
	fmt.Printf("   Logged in as %s\n", bold(cfg.GitHubUser))
	return nil
}
