# Cuento CLI

A command-line tool for deploying and managing [Cuento](https://github.com/kate-makarova/cuento-backend) instances on a VPS.

## Requirements

To deploy a new Cuento instance you need:

- **GitHub account** with a personal access token (scopes: `repo`, `workflow`, `read:org`, `admin:public_key`)
- **Domain name** pointed at your server
- **VPS** running Debian/Ubuntu with at least:
  - 1 CPU
  - 1 GB RAM
  - SSH access (root or sudo user)

The CLI will install and configure MariaDB, Caddy, and all other dependencies automatically.

## Installation

Download the binary for your platform from the [latest release](https://github.com/kate-makarova/cuento-cli/releases/latest):

| Platform | File |
|----------|------|
| macOS    | `cuento-cli-mac` |
| Windows  | `cuento-cli.exe` |

On macOS, make the binary executable before running:

```bash
chmod +x cuento-cli-mac
./cuento-cli-mac
```

## Usage

Run the binary and follow the interactive prompts. On launch you will see a project selection menu where you can choose an existing project or create a new one.

---

## Commands

### Create new project

Sets up a complete Cuento deployment from scratch. You will be prompted for:

- GitHub personal access token
- Server IP address
- SSH username and password
- Domain name
- Database credentials

The setup runs the following steps automatically:

1. Fork the backend and frontend repositories to your GitHub account
2. Configure GitHub Actions workflows and set repository secrets
3. Create a `release` branch in both forks
4. Connect to the server over SSH
5. Update system packages and install dependencies
6. Install and secure MariaDB
7. Create the application database and user
8. Import default database tables
9. Install and configure Caddy as a reverse proxy
10. Create application directories and a dedicated system user
11. Install the backend as a systemd service
12. Configure the firewall (ports 22, 80, 443)
13. Upload the SSH public key to the server
14. Record the deployed commit for both backend and frontend

If the setup is interrupted at any step, re-running the CLI will offer to resume from where it left off.

---

### Update

Checks for upstream changes in the backend and frontend repositories and deploys them.

Steps performed:

1. Compare the last deployed backend commit against the latest upstream
2. If the schema changed, generate and apply a SQL migration
3. Compare the last deployed frontend commit against the latest upstream
4. Merge upstream changes into the release branches of both forks
5. Update the recorded deployed commits

After the update you may be reminded to re-run **Import default permissions** or **Reset deployment pipeline** if relevant files changed upstream.

---

### Add user

Creates a new user account in the database.

Prompts for:
- Username
- Password (hashed with SHA-256 + bcrypt before storing)
- Whether to grant admin role

---

### Import default permissions

Fetches the latest `permissions.csv` from the upstream backend repository and inserts all role-permission records into the database. Safe to run multiple times — uses `INSERT IGNORE` to skip existing entries.

---

### Reset deployment pipeline

Re-applies the current upstream GitHub Actions workflow files to both the backend and frontend forks (on both `main` and `release` branches). Use this when the upstream workflows have changed and the update command warns you about it.

---

## Configuration

Project configuration (server credentials, GitHub token, SSH keys, setup progress) is stored locally in your system config directory and is readable only by your user.
