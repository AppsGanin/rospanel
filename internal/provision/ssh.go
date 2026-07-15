// Package provision installs a RosPanel node on a remote server over SSH, so the
// operator can add a node from the panel UI without touching a terminal. The SSH
// credentials are used only for the duration of the install and are never stored.
//
// The panel uploads its OWN binary to the target and runs `rospanel node install`
// with it, rather than having the target download a release from GitHub. That makes
// the node run the exact same build as the panel (no version skew, no dependency on
// a published release or on the main-branch install.sh), which is also the only way
// this works for an unreleased/dev build.
package provision

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// remoteInstaller is where the panel binary is uploaded on the target before it
// self-installs. It sits in an exec-friendly dir (a hardened /tmp is often noexec)
// and is removed after the install runs.
const remoteInstaller = "/usr/local/bin/rospanel.installer"

// Credentials describe how to reach and authenticate to the target server. Exactly
// one of Password / PrivateKey must be set.
type Credentials struct {
	Host       string // IP or hostname of the target server
	Port       int    // SSH port (0 ⇒ 22)
	User       string // SSH user (must be root)
	Password   string // password auth (mutually exclusive with PrivateKey)
	PrivateKey string // PEM private key (mutually exclusive with Password)
	Passphrase string // optional passphrase for an encrypted PrivateKey
}

// Install connects to the target, uploads localBinary, and runs it as
// `rospanel node <installArgs...>` (join + systemd setup), streaming every output
// line to onLine. Returns the discovered host-key fingerprint and any error.
func Install(ctx context.Context, c Credentials, localBinary string, installArgs []string, onLine func(string)) (hostKeyFP string, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	client, fp, err := dial(ctx, c, onLine)
	if err != nil {
		return fp, err
	}
	defer client.Close()

	onLine("Загрузка агента на сервер…")
	if err := uploadBinary(ctx, client, localBinary); err != nil {
		return fp, fmt.Errorf("upload agent binary: %w", err)
	}

	onLine("Запуск установки…")
	// Run the installer, then remove it whatever the outcome, preserving its exit code.
	cmd := shellQuote(remoteInstaller) + " node " + shellJoin(installArgs) +
		"; rc=$?; rm -f " + shellQuote(remoteInstaller) + "; exit $rc"
	if err := runStreaming(ctx, client, cmd, onLine); err != nil {
		return fp, fmt.Errorf("install command failed: %w", err)
	}
	return fp, nil
}

// dial opens the SSH connection and returns the client + host-key fingerprint.
func dial(ctx context.Context, c Credentials, onLine func(string)) (*ssh.Client, string, error) {
	auth, err := authMethods(c)
	if err != nil {
		return nil, "", err
	}
	var fp string
	cfg := &ssh.ClientConfig{
		User: strings.TrimSpace(c.User),
		Auth: auth,
		// Trust-on-first-use: the operator is installing onto their own fresh server
		// and has no prior key to pin, so we accept the presented key and surface its
		// fingerprint. NOTE: this offers no protection against a man-in-the-middle on
		// the SSH path — with password auth the password is sent to whoever answers.
		// It is intended for provisioning your own box over a trusted network; prefer
		// key auth (the signature is session-bound and can't be replayed by a MITM).
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
	d := net.Dialer{Timeout: cfg.Timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, "", fmt.Errorf("connect to %s: %w", addr, err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, fp, fmt.Errorf("ssh handshake: %w", err)
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	onLine("Подключено к " + addr + " (отпечаток ключа " + fp + ")")
	return client, fp, nil
}

// uploadBinary streams localBinary (gzip-compressed on the wire) to the target and
// writes it to remoteInstaller, chmod +x.
func uploadBinary(ctx context.Context, client *ssh.Client, localBinary string) error {
	f, err := os.Open(localBinary)
	if err != nil {
		return err
	}
	defer f.Close()

	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	go func() {
		<-ctx.Done()
		_ = sess.Close()
	}()

	stdin, err := sess.StdinPipe()
	if err != nil {
		return err
	}
	remote := shellQuote(remoteInstaller)
	if err := sess.Start("gunzip -c > " + remote + " && chmod 0755 " + remote); err != nil {
		return err
	}
	gz := gzip.NewWriter(stdin)
	if _, err := io.Copy(gz, f); err != nil {
		_ = stdin.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		_ = stdin.Close()
		return err
	}
	if err := stdin.Close(); err != nil {
		return err
	}
	return sess.Wait()
}

// runStreaming runs cmd and streams stdout+stderr line by line to onLine.
func runStreaming(ctx context.Context, client *ssh.Client, cmd string, onLine func(string)) error {
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()

	stdout, err := sess.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		return err
	}
	done := make(chan struct{}, 2)
	go streamLines(stdout, onLine, done)
	go streamLines(stderr, onLine, done)
	go func() {
		<-ctx.Done()
		_ = sess.Signal(ssh.SIGKILL)
		_ = sess.Close()
	}()

	runErr := sess.Run(cmd)
	<-done
	<-done
	return runErr
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

// shellQuote single-quotes a string for safe use in a POSIX shell command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellJoin single-quotes and space-joins args for a remote shell command line.
func shellJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = shellQuote(a)
	}
	return strings.Join(q, " ")
}
