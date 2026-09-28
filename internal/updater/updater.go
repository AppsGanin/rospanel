// Package updater self-updates the panel binary from its GitHub Releases: it
// finds the latest release, downloads the matching asset, verifies its SHA-256
// against the release's SHA256SUMS BEFORE the binary is ever executed or swapped
// in, then atomically replaces the running executable (keeping a .bak for
// rollback). The caller restarts the service so systemd re-execs the new binary
// (and respawns the supervised Xray).
package updater

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Repo is the GitHub "owner/repo" this panel self-updates from, baked into the
// build. Change it here if the project moves.
const Repo = "AppsGanin/rospanel"

// ErrInProgress is an install refused because another is running.
var ErrInProgress = errors.New("an update is already in progress")

var applyMu sync.Mutex

// UnderSystemd reports whether the panel runs as a systemd service — what the
// restart after an install relies on. systemd sets INVOCATION_ID for the processes of
// every unit it starts; a container or a shell has none.
func UnderSystemd() bool { return os.Getenv("INVOCATION_ID") != "" }

// ConfiguredRepo is the "owner/repo" the panel self-updates from: Repo, or the
// ROSPANEL_REPO env when set (handy for testing).
func ConfiguredRepo() string {
	if r := strings.TrimSpace(os.Getenv("ROSPANEL_REPO")); r != "" {
		return r
	}
	return Repo
}

// maxAssetBytes bounds a downloaded release binary. The real asset is ~25 MB, so this
// leaves ample room while keeping a hostile or broken host from filling the disk before
// the checksum is checked.
const maxAssetBytes = 256 << 20

// AssetName is the release asset this build updates from, e.g. "rospanel-linux-amd64".
var AssetName = fmt.Sprintf("rospanel-%s-%s", runtime.GOOS, runtime.GOARCH)

// Release is the newest published release of the configured repo.
type Release struct {
	Version     string `json:"version"` // semver without a leading "v"
	Notes       string `json:"notes"`   // release body (changelog)
	AssetURL    string `json:"-"`       // download URL of AssetName
	ChecksumURL string `json:"-"`       // download URL of the SHA256SUMS asset
}

// Latest queries the GitHub Releases API for the newest release of "owner/repo".
func Latest(ctx context.Context, repo string) (*Release, error) {
	repo = strings.Trim(strings.TrimSpace(repo), "/")
	if repo == "" {
		return nil, fmt.Errorf("update repository is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "rospanel-updater")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub is unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("repository %s has no releases", repo)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub returned status %d", resp.StatusCode)
	}
	var gh struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&gh); err != nil {
		return nil, err
	}
	rel := &Release{Version: strings.TrimPrefix(gh.TagName, "v"), Notes: gh.Body}
	for _, a := range gh.Assets {
		switch a.Name {
		case AssetName:
			rel.AssetURL = a.URL
		case "SHA256SUMS":
			rel.ChecksumURL = a.URL
		}
	}
	return rel, nil
}

// IsNewer reports whether semver `latest` is strictly newer than `current`
// (numeric, dot-separated; non-numeric or shorter parts compare conservatively).
func IsNewer(latest, current string) bool {
	la := splitVer(latest)
	cu := splitVer(current)
	for i := 0; i < len(la) || i < len(cu); i++ {
		var l, c int
		if i < len(la) {
			l = la[i]
		}
		if i < len(cu) {
			c = cu[i]
		}
		if l != c {
			return l > c
		}
	}
	return false
}

func splitVer(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 { // drop pre-release/build metadata
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}

// Apply downloads the release asset, VERIFIES its SHA-256 against the release's
// SHA256SUMS before the binary is ever executed, smoke-tests it, runs backupFn as
// a pre-update snapshot (best-effort), then atomically replaces the running
// executable — keeping the previous binary as <exe>.bak for rollback. It does NOT
// restart the service — the caller does that. Refuses to update a release that
// ships no SHA256SUMS (integrity cannot be proven).
func Apply(ctx context.Context, rel *Release, backupFn func() error) error {
	// One install at a time in this process: the button and the schedule landing
	// together would otherwise each move the other's verified binary around.
	if !applyMu.TryLock() {
		return ErrInProgress
	}
	defer applyMu.Unlock()
	if rel == nil || rel.AssetURL == "" {
		return fmt.Errorf("the release carries no %s", AssetName)
	}
	if rel.ChecksumURL == "" {
		return fmt.Errorf("the release carries no SHA256SUMS — update cancelled (integrity cannot be verified)")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	// Already on disk, waiting for the restart (a scheduled install whose restart did
	// not come, the button pressed again): swapping it in again would move it onto
	// <exe>.bak, over the only copy of the version that runs now.
	if v := binaryVersion(exe); v != "" && v == strings.TrimPrefix(rel.Version, "v") {
		return nil
	}
	tmp, err := downloadFile(exe)
	if err != nil {
		return err
	}

	if err := download(ctx, rel.AssetURL, tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("download failed: %w", err)
	}
	// Integrity gate FIRST — a tampered/corrupted asset is rejected here and is
	// never executed or swapped in.
	want, err := expectedSum(ctx, rel.ChecksumURL, AssetName)
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("could not fetch the checksum: %w", err)
	}
	got, err := fileSum(tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("could not compute the checksum: %w", err)
	}
	if !strings.EqualFold(got, want) {
		_ = os.Remove(tmp)
		return fmt.Errorf("checksum mismatch — update cancelled")
	}
	// Only now (integrity proven) run the binary to catch a wrong-arch/truncated
	// build before the swap.
	out, err := exec.Command(tmp, "version").Output()
	if err != nil || !strings.Contains(string(out), ".") {
		_ = os.Remove(tmp)
		return fmt.Errorf("the downloaded binary does not run — update cancelled")
	}
	// Pre-update snapshot (defence-in-depth; an update never touches the DB, but a
	// bad release shouldn't be able to strand the operator). Non-fatal on failure.
	if backupFn != nil {
		_ = backupFn()
	}
	// Atomic swap with a rollback copy: move the current binary aside to <exe>.bak,
	// then rename the verified new binary into place. Replacing a running binary by
	// rename is safe on Linux (the old inode stays open until the process exits).
	bak := exe + ".bak"
	_ = os.Remove(bak)
	if err := os.Rename(exe, bak); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("could not keep a backup copy: %w", err)
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Rename(bak, exe) // roll back to the previous binary
		_ = os.Remove(tmp)
		return fmt.Errorf("replacing the binary failed: %w", err)
	}
	return nil
}

// expectedSum fetches the release's SHA256SUMS and returns the hex digest for
// `name` (lines are "<hex>  <filename>").
func expectedSum(ctx context.Context, url, name string) (string, error) {
	body, err := fetchText(ctx, url)
	if err != nil {
		return "", err
	}
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return f[0], nil
		}
	}
	return "", fmt.Errorf("no SHA256SUMS entry for %s", name)
}

// fileSum returns the hex SHA-256 of the file at path.
func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fetchText(ctx context.Context, url string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "rospanel-updater")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	return string(b), err
}

// binaryVersion is the version the binary at path reports ("" when it cannot say):
// the last word of `<path> version`, without a leading v.
func binaryVersion(path string) string {
	out, err := exec.Command(path, "version").Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return ""
	}
	return strings.TrimPrefix(f[len(f)-1], "v")
}

// downloadFile makes the file a release is downloaded into: its own, beside the
// binary (the swap is a rename), so another process updating at the same moment —
// `rospanel update` — cannot truncate the file this one verified; and runnable, since
// the new binary is run once before the swap.
func downloadFile(exe string) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(exe), filepath.Base(exe)+".new-*")
	if err != nil {
		return "", fmt.Errorf("could not create the download file: %w", err)
	}
	name := f.Name()
	_ = f.Close()
	if err := os.Chmod(name, 0o755); err != nil { // CreateTemp makes it 0600
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func download(ctx context.Context, url, dst string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "rospanel-updater")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	// Capped like the manifest and checksum reads above. The checksum is only verified
	// AFTER the body has landed on disk, and this writes into /usr/local/bin on the root
	// filesystem — so an unbounded body from a hijacked or misbehaving release host
	// fills the disk (taking every SQLite write down with it) long before the bytes are
	// ever authenticated. +1 so an over-cap asset is detected rather than silently
	// truncated into a binary that would then fail the checksum anyway.
	var n int64
	n, err = io.Copy(f, io.LimitReader(resp.Body, maxAssetBytes+1))
	if err == nil && n > maxAssetBytes {
		err = fmt.Errorf("release asset exceeds %d bytes", maxAssetBytes)
	}
	if err == nil {
		err = f.Sync() // durably flush before the swap/exec relies on the bytes
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = fsyncDir(filepath.Dir(dst))
	}
	if err != nil {
		_ = os.Remove(dst)
	}
	return err
}

// fsyncDir flushes a directory entry so a freshly-created/renamed file survives a
// crash right after the swap.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// Restart asks systemd to restart the rospanel service in a DECOUPLED transient
// unit, so the restart (which kills this very process and its child Xray) can't
// abort itself. A short delay lets the triggering HTTP response flush first.
func Restart() {
	if err := exec.Command("systemd-run", "--collect", "--on-active=2",
		"systemctl", "restart", "rospanel").Run(); err == nil {
		return
	}
	go func() {
		time.Sleep(time.Second)
		if err := exec.Command("systemctl", "restart", "rospanel").Run(); err != nil {
			slog.Error("update: the service could not be restarted — the new version starts with the next restart", "err", err)
		}
	}()
}
