package store

import "testing"

func TestProbeHitsUpsertAndCap(t *testing.T) {
	st := newStore(t)

	// A repeat scan from one IP folds into a single row: hits climb, paths keeps the
	// largest burst, last_seen advances.
	if err := st.RecordProbe("1.2.3.4", 10, 1000); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := st.RecordProbe("1.2.3.4", 25, 2000); err != nil {
		t.Fatalf("record 2: %v", err)
	}
	got, err := st.ListProbes(50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %d, want 1 (deduped by IP)", len(got))
	}
	if got[0].Hits != 2 || got[0].Paths != 25 || got[0].LastSeen != 2000 || got[0].FirstSeen != 1000 {
		t.Fatalf("row = %+v, want hits=2 paths=25 last=2000 first=1000", got[0])
	}

	// Newest-first ordering.
	if err := st.RecordProbe("5.6.7.8", 12, 3000); err != nil {
		t.Fatalf("record 3: %v", err)
	}
	got, _ = st.ListProbes(50)
	if len(got) != 2 || got[0].IP != "5.6.7.8" {
		t.Fatalf("order = %+v, want 5.6.7.8 first (most recent)", got)
	}

	// Retention drops rows last seen before the cutoff, keeps the rest.
	n, err := st.PurgeProbes(2500)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged %d, want 1 (only 1.2.3.4 at last_seen=2000)", n)
	}
	got, _ = st.ListProbes(50)
	if len(got) != 1 || got[0].IP != "5.6.7.8" {
		t.Fatalf("survivors = %+v, want only 5.6.7.8", got)
	}

	// The row cap holds: many IPs collapse to the newest maxProbeHits.
	for i := range maxProbeHits + 50 {
		if err := st.RecordProbe(uniqueIP(i), 10, int64(10000+i)); err != nil {
			t.Fatalf("bulk record: %v", err)
		}
	}
	got, _ = st.ListProbes(maxProbeHits + 100)
	if len(got) > maxProbeHits {
		t.Fatalf("rows = %d, want <= %d after the cap", len(got), maxProbeHits)
	}
}

func uniqueIP(i int) string {
	return "192.168." + itoa(i/256) + "." + itoa(i%256)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
