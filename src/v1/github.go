package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

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
