package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/backup"
	"github.com/Shu1t3/rospanel-shu1t3/internal/store"
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
			"Turn on scheduled local backups (Settings → Backups) so this is recoverable next time", dbPath)
	}

	quarantine, qerr := quarantineDB(dbPath)
	if qerr != nil {
		return fmt.Errorf("database is corrupt and could not be set aside for recovery: %w", qerr)
	}

	// Walk the archives newest-first and stop at the first one that yields a READABLE
	// database. Trying only the newest meant a single damaged archive doomed the box
	// even with good older ones sitting next to it — and, because the process exits
	// non-zero and systemd restarts it, that same broken archive was unpacked again on
	// every boot, quarantining another multi-MB copy of the database each time until
	// systemd's start limit gave up.
	var lastErr error
	for _, name := range archives {
		path := filepath.Join(dataDir, backup.LocalBackupDir, name)
		// VET THE ARCHIVE IN A SCRATCH DIRECTORY FIRST. Extracting straight over the
		// data dir and judging afterwards meant a rejected archive still left everything
		// that is not the database behind — and a backup carries secrets.key, so the box
		// ended up with one archive's encryption key beside another archive's (or the
		// quarantined) database, which decrypts every stored secret to garbage.
		if err := vetArchive(path); err != nil {
			lastErr = fmt.Errorf("%s: %w", name, err)
			log.Printf("[ALERT] database: %v — trying an older backup", lastErr)
			continue
		}
		if rerr := backup.Restore(path, dataDir); rerr != nil {
			lastErr = fmt.Errorf("restoring %s failed: %w", name, rerr)
			log.Printf("[ALERT] database: %v — trying an older backup", lastErr)
			continue
		}
		// It passed in the scratch copy; confirm the copy that actually landed.
		if cerr := store.Check(dbPath); cerr != nil {
			lastErr = fmt.Errorf("restored %s but the database is still unusable: %w", name, cerr)
			log.Printf("[ALERT] database: %v — trying an older backup", lastErr)
			continue
		}
		log.Printf("[ALERT] database: recovered from backup %s — changes made after that backup are LOST", name)
		log.Printf("[ALERT] database: the damaged file is preserved at %s", quarantine)
		return nil
	}

	// Nothing restored cleanly. Put the original back: a half-written or absent dbPath
	// would otherwise read as a FRESH INSTALL on the next boot and come up blank on
	// admin/admin, silently masking the corruption — and leaving the quarantine copy in
	// place on every attempt is what grew the disk. Restoring it makes each boot fail
	// identically and idempotently, which is the clean failure this is supposed to be.
	restoreQuarantine(quarantine, dbPath)
	return fmt.Errorf("database is corrupt and none of the %d local backups could be restored "+
		"(last error: %v) — restore an off-box backup with `rospanel restore <file>`", len(archives), lastErr)
}

// restoreQuarantine moves a quarantined database (and its sidecars) back into place,
// best-effort: it runs on a path that is already failing, and leaving the original where
// the next boot will find it matters more than any single rename succeeding.
func restoreQuarantine(quarantine, dbPath string) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(dbPath + suffix) // drop whatever a partial restore left behind
		if _, err := os.Stat(quarantine + suffix); err == nil {
			_ = os.Rename(quarantine+suffix, dbPath+suffix)
		}
	}
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
			// The main DB is already moved aside. Returning now would leave no file at
			// dbPath, and store.Check reads a missing DB as a FRESH INSTALL — the next
			// boot would come up blank on admin/admin at the default secret path, with
			// the real data sitting in the quarantine copy. Put it back and fail loudly
			// instead. (The trigger is plausible: the same disk-full that corrupted the
			// DB also fails these renames, whose names are longer.)
			_ = os.Rename(dst, dbPath)
			return "", err
		}
	}
	return dst, nil
}

// vetArchive unpacks a backup somewhere harmless and reports why it cannot be used, or
// nil when it can. Everything it checks is checked BEFORE the archive is allowed near
// the live data directory.
func vetArchive(path string) error {
	dir, err := os.MkdirTemp("", "rospanel-recover-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := backup.Restore(path, dir); err != nil {
		return fmt.Errorf("archive is unreadable: %w", err)
	}
	db := filepath.Join(dir, "rospanel.db")
	if err := store.Check(db); err != nil {
		return fmt.Errorf("the database in it is unusable: %w", err)
	}
	// store.Check reads a MISSING file as "fresh install", so absence has to be its own
	// check — an archive with no database would otherwise pass and boot the panel blank.
	if _, serr := os.Stat(db); serr != nil {
		return fmt.Errorf("it contains no database")
	}
	v, err := store.DBSchemaVersion(db)
	if err != nil {
		return fmt.Errorf("its schema version is unreadable: %w", err)
	}
	if v > store.SchemaVersion() {
		return fmt.Errorf("it was written by a newer panel (schema %d, this build knows %d)",
			v, store.SchemaVersion())
	}
	return nil
}
