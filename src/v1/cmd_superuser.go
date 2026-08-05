package main

import (
	"fmt"
	"strings"
)

// ─── SET SUPERUSER ────────────────────────────────────────────────────────────

func runSetSuperuser(app *AppConfig, projectName string, saved *ProjectConfig) {
	if saved == nil {
		fatalExit(red("  ✗ No saved config for project " + projectName))
	}

	superuserID := promptRequired("Enter superuser ID")
	fmt.Println()

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
			name: "Set SUPERUSER_ID environment variable on server",
			fn: func() error {
				script := fmt.Sprintf(
					`grep -v '^SUPERUSER_ID=' /etc/environment > /tmp/env.tmp && mv /tmp/env.tmp /etc/environment || true
echo 'SUPERUSER_ID=%s' >> /etc/environment`,
					strings.TrimSpace(superuserID),
				)
				return remote.run(script)
			},
		},
		{
			name: "Restart backend service",
			fn: func() error {
				return remote.run(`systemctl restart cuento-backend`)
			},
		},
	}, 0, nil)

	if remote != nil {
		remote.client.Close()
	}

	fmt.Println()
}
