package main

import (
	"encoding/csv"
	"fmt"
	"strings"
)

// ─── IMPORT PERMISSIONS ───────────────────────────────────────────────────────

const permissionsFile = "src/Install/permissions.csv"

func runImportPermissions(app *AppConfig, projectName string, saved *ProjectConfig) {
	if saved == nil {
		fatalExit(red("  ✗ No saved config for project " + projectName))
	}

	cfg := &Config{}
	cfg.ProjectName = projectName
	cfg.ServerIP = saved.ServerIP
	cfg.SSHUser = saved.SSHUser
	cfg.SSHPass = saved.SSHPass
	cfg.SudoPass = saved.SudoPass
	cfg.DBName = saved.DBName
	cfg.DBUser = saved.DBUser
	cfg.DBPass = saved.DBPass

	var sql string
	var recordCount int
	var remote *Remote

	runSteps([]step{
		{
			name: "Fetch permissions from GitHub",
			fn: func() error {
				fmt.Println("   Fetching permissions.csv from GitHub...")
				content, err := ghReadFile(upstreamBackend, permissionsFile, trackBranch)
				if err != nil {
					return err
				}
				records, err := csv.NewReader(strings.NewReader(content)).ReadAll()
				if err != nil {
					return fmt.Errorf("failed to parse permissions.csv: %w", err)
				}
				var stmts strings.Builder
				for _, row := range records[1:] {
					if len(row) < 3 {
						continue
					}
					fmt.Fprintf(&stmts,
						"INSERT IGNORE INTO role_permission (role_id, type, permission) VALUES (%s, %s, '%s');\n",
						row[0], row[1], row[2])
				}
				sql = stmts.String()
				recordCount = len(records) - 1
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
			name: "Import permissions",
			fn: func() error {
				fmt.Printf("   Inserting %d permissions into %s...\n", recordCount, cfg.DBName)
				return remote.runWithInput(
					fmt.Sprintf(`MYSQL_PWD='%s' mysql -u %s %s`, cfg.DBPass, cfg.DBUser, cfg.DBName),
					sql,
				)
			},
		},
	}, 0, nil)

	if remote != nil {
		remote.client.Close()
	}
	fmt.Println()
}
