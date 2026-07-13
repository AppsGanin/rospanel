// Package provision installs a RosPanel node on a remote server over SSH, so the
// operator can add a node from the panel UI without touching a terminal. The SSH
// credentials are used only for the duration of the install and are never stored.
package provision

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Credentials describe how to reach and authenticate to the target server. Exactly
// one of Password / PrivateKey must be set.
type Credentials struct {
	Host       string // IP or hostname of the target server
	Port       int    // SSH port (0 ⇒ 22)
	User       string // SSH user (must be root or able to sudo -n)
	Password   string // password auth (mutually exclusive with PrivateKey)
	PrivateKey string // PEM private key (mutually exclusive with Password)
	Passphrase string // optional passphrase for an encrypted PrivateKey
}

// Install connects to the target server and runs the given install command
// (the panel's `curl … | bash -s -- --join …` one-liner), streaming every output
// line to onLine. It returns the discovered host-key fingerprint and any error.
// The command must be self-contained; nothing is uploaded.
func Install(ctx context.Context, c Credentials, installCmd string, onLine func(string)) (hostKeyFP string, err error) {
	// Derive a cancelable child so the session-killer goroutine below always exits
	// when Install returns, even if the caller's context outlives this call.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	auth, err := authMethods(c)
	if err != nil {
		return "", err
	}
	var fp string
	cfg := &ssh.ClientConfig{
		User: strings.TrimSpace(c.User),
		Auth: auth,
		// Trust-on-first-use: the operator is installing onto their own fresh server
		// and has no prior key to pin. Record the fingerprint so it can be shown in
		// the UI (a MITM would still need the SSH credentials to do anything).
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			fp = keyFingerprint(key)
			return nil
		},
		Timeout: 20 * time.Second,
	}
	port := c.Port
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(strings.TrimSpace(c.Host), fmt.Sprint(port))

	// Dial with the context deadline honored.
	d := net.Dialer{Timeout: cfg.Timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", fmt.Errorf("connect to %s: %w", addr, err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return fp, fmt.Errorf("ssh handshake: %w", err)
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	defer client.Close()
	onLine("Подключено к " + addr + " (отпечаток ключа " + fp + ")")

	session, err := client.NewSession()
	if err != nil {
		return fp, fmt.Errorf("open session: %w", err)
	}
	defer session.Close()

	stdout, err := session.StdoutPipe()
	if err != nil {
		return fp, err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return fp, err
	}

	// Stream both streams line by line to the caller.
	done := make(chan struct{}, 2)
	go streamLines(stdout, onLine, done)
	go streamLines(stderr, onLine, done)

	// Kill the session if the context is cancelled (operator closed the UI).
	go func() {
		<-ctx.Done()
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
	}()

	onLine("Запуск установки…")
	runErr := session.Run(installCmd)
	<-done
	<-done
	if runErr != nil {
		return fp, fmt.Errorf("install command failed: %w", runErr)
	}
	return fp, nil
}

func authMethods(c Credentials) ([]ssh.AuthMethod, error) {
	switch {
	case c.PrivateKey != "":
		var signer ssh.Signer
		var err error
		if c.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(c.PrivateKey), []byte(c.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(c.PrivateKey))
		}
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	case c.Password != "":
		return []ssh.AuthMethod{
			ssh.Password(c.Password),
			// Some servers offer keyboard-interactive for password auth.
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				ans := make([]string, len(questions))
				for i := range ans {
					ans[i] = c.Password
				}
				return ans, nil
			}),
		}, nil
	default:
		return nil, fmt.Errorf("no SSH password or private key provided")
	}
}

func streamLines(r io.Reader, onLine func(string), done chan<- struct{}) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		onLine(sc.Text())
	}
	done <- struct{}{}
}

// keyFingerprint returns the SHA256 fingerprint of a host key (OpenSSH format).
func keyFingerprint(key ssh.PublicKey) string {
	sum := sha256.Sum256(key.Marshal())
	return "SHA256:" + strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "=")
}
