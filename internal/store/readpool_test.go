package store

import (
	"testing"
	"time"
)

// A read no longer waits for the writer: with the writer held inside an open write
// transaction, a read on the pool still answers — from the last committed state — and
// once the transaction commits, the next read sees it.
func TestReadsDoNotWaitForTheWriter(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	u, err := st.CreateUser("reader", "uuid-r", "pw", "tok-r", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	tx, err := st.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE users SET name = 'renamed' WHERE id = ?`, u.ID); err != nil {
		t.Fatal(err)
	}

	got := make(chan string, 1)
	go func() {
		v, err := st.GetUserBySubToken("tok-r")
		if err != nil {
			got <- "error: " + err.Error()
			return
		}
		got <- v.Name
	}()
	select {
	case name := <-got:
		if name != "reader" {
			t.Errorf("a read during an open write saw %q, want the committed %q", name, "reader")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a read waited for the writer's open transaction")
	}

	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if v, err := st.GetUserBySubToken("tok-r"); err != nil || v.Name != "renamed" {
		t.Fatalf("after the commit a read saw %+v (%v), want the new name", v, err)
	}
}

// The read pool refuses writes outright, so a write that ever strays onto it fails
// where it is made rather than racing the writer.
func TestReadPoolRefusesWrites(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	if _, err := st.rdb.Exec(`UPDATE settings SET sub_path = 'x' WHERE id = 1`); err == nil {
		t.Fatal("the read pool accepted a write")
	}
}

// A pass over a big table stays on the writer: running beside a steady stream of
// commits it would hold a snapshot open and starve the WAL checkpoint. So it waits
// for an open write transaction to finish, where a lookup does not.
func TestScansQueueOnTheWriter(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	if _, err := st.CreateUser("scanned", "uuid-s", "pw", "tok-s", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	for name, scan := range map[string]func() error{
		"WorkingSet":        func() error { _, _, err := st.WorkingSet(time.Now().Unix()); return err },
		"ListUserSummaries": func() error { _, err := st.ListUserSummaries(); return err },
	} {
		tx, err := st.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`UPDATE users SET name = name WHERE id = 1`); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- scan() }()
		select {
		case <-done:
			t.Errorf("%s answered beside an open write: it is not on the writer", name)
		case <-time.After(300 * time.Millisecond):
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s never answered after the commit", name)
		}
	}
}
