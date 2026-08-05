package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ─── DATABASE BACKUP ──────────────────────────────────────────────────────────

func runDatabaseBackup(app *AppConfig, projectName string, saved *ProjectConfig) {
	if saved == nil {
		fatalExit(red("  ✗ No saved config for project " + projectName))
	}

	filename := fmt.Sprintf("%s-backup-%s.sql", projectName, time.Now().Format("2006-01-02-15-04-05"))
	var dump string
	var remote *Remote

	runSteps([]step{
		{
			name: "Connect to server",
			fn: func() error {
				client, err := connectSSH(saved.ServerIP, "22", saved.SSHUser, saved.SSHPass)
				if err != nil {
					return err
				}
				remote = &Remote{client: client, sudoPass: saved.SudoPass}
				return nil
			},
		},
		{
			name: "Run mysqldump",
			fn: func() error {
				var err error
				dump, err = remote.runWithOutput(
					fmt.Sprintf(`MYSQL_PWD='%s' mysqldump -u %s %s`, saved.DBPass, saved.DBUser, saved.DBName),
					"",
				)
				return err
			},
		},
		{
			name: "Save backup locally",
			fn: func() error {
				if err := os.WriteFile(filename, []byte(dump), 0600); err != nil {
					return err
				}
				abs, _ := filepath.Abs(filename)
				fmt.Printf("   Saved to %s\n", abs)
				return nil
			},
		},
	}, 0, nil)

	if remote != nil {
		remote.client.Close()
	}

	fmt.Println()
}
