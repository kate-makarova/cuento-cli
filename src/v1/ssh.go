package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
)

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
