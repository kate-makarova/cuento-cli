package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// ─── ADD USER ─────────────────────────────────────────────────────────────────

func runAddUser(app *AppConfig, projectName string, saved *ProjectConfig) {
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

	fmt.Println(bold("New Cuento user"))
	fmt.Println()
	username := promptRequired("Username")
	password := promptPasswordDefault("Password", "")
	isAdmin := confirm("Make this user an admin?")
	fmt.Println()

	// SHA-256 first (mirrors the frontend), then bcrypt — same as the backend receives.
	sha256Hash := sha256.Sum256([]byte(password))
	sha256Hex := hex.EncodeToString(sha256Hash[:])
	hashed, err := bcrypt.GenerateFromPassword([]byte(sha256Hex), 14)
	if err != nil {
		fatalExit(red("  ✗ Failed to hash password: " + err.Error()))
	}

	var remote *Remote

	runSteps([]step{
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
			name: "Insert user into database",
			fn: func() error {
				roleInsert := "INSERT INTO user_role (user_id, role_id) SELECT @uid, id FROM roles WHERE name = 'user';\n"
				if isAdmin {
					roleInsert += "INSERT INTO user_role (user_id, role_id) SELECT @uid, id FROM roles WHERE name = 'admin';\n"
				}
				sql := fmt.Sprintf(
					"INSERT INTO users (username, password, date_registered, interface_language, interface_timezone)"+
						" VALUES ('%s', '%s', NOW(), 'en-US', 'Europe/London');\n"+
						"SET @uid = LAST_INSERT_ID();\n"+
						roleInsert+
						"UPDATE global_stats SET stat_value = stat_value + 1 WHERE stat_name = 'total_user_number';\n"+
						"UPDATE global_stats SET stat_value = @uid, stat_secondary = '%s' WHERE stat_name = 'last_user';\n",
					username, string(hashed), username,
				)
				return remote.runWithInput(
					fmt.Sprintf(`MYSQL_PWD='%s' mysql -u %s %s`, cfg.DBPass, cfg.DBUser, cfg.DBName), sql)
			},
		},
	}, 0, nil)

	if remote != nil {
		remote.client.Close()
	}

	fmt.Printf("\n  User %s created.\n\n", bold(username))
}
