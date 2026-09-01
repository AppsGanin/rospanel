package store

import (
	"reflect"
	"testing"
)

// The note and the tags are the operator's own annotations: they must survive a
// round trip through the row exactly (the note) or in canonical form (the tags),
// start empty on a fresh user, and clear when set to nothing.
func TestUserNoteAndTagsRoundTrip(t *testing.T) {
	st := newStore(t)
	u, err := st.CreateUser("annotated", "uuid-1", "pw", "tok-1", 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if u.Note != "" || len(u.Tags) != 0 || u.Tags == nil {
		t.Fatalf("fresh user: note=%q tags=%#v", u.Note, u.Tags)
	}

	if err := st.SetUserNote(u.ID, "came via the Telegram ad\nsecond line"); err != nil {
		t.Fatalf("set note: %v", err)
	}
	if err := st.SetUserTags(u.ID, []string{"VIP", " reseller-a "}); err != nil {
		t.Fatalf("set tags: %v", err)
	}
	got, err := st.GetUser(u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Note != "came via the Telegram ad\nsecond line" {
		t.Errorf("note: %q", got.Note)
	}
	if want := []string{"reseller-a", "vip"}; !reflect.DeepEqual(got.Tags, want) {
		t.Errorf("tags: %v, want %v", got.Tags, want)
	}

	// Every reader of the users table goes through the same scan, so the list must
	// carry the same values as the single-row read.
	list, err := st.ListUsers()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v (%d)", err, len(list))
	}
	if list[0].Note != got.Note || !reflect.DeepEqual(list[0].Tags, got.Tags) {
		t.Errorf("list disagrees with get: %q %v", list[0].Note, list[0].Tags)
	}

	// The tag index counts users, not occurrences.
	v, err := st.CreateUser("second", "uuid-2", "pw", "tok-2", 0, 0, 0)
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if err := st.SetUserTags(v.ID, []string{"vip"}); err != nil {
		t.Fatalf("set tags: %v", err)
	}
	counts, err := st.AllUserTags()
	if err != nil {
		t.Fatalf("all tags: %v", err)
	}
	if want := map[string]int{"vip": 2, "reseller-a": 1}; !reflect.DeepEqual(counts, want) {
		t.Errorf("tag counts: %v, want %v", counts, want)
	}

	// Clearing.
	if err := st.SetUserNote(u.ID, ""); err != nil {
		t.Fatalf("clear note: %v", err)
	}
	if err := st.SetUserTags(u.ID, nil); err != nil {
		t.Fatalf("clear tags: %v", err)
	}
	got, err = st.GetUser(u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Note != "" || len(got.Tags) != 0 {
		t.Errorf("after clearing: note=%q tags=%v", got.Note, got.Tags)
	}

	// What the model refuses, the store refuses too — it never stores a variant it
	// could not read back.
	if err := st.SetUserTags(u.ID, []string{"a,b"}); err == nil {
		t.Error("a tag with a comma was stored")
	}
}
