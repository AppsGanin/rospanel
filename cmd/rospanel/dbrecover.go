package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/AppsGanin/rospanel/internal/backup"
	"github.com/AppsGanin/rospanel/internal/store"
)

// ensureHealthyDB gates the boot on a readable database, and recovers from the one
// failure that otherwise ends the install: SQLite reporting the file as corrupt.
//
// A hard reboot or a full disk can tear a page and leave "file is not a database"
// behind. Without this the panel crash-loops forever on a file it will never be
// able to read, and the operator's only clue is a stack trace. So: quarantine the
// damaged file (never delete it — it's the only forensic copy, and it may still be
// partially recoverable by hand) and extract the newest local backup in its place.
//
// Recovery is deliberately restricted to store.ErrCorrupt. A locked file, bad
// permissions or a full disk are all transient or operator-fixable, and restoring
// over them would destroy good data to "fix" a problem that isn't there.
//
// Runs before datasec.Init, because a backup carries its own secrets.key: pulling
// the archive's DB and key in as a pair keeps encrypted columns decryptable, while
// restoring only the DB after the key was already loaded would not.
func ensureHealthyDB(dbPath, dataDir string) error {
	err := store.Check(dbPath)
	if err == nil {
		return nil
	}
	if !errors.Is(err, store.ErrCorrupt) {
		return err
	}

	log.Printf("[ALERT] database: %v", err)
	log.Printf("[ALERT] database: the file is damaged — attempting recovery from the newest local backup")

	archives, lerr := backup.ListLocal(dataDir) // newest first
	if lerr != nil {
		return fmt.Errorf("database is corrupt and the backup directory is unreadable (%v) — "+
			"restore a backup by hand: rospanel restore <file>", lerr)
	}
	if len(archives) == 0 {
		return fmt.Errorf("database is corrupt and there is no local backup to restore from. "+
			"The damaged file is left at %s. Restore an off-box backup with `rospanel restore <file>`, "+
			"or wipe and start fresh with `rospanel reset`. "+
			"Turn on scheduled local backups (Настройки → Бэкапы) so this is recoverable next time", dbPath)
	}

	quarantine, qerr := quarantineDB(dbPath)
	if qerr != nil {
		return fmt.Errorf("database is corrupt and could not be set aside for recovery: %w", qerr)
	}

	newest := filepath.Join(dataDir, backup.LocalBackupDir, archives[0])
	if rerr := backup.Restore(newest, dataDir); rerr != nil {
		return fmt.Errorf("database is corrupt and restoring %s failed: %w "+
			"(the damaged database is preserved at %s)", archives[0], rerr, quarantine)
	}

	// The archive could itself be damaged or truncated. If what we just restored is
	// also unreadable, stop: a boot loop that keeps unpacking a broken archive over
	// the data dir is worse than a clean failure.
	if cerr := store.Check(dbPath); cerr != nil {
		return fmt.Errorf("restored %s but the database is still unusable: %w "+
			"(the original damaged database is preserved at %s)", archives[0], cerr, quarantine)
	}

	log.Printf("[ALERT] database: recovered from backup %s — changes made after that backup are LOST", archives[0])
	log.Printf("[ALERT] database: the damaged file is preserved at %s", quarantine)
	return nil
}

// quarantineDB moves the damaged database aside (with its WAL and shared-memory
// sidecars, which belong to it and would otherwise be replayed onto the restored
// file) and returns the path it was moved to.
func quarantineDB(dbPath string) (string, error) {
	dst := fmt.Sprintf("%s.corrupt-%s", dbPath, time.Now().Format("20060102-150405"))
	if err := os.Rename(dbPath, dst); err != nil {
		return "", err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Rename(dbPath+suffix, dst+suffix); err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	return dst, nil
}
