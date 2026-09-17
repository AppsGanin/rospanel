package store

import (
	"database/sql"
	"fmt"
	"math/rand"
	"path/filepath"
	"reflect"
	"testing"
)

// applyTrafficOneByOne is the batch as it used to be written: each delta's statements
// in turn. The bulk write has to leave the database exactly as this does.
func applyTrafficOneByOne(tx *sql.Tx, deltas []TrafficDelta) error {
	for _, d := range deltas {
		qUp, qDown := d.quotaBytes()
		var err error
		if d.Baseline != nil {
			err = updateTrafficOn(tx, d.UserID, qUp, qDown, d.Baseline.Up, d.Baseline.Down)
		} else {
			err = addUsedTrafficOn(tx, d.UserID, qUp, qDown)
		}
		if err != nil {
			return err
		}
		if err := addDailyTrafficOn(tx, d.UserID, d.NodeID, d.Day, d.AddUp, d.AddDown); err != nil {
			return err
		}
		if d.SeenAt > 0 {
			if err := touchLastSeenOn(tx, d.UserID, d.SeenAt); err != nil {
				return err
			}
		}
	}
	return nil
}

// trafficSnapshot is every column a traffic batch can touch.
func trafficSnapshot(t *testing.T, st *Store) (users []string, daily []string) {
	t.Helper()
	rows, err := st.db.Query(`SELECT id, used_up, used_down, last_up, last_down, last_seen FROM users ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, a, b, c, d, e int64
		if err := rows.Scan(&id, &a, &b, &c, &d, &e); err != nil {
			t.Fatal(err)
		}
		users = append(users, fmt.Sprint(id, a, b, c, d, e))
	}
	rows.Close()
	rows, err = st.db.Query(`SELECT user_id, node_id, day, up, down FROM traffic_daily ORDER BY user_id, node_id, day`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var u, n, up, down int64
		var day string
		if err := rows.Scan(&u, &n, &day, &up, &down); err != nil {
			t.Fatal(err)
		}
		daily = append(daily, fmt.Sprint(u, n, day, up, down))
	}
	rows.Close()
	return users, daily
}

// Random batches — the same user several times, baselines with nothing to charge,
// zero deltas, sightings, a user that does not exist, two nodes and two days — land
// the same through the bulk write as they did one statement at a time.
func TestBulkTrafficWriteMatchesOneByOne(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(7))
	for round := 0; round < 40; round++ {
		var batch []TrafficDelta
		for i := rng.Intn(60); i >= 0; i-- {
			d := TrafficDelta{
				UserID: int64(1 + rng.Intn(9)), // 9 is never created
				NodeID: int64(rng.Intn(2)),
				Day:    []string{"2026-09-14", "2026-09-15"}[rng.Intn(2)],
			}
			if rng.Intn(4) > 0 {
				d.AddUp, d.AddDown = rng.Int63n(1<<40), rng.Int63n(1<<40)
			}
			if rng.Intn(3) == 0 {
				d.QuotaUp, d.QuotaDown = rng.Int63n(1<<40), rng.Int63n(1<<40)
			}
			if rng.Intn(3) == 0 {
				d.Baseline = &TrafficBaseline{Up: rng.Int63n(1 << 50), Down: rng.Int63n(1 << 50)}
			}
			if rng.Intn(2) == 0 {
				d.SeenAt = 1_700_000_000 + rng.Int63n(1_000_000)
			}
			batch = append(batch, d)
		}
		var want, got [2][]string
		for i, apply := range []func(*sql.Tx, []TrafficDelta) error{applyTrafficOneByOne, applyTrafficDeltasOn} {
			st, err := Open(filepath.Join(t.TempDir(), fmt.Sprintf("r%d-%d.db", round, i)))
			if err != nil {
				t.Fatal(err)
			}
			for u := 1; u <= 8; u++ {
				if _, err := st.CreateUser(fmt.Sprintf("u%d", u), fmt.Sprintf("uuid-%d", u), "pw", fmt.Sprintf("tok-%d", u), 0, 0, 0); err != nil {
					t.Fatal(err)
				}
			}
			// Some traffic already booked and every user seen before, so the upsert's add
			// is what is compared and a sighting of nothing cannot pass as untouched.
			if err := st.AddDailyTrafficNode(2, 1, "2026-09-15", 5, 6); err != nil {
				t.Fatal(err)
			}
			for u := int64(1); u <= 8; u++ {
				if err := st.TouchLastSeen(u, 1_600_000_000+u); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.withTx(func(tx *sql.Tx) error { return apply(tx, batch) }); err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
			u, d := trafficSnapshot(t, st)
			if i == 0 {
				want = [2][]string{u, d}
			} else {
				got = [2][]string{u, d}
			}
			st.Close()
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d (%d deltas): bulk write\n%v\nwant\n%v", round, len(batch), got, want)
		}
	}
}
