package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Extra AmneziaWG keys per user, one per device. See migration 0147.

// ErrAWGKeysFull is CreateAWGDevice refusing a key past the user's cap.
var ErrAWGKeysFull = errors.New("awg keys: the user's cap is full")

// ErrAWGSubnetFull is CreateAWGDevice finding no free address on the tunnel subnet.
var ErrAWGSubnetFull = errors.New("awg keys: no address left on the tunnel subnet")

const awgDeviceCols = `id, user_id, name, hwid, private_key, slot, created_at`

func scanAWGDevice(sc interface{ Scan(...any) error }) (model.AWGDevice, error) {
	var d model.AWGDevice
	err := sc.Scan(&d.ID, &d.UserID, &d.Name, &d.HWID, &d.PrivateKey, &d.Slot, &d.CreatedAt)
	d.PrivateKey = decField(d.PrivateKey)
	return d, err
}

// ListAWGDevices returns a user's extra keys, oldest first.
func (s *Store) ListAWGDevices(userID int64) ([]model.AWGDevice, error) {
	rows, err := s.db.Query(`SELECT `+awgDeviceCols+` FROM user_awg_devices WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AWGDevice
	for rows.Next() {
		d, err := scanAWGDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetAWGDevice returns one of a user's keys; nil when the user has no such key.
func (s *Store) GetAWGDevice(userID, id int64) (*model.AWGDevice, error) {
	d, err := scanAWGDevice(s.db.QueryRow(`SELECT `+awgDeviceCols+` FROM user_awg_devices WHERE user_id = ? AND id = ?`, userID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// AWGDeviceByHWID returns the key made for a subscription client; nil when none.
func (s *Store) AWGDeviceByHWID(userID int64, hwid string) (*model.AWGDevice, error) {
	d, err := scanAWGDevice(s.db.QueryRow(`SELECT `+awgDeviceCols+` FROM user_awg_devices WHERE user_id = ? AND hwid = ?`, userID, hwid))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// usedAWGSlots is every slot taken on the tunnel subnet: the users' own and the
// devices'.
func usedAWGSlots(tx *sql.Tx) (map[int]bool, error) {
	used := map[int]bool{}
	for _, q := range []string{
		`SELECT awg_slot FROM users WHERE awg_slot > 0`,
		`SELECT slot FROM user_awg_devices`,
	} {
		rows, err := tx.Query(q)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var slot int
			if err := rows.Scan(&slot); err != nil {
				rows.Close()
				return nil, err
			}
			used[slot] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return used, nil
}

// CreateAWGDevice adds a key to a user, on the lowest free slot in [first, last],
// as long as the user holds fewer than maxKeys keys counting their main one. The
// count, the slot and the insert are one transaction, so two requests cannot both
// take the last place. With a hwid already holding a key, that key is returned and
// nothing is made.
func (s *Store) CreateAWGDevice(d model.AWGDevice, maxKeys, first, last int) (*model.AWGDevice, error) {
	var out *model.AWGDevice
	err := s.withTx(func(tx *sql.Tx) error {
		if d.HWID != "" {
			cur, err := scanAWGDevice(tx.QueryRow(`SELECT `+awgDeviceCols+` FROM user_awg_devices WHERE user_id = ? AND hwid = ?`, d.UserID, d.HWID))
			if err == nil {
				out = &cur
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM user_awg_devices WHERE user_id = ?`, d.UserID).Scan(&n); err != nil {
			return err
		}
		if n+1 >= maxKeys { // +1: the main key
			return ErrAWGKeysFull
		}
		used, err := usedAWGSlots(tx)
		if err != nil {
			return err
		}
		slot := first
		for slot <= last && used[slot] {
			slot++
		}
		if slot > last {
			return ErrAWGSubnetFull
		}
		res, err := tx.Exec(`INSERT INTO user_awg_devices (user_id, name, hwid, private_key, slot, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`, d.UserID, d.Name, d.HWID, encField(d.PrivateKey), slot, d.CreatedAt)
		if err != nil {
			return err
		}
		d.ID, _ = res.LastInsertId()
		d.Slot = slot
		out = &d
		return nil
	})
	return out, err
}

// RenameAWGDevice changes a key's name. false when the user has no such key.
func (s *Store) RenameAWGDevice(userID, id int64, name string) (bool, error) {
	res, err := s.db.Exec(`UPDATE user_awg_devices SET name = ? WHERE user_id = ? AND id = ?`, name, userID, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeleteAWGDevice removes one of a user's keys. false when there was no such key.
func (s *Store) DeleteAWGDevice(userID, id int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM user_awg_devices WHERE user_id = ? AND id = ?`, userID, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeleteAWGDeviceByHWID removes the key made for a subscription client.
func (s *Store) DeleteAWGDeviceByHWID(userID int64, hwid string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM user_awg_devices WHERE user_id = ? AND hwid = ?`, userID, hwid)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// RekeyAWGDevices gives each of a user's keys a new private key from gen, keeping
// its slot: the address stays, the old key stops working.
func (s *Store) RekeyAWGDevices(userID int64, gen func() (string, error)) error {
	return s.withTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT id FROM user_awg_devices WHERE user_id = ?`, userID)
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range ids {
			key, err := gen()
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE user_awg_devices SET private_key = ? WHERE id = ?`, encField(key), id); err != nil {
				return err
			}
		}
		return nil
	})
}

// AllAWGDeviceKeys returns every extra key by user, each user's oldest first.
func (s *Store) AllAWGDeviceKeys() (map[int64][]model.AWGKey, error) {
	rows, err := s.db.Query(`SELECT user_id, id, private_key, slot FROM user_awg_devices ORDER BY user_id, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]model.AWGKey{}
	for rows.Next() {
		var uid int64
		var k model.AWGKey
		if err := rows.Scan(&uid, &k.ID, &k.Key, &k.Slot); err != nil {
			return nil, err
		}
		k.Key = decField(k.Key)
		out[uid] = append(out[uid], k)
	}
	return out, rows.Err()
}

// attachAWGDevices puts every user's extra keys in force on them: as many as their
// device cap leaves room for, oldest first (model.ActiveAWGDevices). A table most
// panels leave empty: one cheap read, skipped entirely when it is.
func (s *Store) attachAWGDevices(users []model.User) error {
	keys, err := s.AllAWGDeviceKeys()
	if err != nil || len(keys) == 0 {
		return err
	}
	set, err := s.GetSettings()
	if err != nil {
		return err
	}
	for i := range users {
		if k, ok := keys[users[i].ID]; ok {
			if n := model.ActiveAWGDevices(len(k), set.AWGKeyCap(users[i])); n > 0 {
				users[i].AWGDevices = k[:n:n]
			}
		}
	}
	return nil
}

// DeleteHWIDAWGKeys removes every key a user's subscription clients were given (the
// ones made by hand stay), returning how many went: their devices were released.
func (s *Store) DeleteHWIDAWGKeys(userID int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM user_awg_devices WHERE user_id = ? AND hwid <> ''`, userID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// PurgeIdleAWGKeys removes the keys of subscription clients whose bound device was
// last seen before the cutoff — the devices PurgeDevices is about to forget. Called
// before it, while those rows still say which keys were theirs.
func (s *Store) PurgeIdleAWGKeys(before int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM user_awg_devices WHERE hwid <> '' AND EXISTS (
		SELECT 1 FROM devices d WHERE d.user_id = user_awg_devices.user_id
		    AND d.hwid = user_awg_devices.hwid AND d.last_seen < ?)`, before)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// AWGKeysSignature names the device keys in force across every user — which keys,
// under which caps (model.ActiveAWGDevices) — so a sync can tell that the set changed
// even when no key was added or deleted: a user's device cap lowered or raised, or
// the panel default changed. "" when there are no device keys at all.
func (s *Store) AWGKeysSignature() (string, error) {
	rows, err := s.db.Query(`SELECT d.user_id, d.id, u.hwid_limit FROM user_awg_devices d
		JOIN users u ON u.id = d.user_id ORDER BY d.user_id, d.id`)
	if err != nil {
		return "", err
	}
	type row struct {
		user, id int64
		limit    int
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.user, &r.id, &r.limit); err != nil {
			rows.Close()
			return "", err
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(all) == 0 {
		return "", err
	}
	set, err := s.GetSettings()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for i := 0; i < len(all); {
		j := i
		for j < len(all) && all[j].user == all[i].user {
			j++
		}
		n := model.ActiveAWGDevices(j-i, set.AWGKeyCap(model.User{HWIDLimit: all[i].limit}))
		for _, r := range all[i : i+n] {
			fmt.Fprintf(&b, "%d:%d,", r.user, r.id)
		}
		i = j
	}
	return b.String(), nil
}
