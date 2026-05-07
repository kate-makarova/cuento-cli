package main

import (
	"fmt"
	"strings"
)

// ─── CREATE ───────────────────────────────────────────────────────────────────

// runCreate runs the full initial setup. When resumeFrom > 0 and resumeCfg is set,
// input prompts are skipped and steps before resumeFrom are marked as already done.
func runCreate(app *AppConfig, resumeFrom int, resumeCfg *Config) {
	cfg := &Config{}

	if resumeCfg != nil {
		// Resume: restore all saved inputs.
		*cfg = *resumeCfg
		fmt.Printf("  Resuming setup from step %d...\n\n", resumeFrom)
	} else {
		cfg.ProjectName = promptDefault("Project name", "cuento")
		fmt.Println()
		saved := app.Projects[cfg.ProjectName]

		if saved != nil && saved.SetupStep == -1 {
			fmt.Println(yellow("  ⚠  This project was already set up successfully."))
			fmt.Println(yellow("     All steps have been completed. Running setup again will re-execute everything."))
			fmt.Println()
			if !confirm("Proceed anyway?") {
				return
			}
			fmt.Println()
		}

		collectGitHub(cfg, saved)
		collectServer(cfg, saved)

		savedDomain := ""
		if saved != nil {
			savedDomain = saved.Domain
		}
		cfg.Domain = promptDefault("Domain name (e.g. example.com)", savedDomain)
		fmt.Println()
		collectDatabase(cfg, saved)

		fmt.Print("  Generating SSH key pair... ")
		var err error
		cfg.SSHPrivateKey, cfg.SSHPublicKey, err = generateSSHKeyPair()
		if err != nil {
			fatalExit(red("failed: " + err.Error()))
		}
		fmt.Println(green("done"))
		fmt.Println()

		// Persist inputs and mark setup as started (step 1 = next to run).
		saveConfig(app, cfg.ProjectName, cfg)
		app.Projects[cfg.ProjectName].SetupStep = 1
		updateSetupStep(cfg.ProjectName, 1)
	}

	var remote *Remote

	runSteps([]step{
		{
			name: "Ensure GitHub CLI is installed",
			fn: func() error {
				if ghInstalled() {
					v, _ := localOutput("gh", "--version")
					fmt.Printf("   already installed: %s\n", strings.SplitN(v, "\n", 2)[0])
					return nil
				}
				return installGh()
			},
		},
		{
			name: "Authenticate with GitHub",
			fn:   func() error { return authGitHub(cfg) },
		},
		{
			name: "Fork backend repository",
			fn: func() error {
				fmt.Printf("   %s → %s\n", upstreamBackend, cfg.BackendFork)
				return ghFork(upstreamBackend, cfg.ProjectName+"-backend")
			},
		},
		{
			name: "Fork frontend repository",
			fn: func() error {
				fmt.Printf("   %s → %s\n", upstreamFrontend, cfg.FrontendFork)
				return ghFork(upstreamFrontend, cfg.ProjectName+"-frontend")
			},
		},
		{
			name: "Update workflows in forks",
			fn: func() error {
				if err := ghUpdateFile(cfg.BackendFork, ".github/workflows/deploy.yml",
					"main", "Configure workflow for release branch", backendWorkflow); err != nil {
					return fmt.Errorf("backend: %w", err)
				}
				if err := ghUpdateFile(cfg.FrontendFork, ".github/workflows/main.yml",
					"main", "Configure workflow for release branch", frontendWorkflow); err != nil {
					return fmt.Errorf("frontend: %w", err)
				}
				// Disable the upstream dev pipelines so writes to main don't trigger dev deploys.
				if err := ghUpdateFile(cfg.BackendFork, ".github/workflows/deploy-dev.yml",
					"main", "Disable dev pipeline in fork", devWorkflowDisabled); err != nil {
					return fmt.Errorf("backend dev pipeline: %w", err)
				}
				if err := ghUpdateFile(cfg.FrontendFork, ".github/workflows/deploy-dev.yml",
					"main", "Disable dev pipeline in fork", devWorkflowDisabled); err != nil {
					return fmt.Errorf("frontend dev pipeline: %w", err)
				}
				if err := ghUpdateFile(cfg.BackendFork, ".github/workflows/sonic.yml",
					"main", "Add Sonic install workflow", sonicWorkflow); err != nil {
					return fmt.Errorf("sonic workflow: %w", err)
				}
				if err := ghUpdateFile(cfg.BackendFork, ".github/sonic/sonic.cfg",
					"main", "Add Sonic config", sonicCfgFile); err != nil {
					return fmt.Errorf("sonic config: %w", err)
				}
				return ghUpdateFile(cfg.BackendFork, ".github/sonic/sonic.service",
					"main", "Add Sonic service file", sonicServiceFile)
			},
		},
		{
			name: "Create release branch in forks",
			fn: func() error {
				for _, fork := range []string{cfg.BackendFork, cfg.FrontendFork} {
					fmt.Printf("   %s → release\n", fork)
					if err := ghCreateBranch(fork, trackBranch, "main"); err != nil {
						return err
					}
				}
				return nil
			},
		},
		{
			name: "Set GitHub Actions secrets",
			fn: func() error {
				for _, repo := range []string{cfg.BackendFork, cfg.FrontendFork} {
					if err := ghSetSecret(repo, "DROPLET_IP", []byte(cfg.ServerIP)); err != nil {
						return fmt.Errorf("%s DROPLET_IP: %w", repo, err)
					}
					if err := ghSetSecret(repo, "SSH_PRIVATE_KEY", cfg.SSHPrivateKey); err != nil {
						return fmt.Errorf("%s SSH_PRIVATE_KEY: %w", repo, err)
					}
				}
				return nil
			},
		},
		{
			name:   "Connect to server",
			always: true,
			fn: func() error {
				client, err := connectSSH(cfg.ServerIP, "22", cfg.SSHUser, cfg.SSHPass)
				if err != nil {
					return err
				}
				remote = &Remote{client: client, sudoPass: cfg.SudoPass}
				return nil
			},
		},
		{name: "Update system", fn: func() error { return remote.run(`apt update && apt upgrade -y`) }},
		{name: "Install dependencies", fn: func() error {
			return remote.run(`apt install -y curl git ufw debian-keyring debian-archive-keyring apt-transport-https`)
		}},
		{name: "Install MariaDB", fn: func() error {
			return remote.run(`apt install -y mariadb-server && systemctl start mariadb && systemctl enable mariadb`)
		}},
		{name: "Secure MariaDB", fn: func() error {
			input := fmt.Sprintf("\ny\n%s\n%s\ny\ny\ny\ny\n", cfg.DBRootPass, cfg.DBRootPass)
			return remote.runWithInput(`mysql_secure_installation`, input)
		}},
		{name: "Create database and user", fn: func() error {
			sql := fmt.Sprintf(
				"CREATE DATABASE IF NOT EXISTS %s;\n"+
					"CREATE USER IF NOT EXISTS '%s'@'localhost' IDENTIFIED BY '%s';\n"+
					"GRANT ALL PRIVILEGES ON %s.* TO '%s'@'localhost';\n"+
					"FLUSH PRIVILEGES;\n",
				cfg.DBName, cfg.DBUser, cfg.DBPass, cfg.DBName, cfg.DBUser)
			return remote.runWithInput(fmt.Sprintf(`mysql -u root -p%s`, cfg.DBRootPass), sql)
		}},
		{
			name: "Import default tables",
			fn: func() error {
				fmt.Println("   Fetching SQL from GitHub...")
				sql, err := ghReadFile(upstreamBackend, sqlFile, trackBranch)
				if err != nil {
					return err
				}
				return remote.runWithInput(
					fmt.Sprintf(`mysql -u root -p%s %s`, cfg.DBRootPass, cfg.DBName), sql)
			},
		},
		{name: "Install Caddy", fn: func() error {
			return remote.run(`
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list
apt update && apt install -y caddy`)
		}},
		{name: "Create app directories", fn: func() error {
			return remote.run(`
mkdir -p /var/www/frontend /var/www/backend
chown -R www-data:www-data /var/www/frontend /var/www/backend
chmod -R 755 /var/www`)
		}},
		{
			name: "Configure Caddy",
			fn: func() error {
				caddyfile := fmt.Sprintf(`%s {
    handle /ws {
        reverse_proxy 127.0.0.1:8080 {
            flush_interval -1
            transport http {
                keepalive off
            }
        }
    }

    # 1. API first (Specific path)
    handle /api/* {
        uri strip_prefix /api
        reverse_proxy 127.0.0.1:8080
    }

    # 2. Backend static files
    handle /backend/* {
        root * /var/www/backend
        file_server
    }

    # 3. Frontend (The "Catch-all")
    handle {
        root * /var/www/frontend
        file_server
        # Essential for Angular routing
        try_files {path} /index.html
    }
}
`, cfg.Domain)
				if err := remote.writeFile(caddyfile, "/etc/caddy/Caddyfile"); err != nil {
					return err
				}
				return remote.run(`systemctl reload caddy`)
			},
		},
		{name: "Create system user", fn: func() error {
			return remote.run(`id cuento &>/dev/null || useradd -r -s /bin/false cuento
usermod -aG www-data cuento`)
		}},
		{
			name: "Install backend systemd service",
			fn: func() error {
				unit := fmt.Sprintf(`[Unit]
Description=Cuento Backend Service
After=network.target mariadb.service

[Service]
User=cuento
Group=cuento
WorkingDirectory=/var/www/backend
ExecStart=/var/www/backend/cuento-backend
Restart=always
RestartSec=5
Environment="GIN_MODE=release"
Environment="DB_USER=%s"
Environment="DB_PASSWORD=%s"
Environment="DB_HOST=localhost"
Environment="DB_PORT=3306"
Environment="DB_NAME=%s"

[Install]
WantedBy=multi-user.target
`, cfg.DBUser, cfg.DBPass, cfg.DBName)
				if err := remote.writeFile(unit, "/etc/systemd/system/cuento-backend.service"); err != nil {
					return err
				}
				return remote.run(`systemctl daemon-reload && systemctl enable cuento-backend`)
			},
		},
		{name: "Configure firewall", fn: func() error {
			return remote.run(`ufw allow 22 && ufw allow 80 && ufw allow 443 && ufw --force enable`)
		}},
		{
			name: "Upload SSH public key to server",
			fn: func() error {
				pubKey := strings.TrimSpace(string(cfg.SSHPublicKey))
				return remote.run(fmt.Sprintf(`
mkdir -p ~/.ssh && chmod 700 ~/.ssh
echo '%s' >> ~/.ssh/authorized_keys
chmod 600 ~/.ssh/authorized_keys`, pubKey))
			},
		},
		{
			name: "Record deployed backend commit hash",
			fn: func() error {
				sha, err := ghGetLatestCommit(upstreamBackend, trackBranch)
				if err != nil {
					return err
				}
				fmt.Printf("   %s @ %s\n", upstreamBackend, sha)
				return ghUpdateFile(cfg.BackendFork, deployedCommitFile, trackBranch,
					"Record last deployed upstream commit", sha+"\n")
			},
		},
		{
			name: "Record deployed frontend commit hash",
			fn: func() error {
				sha, err := ghGetLatestCommit(upstreamFrontend, trackBranch)
				if err != nil {
					return err
				}
				fmt.Printf("   %s @ %s\n", upstreamFrontend, sha)
				return ghUpdateFile(cfg.FrontendFork, deployedFrontendCommitFile, trackBranch,
					"Record last deployed upstream commit", sha+"\n")
			},
		},
		{
			name: "Trigger Sonic install pipeline",
			fn: func() error {
				if err := runLocal("gh", "api",
					fmt.Sprintf("repos/%s/actions/workflows/sonic.yml/dispatches", cfg.BackendFork),
					"-X", "POST", "-f", "ref=main"); err != nil {
					return err
				}
				fmt.Println(cyan("   The search engine is being installed in the background and will be ready in 10–15 minutes."))
				return nil
			},
		},
	}, resumeFrom, func(completedStep int) {
		// Persist progress: next step to run = completedStep + 1.
		updateSetupStep(cfg.ProjectName, completedStep+1)
	})

	if remote != nil {
		remote.client.Close()
	}

	// Final config save, then mark setup complete.
	saveConfig(app, cfg.ProjectName, cfg)
	updateSetupStep(cfg.ProjectName, -1)

	fmt.Println()
	fmt.Printf("  Backend  → github.com/%s\n", cfg.BackendFork)
	fmt.Printf("  Frontend → github.com/%s\n", cfg.FrontendFork)
	fmt.Printf("  Site     → https://%s  (live once pipelines finish)\n", cfg.Domain)
	fmt.Println()
	fmt.Println(yellow("  Recommended next steps:"))
	fmt.Println(yellow("    1. Import default permissions"))
	fmt.Println(yellow("    2. Add a system user for the initial configuration"))
	fmt.Println()
}
