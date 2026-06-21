// Package backup creates and restores tar.gz snapshots of the data directory
// (SQLite DB, certs, generated config, ACME account). The master state needed to
// reproduce a working panel lives entirely under the data dir.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// zeroReader yields an endless stream of zero bytes, used to pad a tar entry
// when a file shrank between being stat'd and copied.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// Manifest is written as manifest.json (the first tar entry) in every backup.
// It lets the operator preview what a backup contains before restoring.
type Manifest struct {
	Domain     string `json:"domain"`
	SecretPath string `json:"secret_path"`
	UserCount  int    `json:"user_count"`
	CreatedAt  string `json:"created_at"`
}

// WriteWithManifest streams a gzip-compressed tar of dataDir to w, prepending
// manifest.json as the first entry so inspectors can peek at it cheaply.
func WriteWithManifest(dataDir string, m Manifest, w io.Writer) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	// First entry: the manifest.
	mdata, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: "manifest.json",
		Mode: 0o600,
		Size: int64(len(mdata)),
	}); err != nil {
		return err
	}
	if _, err := tw.Write(mdata); err != nil {
		return err
	}

	// Remaining entries: the data directory.
	err = filepath.Walk(dataDir, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		// Skip dirs that don't belong in a backup: re-downloadable assets (the
		// auto-installed Xray binary in bin/, the geoip/geosite databases in geo/,
		// the opera-proxy helper in opera/ — all re-fetched on demand), transient
		// restore staging, and logs/ (operational + actively appended, which would
		// otherwise grow mid-archive and trip "write too long").
		if info.IsDir() && (path == filepath.Join(dataDir, "bin") ||
			path == filepath.Join(dataDir, "geo") ||
			path == filepath.Join(dataDir, "opera") ||
			path == filepath.Join(dataDir, "logs") ||
			path == filepath.Join(dataDir, stagingDir)) {
			return filepath.SkipDir
		}
		base := filepath.Base(path)
		if strings.HasSuffix(path, ".db-wal") || strings.HasSuffix(path, ".db-shm") {
			return nil
		}
		// Skip recovery artifacts (.bak, .bak-20060102-150405, .new).
		if strings.HasSuffix(base, ".bak") || strings.Contains(base, ".bak-") || strings.HasSuffix(base, ".new") {
			return nil
		}
		rel, err := filepath.Rel(dataDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		// Write exactly hdr.Size bytes regardless of the file changing under us
		// between the Walk stat and now: io.CopyN never overflows the tar entry (a
		// grown file is truncated to the recorded size), and a shrunk file is
		// zero-padded so the entry still matches its header.
		n, cerr := io.CopyN(tw, src, hdr.Size)
		if cerr == io.EOF {
			cerr = nil
		}
		if cerr != nil {
			return cerr
		}
		if n < hdr.Size {
			_, err = io.CopyN(tw, zeroReader{}, hdr.Size-n)
		}
		return err
	})
	if err != nil {
		_ = tw.Close()
		_ = gz.Close()
		return err
	}
	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return err
	}
	return gz.Close()
}

// write streams a gzip-compressed tar of dataDir to w (no manifest).
func write(dataDir string, w io.Writer) error {
	return WriteWithManifest(dataDir, Manifest{}, w)
}

// Create writes a gzip-compressed tar of dataDir to the named file.
func Create(dataDir, out string) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	return write(dataDir, f)
}

// ReadManifest reads the manifest.json entry from a backup tar.gz stream.
// It stops reading as soon as the manifest is found, so only a small prefix
// of a large archive needs to be decompressed.
func ReadManifest(r io.Reader) (Manifest, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return Manifest{}, fmt.Errorf("not a valid gzip archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Manifest{}, fmt.Errorf("reading archive: %w", err)
		}
		if hdr.Name == "manifest.json" {
			var m Manifest
			if err := json.NewDecoder(tr).Decode(&m); err != nil {
				return Manifest{}, fmt.Errorf("invalid manifest: %w", err)
			}
			return m, nil
		}
		if _, err := io.Copy(io.Discard, tr); err != nil {
			return Manifest{}, err
		}
	}
	return Manifest{}, fmt.Errorf("manifest.json not found in backup (old format?)")
}

// stagingDir is where a restore is unpacked, inside dataDir, to be applied on the
// next boot. readyMarker signals the unpack completed.
const (
	stagingDir  = ".restore"
	readyMarker = ".ready"
)

// StageRestore unpacks a backup into a staging area inside dataDir and marks it
// ready. The restore is applied by ApplyPending on the next boot, BEFORE the
// database is opened — so a live SQLite WAL can't checkpoint stale data back over
// the restored DB (which would silently revert the restore). On failure the live
// data is left untouched.
func StageRestore(file, dataDir string) error {
	staging := filepath.Join(dataDir, stagingDir)
	if err := os.RemoveAll(staging); err != nil {
		return err
	}
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return err
	}
	if err := Restore(file, staging); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	return os.WriteFile(filepath.Join(staging, readyMarker), []byte("1"), 0o600)
}

// ApplyPending applies a staged restore (if any) over dataDir, then clears the
// staging area. Call it on boot before opening the database. No-op when nothing
// is staged. Re-downloadable assets not present in the backup (bin/, geo/) are
// left in place.
func ApplyPending(dataDir string) (applied bool, err error) {
	staging := filepath.Join(dataDir, stagingDir)
	if _, err := os.Stat(filepath.Join(staging, readyMarker)); err != nil {
		return false, nil
	}
	// Drop the live DB sidecars so a stale WAL can't revert the restored DB.
	for _, f := range []string{"rospanel.db-wal", "rospanel.db-shm"} {
		_ = os.Remove(filepath.Join(dataDir, f))
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.Name() == readyMarker {
			continue
		}
		dst := filepath.Join(dataDir, e.Name())
		if err := os.RemoveAll(dst); err != nil {
			return false, err
		}
		if err := os.Rename(filepath.Join(staging, e.Name()), dst); err != nil {
			return false, err
		}
	}
	return true, os.RemoveAll(staging)
}

// Restore extracts a backup tarball into dataDir (which must exist).
func Restore(file, dataDir string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		// Skip the manifest — it's not part of the data directory.
		if hdr.Name == "manifest.json" {
			if _, err := io.Copy(io.Discard, tr); err != nil {
				return err
			}
			continue
		}
		// Guard against path traversal.
		clean := filepath.Clean(hdr.Name)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return fmt.Errorf("unsafe path in archive: %s", hdr.Name)
		}
		target := filepath.Join(dataDir, clean)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		default:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
	return nil
}
