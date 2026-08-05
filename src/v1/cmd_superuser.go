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
			name: "Set SUPERUSER_ID in systemd service and restart",
			fn: func() error {
				script := fmt.Sprintf(
					`sed -i '/^Environment="SUPERUSER_ID=/d' /etc/systemd/system/cuento-backend.service
sed -i '/^\[Install\]/i Environment="SUPERUSER_ID=%s"' /etc/systemd/system/cuento-backend.service
systemctl daemon-reload && systemctl restart cuento-backend`,
					strings.TrimSpace(superuserID),
				)
				return remote.run(script)
			},
		},
	}, 0, nil)

	if remote != nil {
		remote.client.Close()
	}

	fmt.Println()
}
