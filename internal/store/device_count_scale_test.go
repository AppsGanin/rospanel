package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// TestDeviceCountScalesWithRowsNotUsers pins the SHAPE of the device-limit query, not
// its speed. The handover grace needs each user's newest sighting; asking for it as a
// correlated MAX puts it inside the WHERE of the (already correlated) address count, so
// SQLite re-evaluates it once per scanned connection row and each evaluation walks that
// user's whole PK range — cost then grows with rows × rows-per-user instead of with rows.
// That shipped once and cost 780ms per reconcile at 50k rows, on the single connection
// every writer queues behind.
//
// Both halves below hold the row count fixed and only change how those rows are spread,
// so the grouped form costs about the same for each while the correlated form costs an
// order of magnitude more for the fewer-users half. A ratio, not a deadline, so the
// assertion means the same thing on a slow CI box as on a laptop.
func TestDeviceCountScalesWithRowsNotUsers(t *testing.T) {
	const rows = 50_000
	measure := func(users int) time.Duration {
		st, err := Open(filepath.Join(t.TempDir(), "scale.db"))
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer st.Close()
		now := time.Now().Unix()
		for i := 0; i < users; i++ {
			if _, err := st.CreateUser(fmt.Sprintf("u%d", i), fmt.Sprintf("uu%d", i),
				"pw", fmt.Sprintf("tok%d", i), 0, 0, 3); err != nil {
				t.Fatalf("user: %v", err)
			}
		}
		per := rows / users
		hits := make([]ConnectionHit, 0, rows)
		for i := 1; i <= users; i++ {
			for j := 0; j < per; j++ {
				hits = append(hits, ConnectionHit{
					UserID: int64(i), IP: fmt.Sprintf("10.%d.%d.%d", i%250, j/250, j%250),
					SeenAt: now - int64(j%30), Hits: 1,
				})
			}
		}
		if err := st.AddConnections(hits); err != nil {
			t.Fatalf("connections: %v", err)
		}
		best := time.Hour
		for round := 0; round < 3; round++ {
			start := time.Now()
			if _, err := st.WorkingUsers(now); err != nil {
				t.Fatalf("WorkingUsers: %v", err)
			}
			if d := time.Since(start); d < best {
				best = d
			}
		}
		return best
	}
	spread := measure(500) // 100 sightings each
	packed := measure(50)  // 1000 sightings each — same rows, ten times the range walk
	t.Logf("500 users: %v · 50 users: %v", spread.Round(time.Millisecond), packed.Round(time.Millisecond))
	if packed > 4*spread {
		t.Fatalf("device-limit query scales with rows-per-user, not rows: "+
			"%v for 50 users vs %v for 500 users over the same %d rows — "+
			"the per-user lookup is being re-evaluated per row", packed, spread, rows)
	}
}
