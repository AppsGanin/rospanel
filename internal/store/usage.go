package store

import (
	"database/sql"
)

// SubClient is one install that fetched a user's subscription.
type SubClient struct {
	UserID    int64  `json:"-"`
	Key       string `json:"id"` // "h:<hwid>" or "a:<app>|<platform>": one install
	App       string `json:"app"`
	Version   string `json:"version"`
	Platform  string `json:"platform"`
	Model     string `json:"model"`
	// IP is the address this install last fetched from, empty for a row recorded
	// before the column existed.
	IP        string `json:"ip"`
	UA        string `json:"ua"`
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
	Fetches   int64  `json:"fetches"`
}

// SubClientsPerUser bounds a user's rows: the token holder picks the User-Agent and
// the x-hwid, so without it one subscription could grow the table without end. The
// least recently seen rows go first.
const SubClientsPerUser = 20

// RecordSubClients upserts a batch of fetches (FirstSeen/LastSeen the first and last
// fetch in the batch, Fetches how many), then trims each user named to
// SubClientsPerUser rows.
func (s *Store) RecordSubClients(list []SubClient) error {
	if len(list) == 0 {
		return nil
	}
	return s.withTx(func(tx *sql.Tx) error {
		up, err := tx.Prepare(`INSERT INTO sub_clients
			(user_id, client_key, app, version, platform, model, ip, ua, first_seen, last_seen, fetches)
			SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM users WHERE id = ?)
			ON CONFLICT (user_id, client_key) DO UPDATE SET
				app = excluded.app, version = excluded.version, platform = excluded.platform,
				model = CASE WHEN excluded.model <> '' THEN excluded.model ELSE model END,
				ip = CASE WHEN excluded.ip <> '' THEN excluded.ip ELSE ip END,
				ua = excluded.ua, last_seen = MAX(last_seen, excluded.last_seen),
				fetches = fetches + excluded.fetches`)
		if err != nil {
			return err
		}
		defer up.Close()
		trim, err := tx.Prepare(`DELETE FROM sub_clients WHERE user_id = ? AND client_key NOT IN (
			SELECT client_key FROM sub_clients WHERE user_id = ? ORDER BY last_seen DESC, client_key LIMIT ?)`)
		if err != nil {
			return err
		}
		defer trim.Close()
		users := map[int64]bool{}
		for _, c := range list {
			// A user deleted since the fetch was buffered is skipped (the EXISTS), not
			// refused by the foreign key at the cost of the rest of the batch.
			if _, err := up.Exec(c.UserID, c.Key, c.App, c.Version, c.Platform, c.Model, c.IP, c.UA,
				c.FirstSeen, c.LastSeen, max(c.Fetches, 1), c.UserID); err != nil {
				return err
			}
			users[c.UserID] = true
		}
		for id := range users {
			if _, err := trim.Exec(id, id, SubClientsPerUser); err != nil {
				return err
			}
		}
		return nil
	})
}

// UserSubClients is a user's installs, most recently seen first.
func (s *Store) UserSubClients(userID int64) ([]SubClient, error) {
	rows, err := s.rdb.Query(`SELECT client_key, app, version, platform, model, ip, ua, first_seen, last_seen, fetches
		FROM sub_clients WHERE user_id = ? ORDER BY last_seen DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SubClient{}
	for rows.Next() {
		c := SubClient{UserID: userID}
		if err := rows.Scan(&c.Key, &c.App, &c.Version, &c.Platform, &c.Model, &c.IP, &c.UA,
			&c.FirstSeen, &c.LastSeen, &c.Fetches); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ClientShare is how many users fetched with one app, or from one platform.
type ClientShare struct {
	Name  string `json:"name"`
	Users int64  `json:"users"`
}

// ClientShares counts distinct users per app and per platform among the fetches
// seen in [since, until) (unix seconds), most users first, and the distinct users in all.
func (s *Store) ClientShares(since, until int64) (apps, platforms []ClientShare, users int64, err error) {
	read := func(col string) ([]ClientShare, error) {
		// col is one of the two literals below.
		rows, err := s.rdb.Query(`SELECT `+col+`, COUNT(DISTINCT user_id) AS n FROM sub_clients
			WHERE last_seen >= ? AND last_seen < ? GROUP BY `+col+` ORDER BY n DESC, `+col, since, until)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []ClientShare{}
		for rows.Next() {
			var c ClientShare
			if err := rows.Scan(&c.Name, &c.Users); err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		return out, rows.Err()
	}
	if apps, err = read("app"); err != nil {
		return nil, nil, 0, err
	}
	if platforms, err = read("platform"); err != nil {
		return nil, nil, 0, err
	}
	err = s.rdb.QueryRow(`SELECT COUNT(DISTINCT user_id) FROM sub_clients
		WHERE last_seen >= ? AND last_seen < ?`, since, until).Scan(&users)
	return apps, platforms, users, err
}

// PurgeSubClients drops installs not seen since before (unix seconds).
func (s *Store) PurgeSubClients(before int64) (int64, error) {
	var total int64
	for {
		res, err := s.db.Exec(`DELETE FROM sub_clients WHERE rowid IN (
			SELECT rowid FROM sub_clients WHERE last_seen < ? LIMIT ?)`, before, purgeBatch)
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

// LaneMinutes is how many minutes one user was active on one lane on one day.
type LaneMinutes struct {
	UserID  int64
	Day     string
	Lane    string
	Minutes int64
}

// AddLaneMinutes books a batch of active minutes.
func (s *Store) AddLaneMinutes(list []LaneMinutes) error {
	if len(list) == 0 {
		return nil
	}
	return s.withTx(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`INSERT INTO user_lane_daily (user_id, day, lane, minutes)
			SELECT ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM users WHERE id = ?)
			ON CONFLICT (user_id, day, lane) DO UPDATE SET
				minutes = MIN(minutes + excluded.minutes, 1440)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, l := range list {
			if l.Lane == "" || l.Minutes <= 0 {
				continue
			}
			if _, err := stmt.Exec(l.UserID, l.Day, l.Lane, min(l.Minutes, 1440), l.UserID); err != nil {
				return err
			}
		}
		return nil
	})
}

// LaneUsage is one lane of one user over a period.
type LaneUsage struct {
	Lane    string
	Minutes int64
	Days    int64
	LastDay string
}

// UserLaneUsage sums a user's lanes between from and to (inclusive days).
func (s *Store) UserLaneUsage(userID int64, from, to string) ([]LaneUsage, error) {
	rows, err := s.rdb.Query(`SELECT lane, SUM(minutes), COUNT(*), MAX(day) FROM user_lane_daily
		WHERE user_id = ? AND day BETWEEN ? AND ? GROUP BY lane`, userID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LaneUsage
	for rows.Next() {
		var l LaneUsage
		if err := rows.Scan(&l.Lane, &l.Minutes, &l.Days, &l.LastDay); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
