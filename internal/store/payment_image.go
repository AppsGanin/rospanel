package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// The manual payment method's picture (see migrations/0139_manual_pay_image.sql).
// One row, id = 1. Readers ask for the version far more often than for the bytes: the
// subscription page needs only the ?v= to build the URL, and the picture itself is
// fetched by the browser. Both reads are bounded lookups, so they go to the read pool
// and never hold the single writer for the duration of a megabyte.

// ManualPayImageVersion returns the content hash of the stored picture, or "" when
// there is none.
func (s *Store) ManualPayImageVersion() (string, error) {
	var v string
	err := s.rdb.QueryRow(`SELECT version FROM manual_pay_image WHERE id = 1`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// ManualPayImage returns the stored picture, or nil when there is none.
func (s *Store) ManualPayImage() ([]byte, error) {
	var b []byte
	err := s.rdb.QueryRow(`SELECT image FROM manual_pay_image WHERE id = 1`).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return b, err
}

// SetManualPayImage stores the picture (validation is the caller's) and returns its
// new version.
func (s *Store) SetManualPayImage(image []byte) (string, error) {
	sum := sha256.Sum256(image)
	version := hex.EncodeToString(sum[:8])
	_, err := s.db.Exec(
		`INSERT INTO manual_pay_image (id, image, version, updated_at) VALUES (1, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   image = excluded.image, version = excluded.version, updated_at = excluded.updated_at`,
		image, version, time.Now().Unix())
	return version, err
}

// DeleteManualPayImage removes the picture; deleting when there is none is not an
// error.
func (s *Store) DeleteManualPayImage() error {
	_, err := s.db.Exec(`DELETE FROM manual_pay_image WHERE id = 1`)
	return err
}
