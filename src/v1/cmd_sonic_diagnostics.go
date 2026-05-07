package main

import (
	"fmt"
	"strings"
)

// ─── SEARCH ENGINE DIAGNOSTICS ────────────────────────────────────────────────

func runSonicDiagnostics(app *AppConfig, projectName string, saved *ProjectConfig) {
	if saved == nil {
		fatalExit(red("  ✗ No saved config for project " + projectName))
	}

	cfg := &Config{}
	cfg.ServerIP = saved.ServerIP
	cfg.SSHUser = saved.SSHUser
	cfg.SSHPass = saved.SSHPass
	cfg.SudoPass = saved.SudoPass

	// Connect to server
	fmt.Print("\n▶  " + bold("Connecting to server") + "\n")
	client, err := connectSSH(cfg.ServerIP, "22", cfg.SSHUser, cfg.SSHPass)
	if err != nil {
		fatalExit(red("   ✗ " + err.Error()))
	}
	remote := &Remote{client: client, sudoPass: cfg.SudoPass}
	defer remote.client.Close()
	fmt.Println(green("   ✓ Done"))

	// 1. Check binary
	fmt.Print("\n▶  " + bold("Checking Sonic binary") + "\n")
	binOut, err := remote.runWithOutput(`test -f /usr/local/bin/sonic && echo yes || echo no`, "")
	if err != nil {
		fmt.Println(red("   ✗ Could not check binary: " + err.Error()))
		return
	}
	if strings.TrimSpace(binOut) != "yes" {
		fmt.Println(red("   ✗ /usr/local/bin/sonic not found"))
		fmt.Println(yellow("     Trigger the Sonic install workflow from GitHub Actions in your backend fork."))
		fmt.Println()
		return
	}
	fmt.Println(green("   ✓ /usr/local/bin/sonic found"))

	// 2. Check service file
	fmt.Print("\n▶  " + bold("Checking Sonic service file") + "\n")
	svcOut, err := remote.runWithOutput(`test -f /etc/systemd/system/sonic.service && echo yes || echo no`, "")
	if err != nil {
		fmt.Println(red("   ✗ Could not check service file: " + err.Error()))
		return
	}
	if strings.TrimSpace(svcOut) != "yes" {
		fmt.Println(yellow("   ⚠  /etc/systemd/system/sonic.service not found"))
		if confirm("   Create and start the service now?") {
			if err := remote.writeFile(sonicServiceFile, "/etc/systemd/system/sonic.service"); err != nil {
				fmt.Println(red("   ✗ Could not write service file: " + err.Error()))
				return
			}
			if err := remote.run(`systemctl daemon-reload && systemctl enable sonic && systemctl start sonic`); err != nil {
				fmt.Println(red("   ✗ Could not start service: " + err.Error()))
				return
			}
			fmt.Println(green("   ✓ Service created and started"))
		} else {
			fmt.Println(yellow("   Skipped. Sonic will not run without a service file."))
			fmt.Println()
			return
		}
	} else {
		fmt.Println(green("   ✓ Service file found"))
	}

	// 3. Check status
	fmt.Print("\n▶  " + bold("Checking Sonic service status") + "\n")
	status := sonicStatus(remote)
	fmt.Printf("   Status: %s\n", sonicStatusColored(status))

	if status == "active" {
		fmt.Println(green("   ✓ Sonic is running"))
		fmt.Println()
		return
	}

	// 4. Not active — show logs, offer restart
	fmt.Println()
	fmt.Println(bold("   Last 50 log lines:"))
	printSonicLogs(remote)
	fmt.Println()

	if !confirm("   Restart Sonic?") {
		fmt.Println()
		return
	}

	if err := remote.run(`systemctl restart sonic`); err != nil {
		fmt.Println(red("   ✗ Restart failed: " + err.Error()))
		fmt.Println()
		return
	}

	// 5. Check status again after restart
	status = sonicStatus(remote)
	fmt.Printf("   Status: %s\n", sonicStatusColored(status))

	if status == "active" {
		fmt.Println(green("   ✓ Sonic is now running"))
		fmt.Println()
		return
	}

	// Still not active — show logs and tell user to intervene
	fmt.Println()
	fmt.Println(bold("   Last 50 log lines:"))
	printSonicLogs(remote)
	fmt.Println()
	fmt.Println(red("   ✗ Sonic is still not running. Manual intervention required."))
	fmt.Println(yellow("     Connect to your server and investigate with: journalctl -u sonic -n 100"))
	fmt.Println()
}

func sonicStatus(remote *Remote) string {
	out, err := remote.runWithOutput(`systemctl is-active sonic 2>/dev/null || true`, "")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(out)
}

func sonicStatusColored(status string) string {
	switch status {
	case "active":
		return green(status)
	case "inactive", "failed":
		return red(status)
	default:
		return yellow(status)
	}
}

func printSonicLogs(remote *Remote) {
	logs, err := remote.runWithOutput(`journalctl -u sonic -n 50 --no-pager 2>/dev/null || echo "(no logs available)"`, "")
	if err != nil || strings.TrimSpace(logs) == "" {
		fmt.Println(yellow("   (could not fetch logs)"))
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		fmt.Println("   " + line)
	}
}
