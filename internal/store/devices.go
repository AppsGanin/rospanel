package store

import (
	"database/sql"

	"github.com/AppsGanin/rospanel/internal/model"
)

// DeviceAdmission is what RegisterDevice decided about one subscription fetch.
type DeviceAdmission struct {
	Allowed bool // false ⇒ the cap is full and this device is not one of the bound ones
	New     bool // true ⇒ this fetch is what bound the device
	Count   int  // devices bound to the user after the call
}

// RegisterDevice admits a client install to a user's device roster, enforcing the
// cap in the same transaction that writes the row.
//
// The check and the insert MUST NOT be separate statements. Two clients fetching at
// once against a full-but-one roster would both read "one slot left" and both take
// it — the bug Remnawave shipped and had to issue an advisory for
// (GHSA-985p-44h5-v3pq). Here the cap lives inside the INSERT's WHERE clause, so
// SQLite's single writer serialises the two attempts and the second one inserts zero
// rows.
//
// limit 0 means unlimited. An already-bound device always passes: the cap governs
// how many devices exist, not how often they refresh.
// maxDevicesPerUser mirrors model.MaxDevicesPerUser. It is applied here as well as in
// Settings.DeviceCap so a direct store caller (a test, a future job) cannot bypass the
// ceiling by passing 0 — the roster is written from an unauthenticated fetch, so this is
// the last line rather than the only one.
const maxDevicesPerUser = model.MaxDevicesPerUser

func (s *Store) RegisterDevice(userID int64, d model.Device, limit int) (DeviceAdmission, error) {
	// "No limit given" gets the default cap. An explicit number is honoured as-is —
	// this is a floor, not a clamp. The roster is written from an
	// UNAUTHENTICATED fetch carrying an attacker-chosen x-hwid, and the shipped default
	// is exactly this case (hwid_fallback_limit defaults to 0) — so without a ceiling one
	// subscription token can insert a row per request, forever, on a single-connection
	// SQLite. An operator's explicit number is honoured as-is; this only bounds "no
	// number given".
	if limit <= 0 {
		limit = maxDevicesPerUser
	}
	var out DeviceAdmission
	err := s.withTx(func(tx *sql.Tx) error {
		// Refresh first. The overwhelming majority of fetches are a known device
		// updating its subscription, and that path never needs to count anything.
		//
		// The descriptive fields are only overwritten when the client sent them. Only
		// x-hwid is required by the convention, and a fetch without the rest — a curl,
		// a client that sends the id alone, a client version that dropped them — would
		// otherwise blank the model and OS of a device that had introduced itself
		// properly, leaving the operator (and the owner) a row that says nothing about
		// which phone it is. The address is different: it is "where it fetched from
		// last", so the newest value always wins.
		res, err := tx.Exec(`
			UPDATE devices SET last_seen = ?, ip = ?,
			       os         = CASE WHEN ? <> '' THEN ? ELSE os         END,
			       os_version = CASE WHEN ? <> '' THEN ? ELSE os_version END,
			       model      = CASE WHEN ? <> '' THEN ? ELSE model      END,
			       app        = CASE WHEN ? <> '' THEN ? ELSE app        END
			WHERE user_id = ? AND hwid = ?`,
			d.LastSeen, d.IP,
			d.OS, d.OS, d.OSVersion, d.OSVersion, d.Model, d.Model, d.App, d.App,
			userID, d.HWID,
		)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			out.Allowed, out.New = true, false
			out.Count, err = countDevicesOn(tx, userID)
			return err
		}
		// A new device. The WHERE clause is the cap: it either sees room and inserts,
		// or sees none and inserts nothing. The EXISTS guard mirrors AddConnections —
		// user_id is a foreign key and a deleted user's client can keep fetching with
		// a token that is already gone.
		res, err = tx.Exec(`
			INSERT INTO devices
				(user_id, hwid, os, os_version, model, app, ip, first_seen, last_seen)
			SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?
			WHERE EXISTS (SELECT 1 FROM users WHERE id = ?)
			  AND (? = 0 OR (SELECT COUNT(*) FROM devices WHERE user_id = ?) < ?)`,
			userID, d.HWID, d.OS, d.OSVersion, d.Model, d.App, d.IP, d.LastSeen, d.LastSeen,
			userID, limit, userID, limit,
		)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		out.Allowed, out.New = n > 0, n > 0
		out.Count, err = countDevicesOn(tx, userID)
		return err
	})
	return out, err
}

// CountDevices returns how many devices are bound to a user.
func (s *Store) CountDevices(userID int64) (int, error) { return countDevicesOn(s.db, userID) }

func countDevicesOn(q queryer, userID int64) (int, error) {
	var n int
	err := q.QueryRow(`SELECT COUNT(*) FROM devices WHERE user_id = ?`, userID).Scan(&n)
	return n, err
}

// CountAllDevices returns how many devices are bound across every user — the one
// number the metrics endpoint publishes about the roster.
func (s *Store) CountAllDevices() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM devices`).Scan(&n)
	return n, err
}

// ListDevices returns a user's bound devices, most recently seen first.
func (s *Store) ListDevices(userID int64) ([]model.Device, error) {
	rows, err := s.db.Query(`
		SELECT hwid, os, os_version, model, app, ip, first_seen, last_seen
		FROM devices WHERE user_id = ? ORDER BY last_seen DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Device{}
	for rows.Next() {
		var d model.Device
		if err := rows.Scan(
			&d.HWID, &d.OS, &d.OSVersion, &d.Model, &d.App, &d.IP, &d.FirstSeen, &d.LastSeen,
		); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeviceCounts returns the bound-device count for each of the given users, omitting
// those with none. Batched for the user list, which would otherwise run a COUNT per
// row; the list handlers call it once per page.
func (s *Store) DeviceCounts(userIDs []int64) (map[int64]int, error) {
	if len(userIDs) == 0 {
		return map[int64]int{}, nil
	}
	args := make([]any, len(userIDs))
	for i, id := range userIDs {
		args[i] = id
	}
	rows, err := s.db.Query(
		`SELECT user_id, COUNT(*) FROM devices WHERE user_id IN (`+placeholders(len(args))+`)
		 GROUP BY user_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]int, len(userIDs))
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// DeleteDevice unbinds one device, freeing its slot. Reports whether a row was
// actually removed, so the caller can answer 404 for an id that was never bound
// instead of silently succeeding.
func (s *Store) DeleteDevice(userID int64, hwid string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM devices WHERE user_id = ? AND hwid = ?`, userID, hwid)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeleteDevices unbinds every device of a user, returning how many were removed.
// This is the "they lost their phone" button, and it is also what a subscription
// token rotation calls: the old token's devices have no claim on the new one.
func (s *Store) DeleteDevices(userID int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM devices WHERE user_id = ?`, userID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// PurgeDevices forgets devices not seen since the cutoff (unix seconds), returning
// how many were removed. Batched for the same reason the other sweeps are: the pool
// is a single connection, so one unbounded DELETE stalls every query behind it.
func (s *Store) PurgeDevices(before int64) (int64, error) {
	var total int64
	for {
		res, err := s.db.Exec(
			`DELETE FROM devices WHERE rowid IN (
				SELECT rowid FROM devices WHERE last_seen < ? LIMIT ?
			)`, before, purgeBatch)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		if n < purgeBatch {
			return total, nil
		}
	}
}
