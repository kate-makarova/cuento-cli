package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ─── Config ───────────────────────────────────────────────────────────────────

type Config struct {
	// GitHub
	GitHubToken  string
	GitHubUser   string
	ProjectName  string
	BackendFork  string // user/name-backend
	FrontendFork string // user/name-frontend

	// Server
	ServerIP string
	SSHUser  string
	SSHPass  string
	SudoPass string
	Domain   string

	// Database
	DBRootPass string
	DBName     string
	DBUser     string
	DBPass     string

	// Generated (create mode only)
	SSHPrivateKey []byte
	SSHPublicKey  []byte
}

// ─── Persistent config ────────────────────────────────────────────────────────

// ProjectConfig holds saved credentials for a single project.
type ProjectConfig struct {
	GitHubToken string `json:"github_token"`
	GitHubUser  string `json:"github_user"`
	ServerIP    string `json:"server_ip"`
	SSHUser     string `json:"ssh_user"`
	SSHPass     string `json:"ssh_pass"`
	SudoPass    string `json:"sudo_pass"`
	Domain      string `json:"domain"`
	DBRootPass  string `json:"db_root_pass"`
	DBName      string `json:"db_name"`
	DBUser      string `json:"db_user"`
	DBPass      string `json:"db_pass"`
	// Setup resume support
	SetupStep  int    `json:"setup_step,omitempty"`   // 0 = complete; N = next step to run (1-indexed)
	SSHPrivKey string `json:"ssh_priv_key,omitempty"` // base64-encoded, kept for setup resume
	SSHPubKey  string `json:"ssh_pub_key,omitempty"`  // base64-encoded, kept for setup resume
}

// AppConfig is the root of the on-disk config file.
type AppConfig struct {
	Projects map[string]*ProjectConfig `json:"projects"`
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cuento", "config.json"), nil
}

func loadConfig() *AppConfig {
	app := &AppConfig{Projects: make(map[string]*ProjectConfig)}
	path, err := configPath()
	if err != nil {
		return app
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return app
	}
	_ = json.Unmarshal(data, app)
	if app.Projects == nil {
		app.Projects = make(map[string]*ProjectConfig)
	}
	return app
}

func saveConfig(app *AppConfig, projectName string, cfg *Config) {
	path, err := configPath()
	if err != nil {
		fmt.Println(yellow("  ⚠  Could not determine config path: " + err.Error()))
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		fmt.Println(yellow("  ⚠  Could not create config directory: " + err.Error()))
		return
	}
	app.Projects[projectName] = &ProjectConfig{
		GitHubToken: cfg.GitHubToken,
		GitHubUser:  cfg.GitHubUser,
		ServerIP:    cfg.ServerIP,
		SSHUser:     cfg.SSHUser,
		SSHPass:     cfg.SSHPass,
		SudoPass:    cfg.SudoPass,
		Domain:      cfg.Domain,
		DBRootPass:  cfg.DBRootPass,
		DBName:      cfg.DBName,
		DBUser:      cfg.DBUser,
		DBPass:      cfg.DBPass,
		SSHPrivKey:  base64.StdEncoding.EncodeToString(cfg.SSHPrivateKey),
		SSHPubKey:   base64.StdEncoding.EncodeToString(cfg.SSHPublicKey),
		// SetupStep intentionally 0: config saved at end means setup completed.
	}
	data, err := json.MarshalIndent(app, "", "  ")
	if err != nil {
		fmt.Println(yellow("  ⚠  Could not encode config: " + err.Error()))
		return
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		fmt.Println(yellow("  ⚠  Could not save config: " + err.Error()))
		return
	}
	fmt.Printf("  Credentials saved to %s\n", path)
}

// updateSetupStep persists the current setup progress without touching other fields.
func updateSetupStep(projectName string, step int) {
	path, err := configPath()
	if err != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var app AppConfig
	if err := json.Unmarshal(data, &app); err != nil {
		return
	}
	if p, ok := app.Projects[projectName]; ok {
		p.SetupStep = step
		if updated, err := json.MarshalIndent(app, "", "  "); err == nil {
			_ = os.WriteFile(path, updated, 0600)
		}
	}
}
