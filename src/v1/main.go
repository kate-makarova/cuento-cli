package main

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// ─── ANSI colours ─────────────────────────────────────────────────────────────

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
)

// colorsEnabled is set at startup based on whether the terminal supports ANSI.
// Windows CMD and PowerShell on versions before Windows 10 do not support ANSI
// escape codes and will render them as raw text.
var colorsEnabled = runtime.GOOS != "windows"

func bold(s string) string {
	if colorsEnabled {
		return colorBold + s + colorReset
	}
	return s
}
func green(s string) string {
	if colorsEnabled {
		return colorGreen + s + colorReset
	}
	return s
}
func red(s string) string {
	if colorsEnabled {
		return colorRed + s + colorReset
	}
	return s
}
func yellow(s string) string {
	if colorsEnabled {
		return colorYellow + s + colorReset
	}
	return s
}
func cyan(s string) string {
	if colorsEnabled {
		return colorCyan + s + colorReset
	}
	return s
}

// ─── Upstream repos ───────────────────────────────────────────────────────────

const (
	upstreamBackend            = "kate-makarova/cuento-backend"
	upstreamFrontend           = "kate-makarova/cuento-frontend"
	trackBranch                = "release"
	deployedCommitFile         = "deployments/last-deployed-commit"
	deployedFrontendCommitFile = "deployments/last-deployed-commit"
	sqlFile                    = "src/Install/default_tables.sql"
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

// ─── Prompts ──────────────────────────────────────────────────────────────────

var reader = bufio.NewReader(os.Stdin)

func promptDefault(q, def string) string {
	fmt.Printf("%s%s%s [%s]: ", cyan("? "), q, colorReset, yellow(def))
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func promptRequired(q string) string {
	for {
		fmt.Print(cyan("? ") + q + ": ")
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
		fmt.Println(yellow("  This field is required."))
	}
}

func promptPassword(q string) string {
	// term.ReadPassword uses raw console mode which is unreliable on old Windows
	// (8.1 and earlier). Fall back to plain input on Windows; the token is not
	// echoed on Unix-like systems but will be visible on legacy Windows consoles.
	if runtime.GOOS != "windows" && term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Print(cyan("? ") + q + ": ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err == nil {
			return string(b)
		}
	}
	fmt.Print(cyan("? ") + q + " (input visible): ")
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line)
}

func promptPasswordDefault(q, saved string) string {
	hint := ""
	if saved != "" {
		hint = " [saved, Enter to keep]"
	}
	if runtime.GOOS != "windows" && term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Print(cyan("? ") + q + hint + ": ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err == nil {
			if string(b) == "" {
				return saved
			}
			return string(b)
		}
	}
	fmt.Print(cyan("? ") + q + hint + " (input visible): ")
	line, _ := reader.ReadString('\n')
	val := strings.TrimSpace(line)
	if val == "" {
		return saved
	}
	return val
}

func confirm(q string) bool {
	fmt.Print(cyan("? ") + q + " [y/N]: ")
	line, _ := reader.ReadString('\n')
	return strings.EqualFold(strings.TrimSpace(line), "y")
}

// ─── Shell helpers ────────────────────────────────────────────────────────────

func runLocal(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func localOutput(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

// ─── gh tool ──────────────────────────────────────────────────────────────────

func ghInstalled() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

func installGh() error {
	switch runtime.GOOS {
	case "darwin":
		return runLocal("brew", "install", "gh")
	case "linux":
		cmd := exec.Command("bash", "-c", `
curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
  | sudo dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg
sudo chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] \
  https://cli.github.com/packages stable main" \
  | sudo tee /etc/apt/sources.list.d/github-cli.list
sudo apt-get update && sudo apt-get install -y gh
`)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	case "windows":
		return fmt.Errorf("please install gh from https://cli.github.com and re-run this installer")
	default:
		return fmt.Errorf("unsupported OS %s — install gh manually", runtime.GOOS)
	}
}

func ghAPI(endpoint string, extraArgs ...string) (map[string]any, error) {
	args := append([]string{"api", endpoint}, extraArgs...)
	out, err := exec.Command("gh", args...).Output()
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func ghFork(upstream, forkName string) error {
	return runLocal("gh", "repo", "fork", upstream, "--fork-name", forkName, "--clone=false")
}

func ghSetSecret(repo, name string, value []byte) error {
	cmd := exec.Command("gh", "secret", "set", name, "-R", repo)
	cmd.Stdin = bytes.NewReader(value)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ghUpdateFile creates or updates a file in a repo on the given branch via the GitHub API.
func ghUpdateFile(repo, path, branch, commitMsg, content string) error {
	var sha string
	if data, err := ghAPI(fmt.Sprintf("repos/%s/contents/%s?ref=%s", repo, path, branch)); err == nil {
		sha, _ = data["sha"].(string)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	args := []string{"api", fmt.Sprintf("repos/%s/contents/%s", repo, path),
		"-X", "PUT", "-f", "message=" + commitMsg, "-f", "content=" + encoded, "-f", "branch=" + branch}
	if sha != "" {
		args = append(args, "-f", "sha="+sha)
	}
	cmd := exec.Command("gh", args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ghReadFile fetches a text file from a repo at an optional ref (empty = default branch).
func ghReadFile(repo, path, ref string) (string, error) {
	endpoint := fmt.Sprintf("repos/%s/contents/%s", repo, path)
	if ref != "" {
		endpoint += "?ref=" + ref
	}
	var stderr bytes.Buffer
	cmd := exec.Command("gh", "api", endpoint, "--jq", ".content")
	cmd.Env = append(os.Environ(), "GH_TOKEN="+os.Getenv("GH_TOKEN"))
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, strings.TrimSpace(stderr.String()))
	}
	raw := strings.ReplaceAll(strings.TrimSpace(string(out)), "\n", "")
	decoded, err := base64.StdEncoding.DecodeString(raw)
	return string(decoded), err
}

func ghGetLatestCommit(repo, branch string) (string, error) {
	return localOutput("gh", "api",
		fmt.Sprintf("repos/%s/git/ref/heads/%s", repo, branch),
		"--jq", ".object.sha",
	)
}

// ghCreateBranch creates branch in repo from the tip of fromBranch.
// If the branch already exists the error is silently ignored.
func ghCreateBranch(repo, branch, fromBranch string) error {
	sha, err := ghGetLatestCommit(repo, fromBranch)
	if err != nil {
		return fmt.Errorf("resolving %s in %s: %w", fromBranch, repo, err)
	}
	cmd := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/git/refs", repo),
		"-X", "POST",
		"-f", "ref=refs/heads/"+branch,
		"-f", "sha="+sha)
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	// 422 means the branch already exists — that's fine.
	if err != nil && !strings.Contains(stderr.String(), "422") {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// ghMergeUpstream pulls the latest upstream into a fork branch via the API.
func ghMergeUpstream(fork, branch string) error {
	cmd := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/merge-upstream", fork),
		"-X", "POST", "-f", "branch="+branch)
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ─── SSH remote execution ─────────────────────────────────────────────────────

type Remote struct {
	client   *ssh.Client
	sudoPass string
}

func connectSSH(host, port, user, pass string) (*ssh.Client, error) {
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec
	}
	return ssh.Dial("tcp", net.JoinHostPort(host, port), cfg)
}

func (r *Remote) run(script string) error {
	sess, err := r.client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	sess.Stdout = os.Stdout
	sess.Stderr = os.Stderr
	if strings.Contains(script, "sudo") {
		sess.Stdin = strings.NewReader(r.sudoPass + "\n")
		script = "sudo -S bash -s <<'SCRIPT'\n" + script + "\nSCRIPT"
	}
	return sess.Run(script)
}

func (r *Remote) runWithInput(script, stdin string) error {
	sess, err := r.client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	sess.Stdout = os.Stdout
	sess.Stderr = os.Stderr
	var buf bytes.Buffer
	if strings.Contains(script, "sudo") {
		buf.WriteString(r.sudoPass + "\n")
	}
	buf.WriteString(stdin)
	sess.Stdin = &buf
	return sess.Run(script)
}

func (r *Remote) runWithOutput(script, stdin string) (string, error) {
	sess, err := r.client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	var out bytes.Buffer
	sess.Stdout = &out
	sess.Stderr = os.Stderr
	var buf bytes.Buffer
	if strings.Contains(script, "sudo") {
		buf.WriteString(r.sudoPass + "\n")
	}
	if stdin != "" {
		buf.WriteString(stdin)
	}
	if buf.Len() > 0 {
		sess.Stdin = &buf
	}
	if err := sess.Run(script); err != nil {
		return "", err
	}
	return out.String(), nil
}

func (r *Remote) writeFile(content, remotePath string) error {
	// Step 1: write content to a temp file without sudo (no password mixing).
	tmp := "/tmp/cuento_write_tmp"
	sess, err := r.client.NewSession()
	if err != nil {
		return err
	}
	sess.Stdin = strings.NewReader(content)
	sess.Stdout = os.Stdout
	sess.Stderr = os.Stderr
	if err := sess.Run(fmt.Sprintf("cat > %s", tmp)); err != nil {
		sess.Close()
		return err
	}
	sess.Close()

	// Step 2: move into place with sudo (stdin carries only the password).
	return r.run(fmt.Sprintf("sudo mv %s %s", tmp, remotePath))
}

// ─── Key generation ───────────────────────────────────────────────────────────

func generateSSHKeyPair() (privPEM []byte, pubAuthorizedKey []byte, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return nil, nil, err
	}
	privPEM = pem.EncodeToMemory(block)
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, nil, err
	}
	return privPEM, ssh.MarshalAuthorizedKey(sshPub), nil
}

// ─── SQL migration generator ──────────────────────────────────────────────────

var reCreateTable = regexp.MustCompile(
	`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?` +
		"(?:`?(\\w+)`?)" +
		`\s*\(([\s\S]+?)\)\s*(?:ENGINE\b|;)`,
)

var reColumnName = regexp.MustCompile("^\\s*`?(\\w+)`?\\s+")
var rePKCols = regexp.MustCompile(`(?i)primary\s+key\s*\(([^)]+)\)`)

// parseTables returns map[tableName]map[columnName]fullColumnDef.
func parseTables(sql string) map[string]map[string]string {
	tables := make(map[string]map[string]string)
	for _, m := range reCreateTable.FindAllStringSubmatch(sql, -1) {
		name := strings.ToLower(m[1])
		cols := make(map[string]string)
		body := m[2]
		var pkCols []string // columns that must be NOT NULL due to PK
		var lastCol string  // last parsed column name (for bare inline "primary key")
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(strings.TrimRight(line, ","))
			if line == "" {
				continue
			}
			upper := strings.ToUpper(line)
			// Bare "primary key" continuation (no parens) — belongs to the
			// column defined on the preceding line.
			if upper == "PRIMARY KEY" {
				if lastCol != "" {
					pkCols = append(pkCols, lastCol)
				}
				continue
			}
			// Skip constraint / index lines. Use word-boundary checks so column
			// names like key_author or index_col are not accidentally skipped.
			if strings.HasPrefix(upper, "PRIMARY ") ||
				strings.HasPrefix(upper, "UNIQUE ") || strings.HasPrefix(upper, "UNIQUE\t") ||
				upper == "KEY" || strings.HasPrefix(upper, "KEY ") || strings.HasPrefix(upper, "KEY\t") || strings.HasPrefix(upper, "KEY`") ||
				strings.HasPrefix(upper, "INDEX ") || strings.HasPrefix(upper, "INDEX\t") ||
				strings.HasPrefix(upper, "CONSTRAINT ") ||
				strings.HasPrefix(upper, "FOREIGN ") {
				continue
			}
			if cm := reColumnName.FindStringSubmatch(line); len(cm) > 1 {
				lastCol = strings.ToLower(cm[1])
				cols[lastCol] = line
			}
		}
		// Also collect PK columns from standalone PRIMARY KEY (...) lines.
		if pm := rePKCols.FindStringSubmatch(strings.ReplaceAll(body, "\n", " ")); pm != nil {
			for _, pkCol := range strings.Split(pm[1], ",") {
				pkCols = append(pkCols, strings.ToLower(strings.Trim(strings.TrimSpace(pkCol), "`")))
			}
		}
		// Primary key columns are implicitly NOT NULL — enforce it.
		for _, pk := range pkCols {
			if def, ok := cols[pk]; ok {
				upper := strings.ToUpper(def)
				if !strings.Contains(upper, "NOT NULL") {
					if strings.Contains(upper, " NULL") {
						def = regexp.MustCompile(`(?i)\bnull\b`).ReplaceAllString(def, "NOT NULL")
					} else {
						def += " NOT NULL"
					}
					cols[pk] = def
				}
			}
		}
		tables[name] = cols
	}
	return tables
}

// extractInserts returns all INSERT statements from sql (single- or multi-line).
// Each returned string is the full statement, whitespace-normalised, up to and
// including the terminating semicolon.
func extractInserts(sql string) []string {
	var inserts []string
	var current strings.Builder
	inInsert := false

	for _, line := range strings.Split(sql, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inInsert {
			if strings.HasPrefix(strings.ToUpper(trimmed), "INSERT") {
				inInsert = true
				current.Reset()
			} else {
				continue
			}
		}
		if current.Len() > 0 {
			current.WriteByte(' ')
		}
		current.WriteString(trimmed)
		if strings.HasSuffix(trimmed, ";") {
			inserts = append(inserts, current.String())
			inInsert = false
		}
	}
	return inserts
}

// generateMigration compares two SQL schemas and returns the migration SQL needed
// to bring the old schema up to the new one (new tables, new columns, new inserts).
func generateMigration(oldSQL, newSQL string) string {
	oldTables := parseTables(oldSQL)
	newTables := parseTables(newSQL)

	var stmts []string

	for tableName, newCols := range newTables {
		oldCols, exists := oldTables[tableName]
		if !exists {
			// Entire table is new — emit the full CREATE TABLE block
			if m := reCreateTable.FindStringSubmatch(newSQL); m != nil {
				for _, mm := range reCreateTable.FindAllStringSubmatch(newSQL, -1) {
					if strings.ToLower(mm[1]) == tableName {
						stmts = append(stmts, mm[0]+";\n")
						break
					}
				}
			}
			continue
		}
		// Table exists — find new or changed columns
		for colName, colDef := range newCols {
			if oldDef, known := oldCols[colName]; !known {
				stmts = append(stmts,
					fmt.Sprintf("ALTER TABLE `%s` ADD COLUMN %s;\n", tableName, colDef))
			} else if strings.TrimSpace(oldDef) != strings.TrimSpace(colDef) {
				stmts = append(stmts,
					fmt.Sprintf("ALTER TABLE `%s` MODIFY COLUMN %s;\n", tableName, colDef))
			}
		}
	}

	// Find INSERT statements present in new but not in old
	oldInserts := make(map[string]bool)
	for _, ins := range extractInserts(oldSQL) {
		oldInserts[ins] = true
	}
	for _, ins := range extractInserts(newSQL) {
		if !oldInserts[ins] {
			stmts = append(stmts, ins+"\n")
		}
	}

	return strings.Join(stmts, "\n")
}

// ─── Workflow templates ───────────────────────────────────────────────────────

const backendWorkflow = `name: Build and Deploy Cuento

on:
  push:
    branches: [release]
    paths-ignore:
      - 'deployments/**'
  workflow_dispatch:

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout code
        uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.25'
          cache: true

      - name: Build Binary
        run: GOOS=linux GOARCH=amd64 go build -o cuento-backend main.go

      - name: Copy binary to server
        uses: appleboy/scp-action@v0.1.7
        with:
          host: ${{ secrets.DROPLET_IP }}
          username: root
          key: ${{ secrets.SSH_PRIVATE_KEY }}
          source: "cuento-backend"
          target: "/var/www/backend"

      - name: Copy locales to server
        uses: appleboy/scp-action@v0.1.7
        with:
          host: ${{ secrets.DROPLET_IP }}
          username: root
          key: ${{ secrets.SSH_PRIVATE_KEY }}
          source: "locales"
          target: "/var/www/backend"

      - name: Restart service
        uses: appleboy/ssh-action@v1.0.3
        with:
          host: ${{ secrets.DROPLET_IP }}
          username: root
          key: ${{ secrets.SSH_PRIVATE_KEY }}
          script: |
            chmod +x /var/www/backend/cuento-backend
            systemctl restart cuento-backend
`

// devWorkflowDisabled replaces the upstream dev pipeline in a fork so that
// writes to main (e.g. workflow resets) do not trigger unintended dev deploys.
const devWorkflowDisabled = `name: Dev pipeline (disabled in fork)

on:
  workflow_dispatch:

jobs:
  disabled:
    runs-on: ubuntu-latest
    steps:
      - name: Disabled
        run: echo "Dev pipeline is disabled in this fork. Use the release branch."
`

const frontendWorkflow = `name: Deploy Cuento Frontend

on:
  push:
    branches: [release]
    paths-ignore:
      - 'deployments/**'
  workflow_dispatch:

jobs:
  build-and-deploy:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout code
        uses: actions/checkout@v4

      - name: Set up Node.js
        uses: actions/setup-node@v4
        with:
          node-version: '20'
          cache: 'npm'

      - name: Install dependencies
        run: npm ci

      - name: Build Angular app
        run: npm run build -- --configuration production

      - name: Clear old files (preserving custom styles and uploads)
        uses: appleboy/ssh-action@v1.0.3
        with:
          host: ${{ secrets.DROPLET_IP }}
          username: root
          key: ${{ secrets.SSH_PRIVATE_KEY }}
          script: |
            find /var/www/frontend -mindepth 1 \
              ! -name 'favicon*' \
              ! -name 'main_style*' \
              ! -name 'custom_style*' \
              ! -path '/var/www/frontend/reactions' \
              ! -path '/var/www/frontend/reactions/*' \
              -delete
            mkdir -p /var/www/frontend/reactions

      - name: Deploy to temporary staging
        uses: appleboy/scp-action@v0.1.7
        with:
          host: ${{ secrets.DROPLET_IP }}
          username: root
          key: ${{ secrets.SSH_PRIVATE_KEY }}
          source: "dist/cuento-frontend/browser/*"
          target: "/tmp/frontend_staging"
          strip_components: 3
          overwrite: true

      - name: Move to target and fix permissions
        uses: appleboy/ssh-action@v1.0.3
        with:
          host: ${{ secrets.DROPLET_IP }}
          username: root
          key: ${{ secrets.SSH_PRIVATE_KEY }}
          script: |
            cp -rn /tmp/frontend_staging/* /var/www/frontend/
            chown -R www-data:www-data /var/www/frontend
            chmod -R 775 /var/www/frontend
            rm -rf /tmp/frontend_staging
`

// ─── Step runner ──────────────────────────────────────────────────────────────

type step struct {
	name   string
	fn     func() error
	always bool // run even when skipping (e.g. connect to server)
}

// runSteps executes steps sequentially. startFrom (1-indexed) skips earlier steps
// unless they are marked always. onStepDone is called with the 1-indexed step number
// after each step completes (use nil when progress tracking is not needed).
// fatalExit prints an error and exits. On Windows it pauses so the user can
// read the message before the console window closes.
func fatalExit(msg string) {
	fmt.Println(msg)
	if runtime.GOOS == "windows" {
		fmt.Println("\nPress Enter to exit...")
		_, _ = reader.ReadString('\n')
	}
	os.Exit(1)
}

func runSteps(steps []step, startFrom int, onStepDone func(int)) {
	passed, skipped := 0, 0
	for i, s := range steps {
		stepNum := i + 1
		skip := stepNum < startFrom && !s.always
		if skip {
			fmt.Printf("\n▶  %s %s\n", s.name, cyan("(already done)"))
			skipped++
			continue
		}
		label := s.name
		if stepNum < startFrom && s.always {
			label += " (prerequisite)"
		}
		fmt.Printf("\n▶  %s\n", bold(label))
		if err := s.fn(); err != nil {
			fatalExit(red("   ✗ "+err.Error()) + red("\nStopped."))
		}
		fmt.Println(green("   ✓ Done"))
		passed++
		if onStepDone != nil && stepNum >= startFrom {
			onStepDone(stepNum)
		}
	}
	fmt.Println()
	fmt.Println(bold("─────────── Done ───────────"))
	if skipped > 0 {
		fmt.Printf("  %s  %d steps completed, %d skipped\n", green("✓"), passed, skipped)
	} else {
		fmt.Printf("  %s  %d steps completed\n", green("✓"), passed)
	}
	fmt.Println("────────────────────────────")
}

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

func authGitHub(cfg *Config) error {
	cmd := exec.Command("gh", "auth", "login", "--with-token")
	cmd.Stdin = strings.NewReader(cfg.GitHubToken + "\n")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	var err error
	cfg.GitHubUser, err = localOutput("gh", "api", "user", "--jq", ".login")
	if err != nil {
		return fmt.Errorf("could not resolve GitHub username: %w", err)
	}
	cfg.BackendFork = cfg.GitHubUser + "/" + cfg.ProjectName + "-backend"
	cfg.FrontendFork = cfg.GitHubUser + "/" + cfg.ProjectName + "-frontend"
	fmt.Printf("   Logged in as %s\n", bold(cfg.GitHubUser))
	return nil
}

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

// ─── UPDATE ───────────────────────────────────────────────────────────────────

func runUpdate(app *AppConfig, projectName string, saved *ProjectConfig) {
	if saved == nil {
		fatalExit(red("  ✗ No saved config for project " + projectName))
	}

	cfg := &Config{}
	cfg.ProjectName = projectName
	cfg.GitHubToken = saved.GitHubToken
	cfg.GitHubUser = saved.GitHubUser
	cfg.BackendFork = saved.GitHubUser + "/" + projectName + "-backend"
	cfg.FrontendFork = saved.GitHubUser + "/" + projectName + "-frontend"
	cfg.ServerIP = saved.ServerIP
	cfg.SSHUser = saved.SSHUser
	cfg.SSHPass = saved.SSHPass
	cfg.SudoPass = saved.SudoPass
	cfg.DBRootPass = saved.DBRootPass
	cfg.DBName = saved.DBName
	cfg.DBUser = saved.DBUser
	cfg.DBPass = saved.DBPass

	if cfg.GitHubToken != "" {
		os.Setenv("GH_TOKEN", cfg.GitHubToken)
	}

	user, err := localOutput("gh", "api", "user", "--jq", ".login")
	if err != nil || user == "" {
		fatalExit(red("  ✗ Could not resolve GitHub user: " + err.Error()))
	}
	cfg.GitHubUser = user
	cfg.BackendFork = cfg.GitHubUser + "/" + projectName + "-backend"
	cfg.FrontendFork = cfg.GitHubUser + "/" + projectName + "-frontend"

	var remote *Remote

	// SQL diff and commit SHAs are computed mid-flow; captured here for later steps.
	var migrationSQL string
	var mainGoChanged bool
	var pipelineChanged bool
	var newBackendSHA, newFrontendSHA string

	runSteps([]step{
		{
			name: "Read last deployed backend commit",
			fn: func() error {
				content, err := ghReadFile(cfg.BackendFork, deployedCommitFile, trackBranch)
				if err != nil {
					return fmt.Errorf("could not read %s — has this project been set up with 'create'? %w",
						deployedCommitFile, err)
				}
				lastSHA := strings.TrimSpace(content)
				newBackendSHA, err = ghGetLatestCommit(upstreamBackend, trackBranch)
				if err != nil {
					return err
				}
				fmt.Printf("   Deployed : %s\n", lastSHA)
				fmt.Printf("   Latest   : %s\n", newBackendSHA)

				if lastSHA == newBackendSHA {
					fmt.Println(green("   Already up to date — no SQL migration needed."))
					return nil
				}

				// Fetch SQL at both commits and generate migration
				fmt.Println("   Comparing SQL schemas...")
				oldSQL, err := ghReadFile(upstreamBackend, sqlFile, lastSHA)
				if err != nil {
					return fmt.Errorf("fetching SQL at deployed commit: %w", err)
				}
				newSQL, err := ghReadFile(upstreamBackend, sqlFile, newBackendSHA)
				if err != nil {
					return fmt.Errorf("fetching SQL at latest commit: %w", err)
				}

				migrationSQL = generateMigration(oldSQL, newSQL)
				if migrationSQL == "" {
					fmt.Println(green("   SQL schema unchanged — no migration needed."))
				} else {
					fmt.Println(yellow("   Migration SQL generated:"))
					fmt.Println()
					for _, line := range strings.Split(strings.TrimSpace(migrationSQL), "\n") {
						fmt.Println("   " + line)
					}
					fmt.Println()
				}

				// Check if main.go or .github/ changed
				changedFiles, err := localOutput("gh", "api",
					fmt.Sprintf("repos/%s/compare/%s...%s", upstreamBackend, lastSHA, newBackendSHA),
					"--jq", ".files[].filename")
				if err == nil {
					for _, f := range strings.Split(changedFiles, "\n") {
						f = strings.TrimSpace(f)
						if f == "main.go" {
							mainGoChanged = true
						}
						if strings.HasPrefix(f, ".github/") {
							pipelineChanged = true
						}
					}
				}

				return nil
			},
		},
		{
			name: "Read last deployed frontend commit",
			fn: func() error {
				content, err := ghReadFile(cfg.FrontendFork, deployedFrontendCommitFile, trackBranch)
				if err != nil {
					// File doesn't exist yet (project set up before this feature).
					// Record current upstream commit and skip diff detection.
					fmt.Println(yellow("   Commit file not found — initialising frontend tracking."))
					newFrontendSHA, err = ghGetLatestCommit(upstreamFrontend, trackBranch)
					if err != nil {
						return err
					}
					fmt.Printf("   Recording %s\n", newFrontendSHA)
					return ghUpdateFile(cfg.FrontendFork, deployedFrontendCommitFile, trackBranch,
						"Record last deployed upstream commit", newFrontendSHA+"\n")
				}
				lastSHA := strings.TrimSpace(content)
				newFrontendSHA, err = ghGetLatestCommit(upstreamFrontend, trackBranch)
				if err != nil {
					return err
				}
				fmt.Printf("   Deployed : %s\n", lastSHA)
				fmt.Printf("   Latest   : %s\n", newFrontendSHA)
				if lastSHA == newFrontendSHA {
					fmt.Println(green("   Already up to date."))
					return nil
				}
				changedFiles, err := localOutput("gh", "api",
					fmt.Sprintf("repos/%s/compare/%s...%s", upstreamFrontend, lastSHA, newFrontendSHA),
					"--jq", ".files[].filename")
				if err == nil {
					for _, f := range strings.Split(changedFiles, "\n") {
						if strings.HasPrefix(strings.TrimSpace(f), ".github/") {
							pipelineChanged = true
							break
						}
					}
				}
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
			name: "Run database migration",
			fn: func() error {
				if migrationSQL == "" {
					fmt.Println("   Nothing to migrate.")
					return nil
				}
				if !confirm("   Apply the migration SQL shown above?") {
					fmt.Println(yellow("   Skipped by user."))
					return nil
				}
				return remote.runWithInput(
					fmt.Sprintf(`MYSQL_PWD='%s' mysql -u %s %s`, cfg.DBPass, cfg.DBUser, cfg.DBName),
					migrationSQL,
				)
			},
		},
		{
			name: "Merge upstream into backend fork",
			fn: func() error {
				return ghMergeUpstream(cfg.BackendFork, trackBranch)
			},
		},
		{
			name: "Merge upstream into frontend fork",
			fn: func() error {
				return ghMergeUpstream(cfg.FrontendFork, trackBranch)
			},
		},
		{
			name: "Record new deployed backend commit hash",
			fn: func() error {
				fmt.Printf("   Recording %s\n", newBackendSHA)
				return ghUpdateFile(cfg.BackendFork, deployedCommitFile, trackBranch,
					"Update last deployed upstream commit", newBackendSHA+"\n")
			},
		},
		{
			name: "Record new deployed frontend commit hash",
			fn: func() error {
				fmt.Printf("   Recording %s\n", newFrontendSHA)
				return ghUpdateFile(cfg.FrontendFork, deployedFrontendCommitFile, trackBranch,
					"Update last deployed upstream commit", newFrontendSHA+"\n")
			},
		},
	}, 0, nil)

	if remote != nil {
		remote.client.Close()
	}

	if mainGoChanged {
		fmt.Println(yellow("  ⚠  main.go changed — new API endpoints may have been added."))
		fmt.Println(yellow("     Consider running \"Import default permissions\" again to cover them."))
		fmt.Println(yellow("     (INSERT IGNORE is safe to run multiple times — existing rows are skipped.)"))
		fmt.Println()
	}
	if pipelineChanged {
		fmt.Println(yellow("  ⚠  .github/ changed — deployment workflows may have been updated upstream."))
		fmt.Println(yellow("     Consider running \"Reset deployment pipeline\" to apply the changes."))
		fmt.Println()
	}
	fmt.Println()
}

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

// ─── RESET PIPELINE ───────────────────────────────────────────────────────────

func runResetPipeline(app *AppConfig, projectName string, saved *ProjectConfig) {
	if saved == nil {
		fatalExit(red("  ✗ No saved config for project " + projectName))
	}

	cfg := &Config{}
	cfg.ProjectName = projectName
	cfg.GitHubToken = saved.GitHubToken

	runSteps([]step{
		{
			name: "Authenticate with GitHub",
			fn:   func() error { return authGitHub(cfg) },
		},
		{
			name: "Update backend workflow",
			fn: func() error {
				for _, branch := range []string{"main", "release"} {
					if err := ghUpdateFile(cfg.BackendFork, ".github/workflows/deploy.yml",
						branch, "Reset deployment workflow", backendWorkflow); err != nil {
						return fmt.Errorf("branch %s: %w", branch, err)
					}
				}
				return nil
			},
		},
		{
			name: "Update frontend workflow",
			fn: func() error {
				for _, branch := range []string{"main", "release"} {
					if err := ghUpdateFile(cfg.FrontendFork, ".github/workflows/main.yml",
						branch, "Reset deployment workflow", frontendWorkflow); err != nil {
						return fmt.Errorf("branch %s: %w", branch, err)
					}
				}
				return nil
			},
		},
		{
			name: "Disable dev pipelines in forks",
			fn: func() error {
				if err := ghUpdateFile(cfg.BackendFork, ".github/workflows/deploy-dev.yml",
					"main", "Disable dev pipeline in fork", devWorkflowDisabled); err != nil {
					return fmt.Errorf("backend: %w", err)
				}
				return ghUpdateFile(cfg.FrontendFork, ".github/workflows/deploy-dev.yml",
					"main", "Disable dev pipeline in fork", devWorkflowDisabled)
			},
		},
		{
			name: "Update Sonic workflow",
			fn: func() error {
				if err := ghUpdateFile(cfg.BackendFork, ".github/workflows/sonic.yml",
					"main", "Reset Sonic install workflow", sonicWorkflow); err != nil {
					return fmt.Errorf("sonic workflow: %w", err)
				}
				if err := ghUpdateFile(cfg.BackendFork, ".github/sonic/sonic.cfg",
					"main", "Reset Sonic config", sonicCfgFile); err != nil {
					return fmt.Errorf("sonic config: %w", err)
				}
				return ghUpdateFile(cfg.BackendFork, ".github/sonic/sonic.service",
					"main", "Reset Sonic service file", sonicServiceFile)
			},
		},
	}, 0, nil)

	fmt.Println()
}

// ─── DATABASE DIAGNOSTICS ─────────────────────────────────────────────────────

type riskLevel int

const (
	riskNone    riskLevel = 0
	riskMinimal riskLevel = 1
	riskLow     riskLevel = 2
	riskMedium  riskLevel = 3
	riskHigh    riskLevel = 4
)

func (r riskLevel) label() string {
	switch r {
	case riskMinimal:
		return "Minimal"
	case riskLow:
		return "Low"
	case riskMedium:
		return "Medium"
	case riskHigh:
		return "High"
	default:
		return "—"
	}
}

func (r riskLevel) colored() string {
	switch r {
	case riskMinimal:
		return green("Minimal")
	case riskLow:
		return cyan("Low")
	case riskMedium:
		return yellow("Medium")
	case riskHigh:
		return red("High")
	default:
		return "—"
	}
}

var (
	reNormWS = regexp.MustCompile(`\s+`)
	// MariaDB puts NOT NULL before DEFAULT; canonicalise to NOT NULL before DEFAULT.
	reDefaultBeforeNull = regexp.MustCompile(`(?i)\bdefault\s+(\S+)\s+not\s+null\b`)
	reCharsetSQL        = regexp.MustCompile(`(?i)\s+(character\s+set|charset|collate)\s+\S+`)
	// MariaDB 10.5+ drops display widths for integer types (int(11) → int).
	reIntWidth = regexp.MustCompile(`\b(tinyint|smallint|mediumint|int|integer|bigint|year)\(\d+\)`)
	// mysqldump moves inline PRIMARY KEY / UNIQUE [KEY] to separate constraint lines.
	reInlineKey = regexp.MustCompile(`\s+(primary\s+key|unique\s+key|unique)\b`)
	// NULL and DEFAULT NULL are semantically equivalent for nullable columns.
	// Match only when not preceded by NOT (which we protect with a placeholder).
	reNullClause = regexp.MustCompile(`\s+(default\s+)?null\b`)
	// MariaDB stores boolean/bool as tinyint(1).
	reBoolean = regexp.MustCompile(`\b(boolean|bool)\b`)
	// Matches decimal with or without precision — used to normalise bare decimal.
	reDecimal = regexp.MustCompile(`\bdecimal(\s*\([^)]*\))?`)
	// MariaDB stores json as longtext with a CHECK constraint.
	reJSON = regexp.MustCompile(`\bjson\b`)
	// Strip CHECK (...) including one level of nested parens: CHECK (json_valid(`col`))
	reCheckExpr = regexp.MustCompile(`\s+check\s*\((?:[^()]*|\([^()]*\))*\)`)
)

func normalizeColDef(def string) string {
	def = strings.ReplaceAll(def, "`", "")
	def = reCharsetSQL.ReplaceAllString(def, "")
	def = strings.ToLower(strings.TrimSpace(def))
	def = strings.ReplaceAll(def, "current_timestamp()", "current_timestamp")
	// MariaDB stores boolean literals as integers.
	def = strings.ReplaceAll(def, "default false", "default 0")
	def = strings.ReplaceAll(def, "default true", "default 1")
	// MariaDB stores boolean as tinyint(1) — must run before reIntWidth so the
	// introduced tinyint(1) gets its display width stripped in the same pass.
	def = reBoolean.ReplaceAllString(def, "tinyint(1)")
	// Strip deprecated display widths from integer/year types.
	def = reIntWidth.ReplaceAllString(def, "$1")
	// Plain decimal without precision defaults to decimal(10,0) in MariaDB.
	def = reDecimal.ReplaceAllStringFunc(def, func(s string) string {
		if strings.Contains(s, "(") {
			return s
		}
		return "decimal(10,0)"
	})
	// MariaDB stores json as longtext with a CHECK constraint; treat them as equal.
	def = reJSON.ReplaceAllString(def, "longtext")
	def = reCheckExpr.ReplaceAllString(def, "")
	// AUTO_INCREMENT columns are implicitly NOT NULL; make it explicit so both
	// sides of the comparison carry "not null".
	if strings.Contains(def, "auto_increment") && !strings.Contains(def, "not null") {
		def = strings.Replace(def, "auto_increment", "not null auto_increment", 1)
	}
	// Canonicalise: MariaDB always emits NOT NULL before AUTO_INCREMENT.
	def = strings.ReplaceAll(def, "auto_increment not null", "not null auto_increment")
	// Inline PRIMARY KEY implies NOT NULL — make it explicit before stripping the keyword.
	if strings.Contains(def, "primary key") && !strings.Contains(def, "not null") {
		def = strings.Replace(def, "primary key", "not null primary key", 1)
	}
	// mysqldump moves inline PRIMARY KEY / UNIQUE to a separate constraint line.
	def = reInlineKey.ReplaceAllString(def, "")
	// NULL and DEFAULT NULL are equivalent. Protect "not null" with a placeholder
	// before stripping bare null / default null so it is not accidentally removed.
	// Canonicalise clause order: MariaDB emits NOT NULL before DEFAULT, but SQL
	// files often write DEFAULT first. Normalise to "not null default <val>".
	def = reDefaultBeforeNull.ReplaceAllString(def, "not null default $1")
	// NULL and DEFAULT NULL are equivalent. Protect "not null" with a placeholder
	// before stripping bare null / default null so it is not accidentally removed.
	def = strings.ReplaceAll(def, "not null", "\x01")
	def = reNullClause.ReplaceAllString(def, "")
	def = strings.ReplaceAll(def, "\x01", "not null")
	def = reNormWS.ReplaceAllString(def, " ")
	def = strings.ReplaceAll(def, ", ", ",")
	return strings.TrimSpace(def)
}

var reExtractType = regexp.MustCompile(`(?i)^\w+\s+(\w+)(?:\((\d+))?`)

func extractBaseType(colDef string) (string, int) {
	def := strings.ReplaceAll(strings.TrimSpace(colDef), "`", "")
	m := reExtractType.FindStringSubmatch(def)
	if m == nil {
		return "", 0
	}
	t := strings.ToLower(m[1])
	size := 0
	if len(m) > 2 && m[2] != "" {
		size, _ = strconv.Atoi(m[2])
	}
	return t, size
}

func numericTypeRank(t string) int {
	switch t {
	case "tinyint":
		return 1
	case "smallint":
		return 2
	case "mediumint":
		return 3
	case "int", "integer":
		return 4
	case "bigint":
		return 5
	case "float":
		return 6
	case "double", "real":
		return 7
	}
	return 0
}

func textTypeRank(t string) int {
	switch t {
	case "tinytext":
		return 1
	case "text":
		return 2
	case "mediumtext":
		return 3
	case "longtext":
		return 4
	}
	return 0
}

func isNullableOrHasDefault(colDef string) bool {
	upper := strings.ToUpper(colDef)
	return !strings.Contains(upper, "NOT NULL") || strings.Contains(upper, "DEFAULT")
}

func assessTypeRisk(oldDef, newDef string) (riskLevel, string) {
	oldType, oldSize := extractBaseType(oldDef)
	newType, newSize := extractBaseType(newDef)

	if oldType == newType {
		if oldSize == 0 || newSize == 0 || newSize >= oldSize {
			return riskLow, "column type expanded"
		}
		return riskHigh, "column type shrunk"
	}

	oldNum := numericTypeRank(oldType)
	newNum := numericTypeRank(newType)
	if oldNum > 0 && newNum > 0 {
		if newNum >= oldNum {
			return riskLow, "numeric type expanded"
		}
		return riskHigh, "numeric type shrunk"
	}

	oldText := textTypeRank(oldType)
	newText := textTypeRank(newType)
	if oldText > 0 && newText > 0 {
		if newText >= oldText {
			return riskLow, "text type expanded"
		}
		return riskHigh, "text type shrunk"
	}

	return riskHigh, "data type changed"
}

type colChange struct {
	name   string
	oldDef string // empty = new column
	newDef string // empty = dropped column
	risk   riskLevel
	reason string
}

// fetchLiveSchema uses SHOW CREATE TABLE for each table so the returned
// CREATE statements match what MySQL actually stores, avoiding the formatting
// differences that mysqldump introduces.
func (r *Remote) fetchLiveSchema(dbUser, dbPass, dbName string) (string, error) {
	tablesOut, err := r.runWithOutput(
		fmt.Sprintf(`MYSQL_PWD='%s' mysql -u %s %s --skip-column-names -e "SHOW TABLES"`, dbPass, dbUser, dbName),
		"",
	)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	for _, tableName := range strings.Split(strings.TrimSpace(tablesOut), "\n") {
		tableName = strings.TrimSpace(tableName)
		if tableName == "" {
			continue
		}
		createOut, err := r.runWithOutput(
			fmt.Sprintf("MYSQL_PWD='%s' mysql -u %s %s --skip-column-names -e 'SHOW CREATE TABLE %s'", dbPass, dbUser, dbName, tableName),
			"",
		)
		if err != nil {
			return "", fmt.Errorf("SHOW CREATE TABLE %s: %w", tableName, err)
		}
		// Output is two tab-separated columns: tablename\tCREATE TABLE ...
		// MySQL batch mode escapes newlines as \n and tabs as \t within values.
		parts := strings.SplitN(createOut, "\t", 2)
		if len(parts) == 2 {
			stmt := parts[1]
			stmt = strings.ReplaceAll(stmt, `\n`, "\n")
			stmt = strings.ReplaceAll(stmt, `\t`, "\t")
			stmt = strings.TrimSpace(stmt)
			if !strings.HasSuffix(stmt, ";") {
				stmt += ";"
			}
			sb.WriteString(stmt)
			sb.WriteString("\n\n")
		}
	}
	return sb.String(), nil
}

func (r *Remote) queryRowCount(dbUser, dbPass, dbName, tableName string) (int, error) {
	out, err := r.runWithOutput(
		fmt.Sprintf(`MYSQL_PWD='%s' mysql -u %s %s --skip-column-names`, dbPass, dbUser, dbName),
		fmt.Sprintf("SELECT COUNT(*) FROM `%s`;\n", tableName),
	)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("unexpected row count output: %q", strings.TrimSpace(out))
	}
	return n, nil
}

func extractCreateTableSQL(fullSQL, tableName string) string {
	for _, m := range reCreateTable.FindAllStringSubmatch(fullSQL, -1) {
		if strings.ToLower(m[1]) == strings.ToLower(tableName) {
			stmt := strings.TrimSpace(m[0])
			if !strings.HasSuffix(stmt, ";") {
				stmt += ";"
			}
			return stmt
		}
	}
	return ""
}

func runDiagnostics(app *AppConfig, projectName string, saved *ProjectConfig) {
	if saved == nil {
		fatalExit(red("  ✗ No saved config for project " + projectName))
	}

	cfg := &Config{}
	cfg.ProjectName = projectName
	cfg.GitHubToken = saved.GitHubToken
	cfg.GitHubUser = saved.GitHubUser
	cfg.BackendFork = saved.GitHubUser + "/" + projectName + "-backend"
	cfg.ServerIP = saved.ServerIP
	cfg.SSHUser = saved.SSHUser
	cfg.SSHPass = saved.SSHPass
	cfg.SudoPass = saved.SudoPass
	cfg.DBName = saved.DBName
	cfg.DBUser = saved.DBUser
	cfg.DBPass = saved.DBPass

	if cfg.GitHubToken != "" {
		os.Setenv("GH_TOKEN", cfg.GitHubToken)
	}

	// ── Gather data ──────────────────────────────────────────────────────────
	fmt.Print("\n▶  " + bold("Fetching schema from GitHub") + "\n")
	sqlSchema, err := ghReadFile(cfg.BackendFork, sqlFile, trackBranch)
	if err != nil {
		fatalExit(red("   ✗ " + err.Error()))
	}
	fmt.Println(green("   ✓ Done"))

	fmt.Print("\n▶  " + bold("Connecting to server") + "\n")
	client, err := connectSSH(cfg.ServerIP, "22", cfg.SSHUser, cfg.SSHPass)
	if err != nil {
		fatalExit(red("   ✗ " + err.Error()))
	}
	remote := &Remote{client: client, sudoPass: cfg.SudoPass}
	defer remote.client.Close()
	fmt.Println(green("   ✓ Done"))

	fmt.Print("\n▶  " + bold("Fetching live database schema") + "\n")
	liveSchema, err := remote.fetchLiveSchema(cfg.DBUser, cfg.DBPass, cfg.DBName)
	if err != nil {
		fatalExit(red("   ✗ " + err.Error()))
	}
	fmt.Println(green("   ✓ Done"))

	// ── Compare ──────────────────────────────────────────────────────────────
	sqlTables := parseTables(sqlSchema)
	liveTables := parseTables(liveSchema)

	sep := strings.Repeat("─", 52)

	tableNames := make([]string, 0, len(sqlTables))
	for t := range sqlTables {
		tableNames = append(tableNames, t)
	}
	sort.Strings(tableNames)

	fmt.Println()
	fmt.Println(bold(sep))
	fmt.Printf("  %s — %s\n", bold("Database Diagnostic Report"), cyan(cfg.DBName))
	fmt.Println(bold(sep))

	for _, tableName := range tableNames {
		sqlCols := sqlTables[tableName]
		fmt.Println()
		fmt.Printf("  Table: %s\n", bold(tableName))

		fmt.Printf("  [SQL file]\n%s\n\n", extractCreateTableSQL(sqlSchema, tableName))
		fmt.Printf("  [Live DB]\n%s\n\n", extractCreateTableSQL(liveSchema, tableName))

		liveCols, exists := liveTables[tableName]
		if !exists {
			fmt.Printf("  Status: %s\n", yellow("MISSING — not in database"))
			createSQL := extractCreateTableSQL(sqlSchema, tableName)
			if createSQL != "" && confirm("  Create this table?") {
				if applyErr := remote.runWithInput(
					fmt.Sprintf(`MYSQL_PWD='%s' mysql -u %s %s`, cfg.DBPass, cfg.DBUser, cfg.DBName),
					createSQL+"\n",
				); applyErr != nil {
					fmt.Println(red("   ✗ " + applyErr.Error()))
				} else {
					fmt.Println(green("   ✓ Table created."))
				}
			}
			fmt.Println(sep)
			continue
		}

		// Collect column differences
		var changes []colChange
		for colName, newDef := range sqlCols {
			oldDef, known := liveCols[colName]
			if !known {
				risk, reason := riskLow, "new nullable column"
				if !isNullableOrHasDefault(newDef) {
					risk, reason = riskMedium, "new NOT NULL column without default"
				}
				changes = append(changes, colChange{name: colName, newDef: newDef, risk: risk, reason: reason})
			} else if normalizeColDef(oldDef) != normalizeColDef(newDef) {
				risk, reason := assessTypeRisk(oldDef, newDef)
				changes = append(changes, colChange{name: colName, oldDef: oldDef, newDef: newDef, risk: risk, reason: reason})
			}
		}
		// _flattened tables have dynamic extra columns — only validate what the
		// SQL file defines, ignore any additional live columns.
		if !strings.HasSuffix(tableName, "_flattened") {
			for colName, oldDef := range liveCols {
				if _, inSQL := sqlCols[colName]; !inSQL {
					changes = append(changes, colChange{name: colName, oldDef: oldDef, risk: riskHigh, reason: "column to be removed"})
				}
			}
		}

		if len(changes) == 0 {
			fmt.Printf("  Status: %s\n", green("OK ✓"))
			fmt.Println(sep)
			continue
		}

		fmt.Printf("  Status: %s\n", yellow("DIFFERENCES FOUND"))

		rowCount, countErr := remote.queryRowCount(cfg.DBUser, cfg.DBPass, cfg.DBName, tableName)
		if countErr != nil {
			fmt.Printf("  %s\n", yellow("⚠  Could not get row count: "+countErr.Error()))
		} else {
			fmt.Printf("  Rows: %d\n", rowCount)
		}

		sort.Slice(changes, func(i, j int) bool { return changes[i].name < changes[j].name })

		overallRisk := riskNone
		for _, c := range changes {
			if c.risk > overallRisk {
				overallRisk = c.risk
			}
		}

		fmt.Println()
		fmt.Println("  Changes:")
		for _, c := range changes {
			var marker, def string
			switch {
			case c.oldDef == "":
				marker = green("+")
				def = c.newDef
			case c.newDef == "":
				marker = red("-")
				def = c.oldDef
			default:
				marker = yellow("~")
				def = c.newDef
			}
			fmt.Printf("    %s  %s\n       Risk: %s — %s\n", marker, def, c.risk.colored(), c.reason)
		}

		// Generate ALTER TABLE statements
		var alters []string
		for _, c := range changes {
			switch {
			case c.oldDef == "":
				alters = append(alters, fmt.Sprintf("ALTER TABLE `%s` ADD COLUMN %s;", tableName, c.newDef))
			case c.newDef == "":
				alters = append(alters, fmt.Sprintf("ALTER TABLE `%s` DROP COLUMN `%s`;", tableName, c.name))
			default:
				alters = append(alters, fmt.Sprintf("ALTER TABLE `%s` MODIFY COLUMN %s;", tableName, c.newDef))
			}
		}

		fmt.Println()
		if countErr == nil && rowCount == 0 {
			fmt.Printf("  Risk level: %s (table is empty — safe to recreate)\n", green("Minimal"))
			createSQL := extractCreateTableSQL(sqlSchema, tableName)
			if createSQL != "" && confirm("  Recreate this table from schema?") {
				recreateSQL := fmt.Sprintf("DROP TABLE IF EXISTS `%s`;\n%s\n", tableName, createSQL)
				if applyErr := remote.runWithInput(
					fmt.Sprintf(`MYSQL_PWD='%s' mysql -u %s %s`, cfg.DBPass, cfg.DBUser, cfg.DBName),
					recreateSQL,
				); applyErr != nil {
					fmt.Println(red("   ✗ " + applyErr.Error()))
				} else {
					fmt.Println(green("   ✓ Table recreated."))
				}
			}
		} else {
			fmt.Printf("  Overall risk: %s\n", overallRisk.colored())
			fmt.Println()
			fmt.Println("  Generated SQL:")
			for _, a := range alters {
				fmt.Println("    " + a)
			}
			fmt.Println()
			if confirm("  Apply these changes?") {
				if applyErr := remote.runWithInput(
					fmt.Sprintf(`MYSQL_PWD='%s' mysql -u %s %s`, cfg.DBPass, cfg.DBUser, cfg.DBName),
					strings.Join(alters, "\n")+"\n",
				); applyErr != nil {
					fmt.Println(red("   ✗ " + applyErr.Error()))
				} else {
					fmt.Println(green("   ✓ Changes applied."))
				}
			}
		}
		fmt.Println(sep)
	}

	// Report tables in live DB not in schema file
	for tableName := range liveTables {
		if _, inSQL := sqlTables[tableName]; !inSQL {
			fmt.Println()
			fmt.Printf("  Table: %s\n", bold(tableName))
			fmt.Printf("  Status: %s\n", yellow("EXTRA — not in schema file (no action taken)"))
			fmt.Println(sep)
		}
	}

	fmt.Println()
}

// ─── SONIC WORKFLOW ───────────────────────────────────────────────────────────

const sonicWorkflow = `name: Install Sonic

on:
  workflow_dispatch:

jobs:
  install-sonic:
    runs-on: ubuntu-22.04
    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Install Rust and build Sonic
        run: |
          sudo apt-get install -y build-essential git
          curl https://sh.rustup.rs -sSf | sh -s -- -y --profile minimal
          source "$HOME/.cargo/env"
          git clone --depth 1 https://github.com/valeriansaliou/sonic.git sonic-src
          cd sonic-src
          cargo build --release
          mkdir -p ../deploy
          cp target/release/sonic ../deploy/sonic
          cp ../.github/sonic/sonic.cfg ../deploy/sonic.cfg
          cp ../.github/sonic/sonic.service ../deploy/sonic.service

      - name: Copy files to server
        uses: appleboy/scp-action@v0.1.7
        with:
          host: ${{ secrets.DROPLET_IP }}
          username: root
          key: ${{ secrets.SSH_PRIVATE_KEY }}
          source: "deploy/*"
          target: "/tmp/sonic-deploy/"
          strip_components: 1

      - name: Install and start Sonic
        uses: appleboy/ssh-action@v1.0.3
        with:
          host: ${{ secrets.DROPLET_IP }}
          username: root
          key: ${{ secrets.SSH_PRIVATE_KEY }}
          script: |
            cp /tmp/sonic-deploy/sonic /usr/local/bin/sonic
            chmod 755 /usr/local/bin/sonic
            cp /tmp/sonic-deploy/sonic.cfg /etc/sonic.cfg
            cp /tmp/sonic-deploy/sonic.service /etc/systemd/system/sonic.service
            id -u sonic &>/dev/null || useradd --system --no-create-home --shell /usr/sbin/nologin sonic
            mkdir -p /var/lib/sonic/store/kv /var/lib/sonic/store/fst
            chown -R sonic:sonic /var/lib/sonic
            systemctl daemon-reload
            systemctl enable sonic
            systemctl restart sonic
            rm -rf /tmp/sonic-deploy
`

const sonicCfgFile = `[server]
log_level = "error"

[channel]
inet = "127.0.0.1:1491"
tcp_timeout = 300
auth_password = "SecretPassword"

[channel.search]
query_limit_default = 10
query_limit_maximum = 100
query_alternates_try = 4
suggest_limit_default = 5
suggest_limit_maximum = 20
list_limit_default = 100
list_limit_maximum = 500

[store]

[store.kv]
path = "/var/lib/sonic/store/kv/"
retain_word_objects = 1000

[store.kv.pool]
inactive_after = 1800

[store.kv.database]
flush_after = 900
compress = true
parallelism = 2
max_files = 100
max_compactions = 1
max_flushes = 1
write_buffer = 16384
write_ahead_log = true

[store.fst]
path = "/var/lib/sonic/store/fst/"

[store.fst.pool]
inactive_after = 300

[store.fst.graph]
consolidate_after = 180
max_size = 2048
max_words = 250000
`

const sonicServiceFile = `[Unit]
Description=Sonic Search Index
After=network.target

[Service]
Type=simple
User=sonic
ExecStart=/usr/local/bin/sonic -c /etc/sonic.cfg
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`

// ─── Banner & main ────────────────────────────────────────────────────────────

func printBanner() {
	fmt.Println()
	fmt.Println(bold(colorBlue + "╔══════════════════════════════════════╗" + colorReset))
	fmt.Println(bold(colorBlue + "║          Cuento CLI 1.3.2            ║" + colorReset))
	fmt.Println(bold(colorBlue + "╚══════════════════════════════════════╝" + colorReset))
	fmt.Println()
}

func main() {
	printBanner()

	app := loadConfig()

	for {
		// Build sorted list of existing project names.
		projects := make([]string, 0, len(app.Projects))
		for name := range app.Projects {
			projects = append(projects, name)
		}
		sort.Strings(projects)

		if len(projects) > 0 {
			fmt.Println(bold("Select a project:"))
		} else {
			fmt.Println(bold("No existing projects found."))
		}
		fmt.Println()

		for i, name := range projects {
			fmt.Printf("  %d. %s\n", i+1, name)
		}
		addNewIdx := len(projects) + 1
		exitIdx := len(projects) + 2
		fmt.Printf("  %d. Add new project\n", addNewIdx)
		fmt.Printf("  %d. Exit\n", exitIdx)
		fmt.Println()

		choice := promptRequired("Enter choice")
		fmt.Println()

		n, err := strconv.Atoi(choice)
		if err != nil || n < 1 || n > exitIdx {
			fmt.Println(red("Invalid choice."))
			continue
		}

		if n == exitIdx {
			return
		}

		if n == addNewIdx {
			runCreate(app, 0, nil)
			app = loadConfig()
			continue
		}

		name := projects[n-1]
		saved := app.Projects[name]

		// If setup was interrupted mid-way, offer to resume before showing the normal menu.
		if saved.SetupStep > 0 {
			fmt.Printf(yellow("  ⚠  Setup was interrupted (completed %d steps).\n"), saved.SetupStep-1)
			if confirm("Resume setup from where it left off?") {
				resumeCfg := &Config{
					ProjectName:  name,
					GitHubToken:  saved.GitHubToken,
					GitHubUser:   saved.GitHubUser,
					BackendFork:  saved.GitHubUser + "/" + name + "-backend",
					FrontendFork: saved.GitHubUser + "/" + name + "-frontend",
					ServerIP:     saved.ServerIP,
					SSHUser:      saved.SSHUser,
					SSHPass:      saved.SSHPass,
					SudoPass:     saved.SudoPass,
					Domain:       saved.Domain,
					DBRootPass:   saved.DBRootPass,
					DBName:       saved.DBName,
					DBUser:       saved.DBUser,
					DBPass:       saved.DBPass,
				}
				if saved.SSHPrivKey != "" {
					resumeCfg.SSHPrivateKey, _ = base64.StdEncoding.DecodeString(saved.SSHPrivKey)
				}
				if saved.SSHPubKey != "" {
					resumeCfg.SSHPublicKey, _ = base64.StdEncoding.DecodeString(saved.SSHPubKey)
				}
				runCreate(app, saved.SetupStep, resumeCfg)
				app = loadConfig()
				continue
			}
			fmt.Println()
		}

		fmt.Printf(bold("Project: %s\n"), name)
		fmt.Println()
		fmt.Println("  1. Update")
		fmt.Println("  2. Add user")
		fmt.Println("  3. Import default permissions")
		fmt.Println("  4. Reset deployment pipeline")
		fmt.Println("  5. Database diagnostics")
		fmt.Println()

		action := promptRequired("Enter choice")
		fmt.Println()

		switch action {
		case "1":
			runUpdate(app, name, saved)
		case "2":
			runAddUser(app, name, saved)
		case "3":
			runImportPermissions(app, name, saved)
		case "4":
			runResetPipeline(app, name, saved)
		case "5":
			runDiagnostics(app, name, saved)
		default:
			fmt.Println(red("Invalid choice."))
		}
	}
}
