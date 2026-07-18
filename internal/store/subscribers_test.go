package store

import (
	"path/filepath"
	"testing"
)

func subStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "subs.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestUpsertSubscriber(t *testing.T) {
	st := subStore(t)

	if sub, err := st.SubscriberByChat(555); err != nil || sub != nil {
		t.Fatalf("unknown chat = %+v, %v; want nil, nil", sub, err)
	}

	if err := st.UpsertSubscriber(555, 0, "vanya", "Ваня", "ru", 1700000000); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	sub, err := st.SubscriberByChat(555)
	if err != nil || sub == nil {
		t.Fatalf("SubscriberByChat: %+v, %v", sub, err)
	}
	// An unregistered chat is a legitimate audience member, so user_id stays NULL
	// rather than the row being withheld.
	if sub.UserID != 0 || !sub.Active || sub.OptOut {
		t.Fatalf("unexpected fresh subscriber: %+v", sub)
	}
	if sub.Username != "vanya" || sub.Lang != "ru" {
		t.Fatalf("profile not stored: %+v", sub)
	}

	// Registering later links the account without creating a second row.
	if err := st.UpsertSubscriber(555, 42, "vanya2", "Иван", "en", 1700000100); err != nil {
		t.Fatalf("upsert linked: %v", err)
	}
	if sub, err = st.SubscriberByChat(555); err != nil || sub.UserID != 42 {
		t.Fatalf("user id = %+v, %v; want 42", sub, err)
	}
	if sub.Username != "vanya2" || sub.FirstName != "Иван" || sub.Lang != "en" {
		t.Fatalf("profile not refreshed: %+v", sub)
	}
	if total, reachable, err := st.CountSubscribers(); err != nil || total != 1 || reachable != 1 {
		t.Fatalf("counts = %d/%d, %v; want 1/1", reachable, total, err)
	}
}

// TestUpsertKeepsOptOut is the rule that keeps people from blocking the bot instead
// of unsubscribing: writing to the bot re-activates a blocked chat, but it must
// never quietly re-subscribe someone who opted out.
func TestUpsertKeepsOptOut(t *testing.T) {
	st := subStore(t)
	if err := st.UpsertSubscriber(555, 42, "vanya", "Ваня", "ru", 1700000000); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.SetSubscriberOptOut(555, true, 1700000100); err != nil {
		t.Fatalf("opt out: %v", err)
	}
	if err := st.SetSubscriberBlocked(555, 1700000200); err != nil {
		t.Fatalf("blocked: %v", err)
	}

	// They came back and wrote again.
	if err := st.UpsertSubscriber(555, 42, "vanya", "Ваня", "ru", 1700000300); err != nil {
		t.Fatalf("upsert after contact: %v", err)
	}
	sub, err := st.SubscriberByChat(555)
	if err != nil {
		t.Fatalf("SubscriberByChat: %v", err)
	}
	if !sub.Active || sub.BlockedAt != 0 {
		t.Errorf("contact did not clear the block: %+v", sub)
	}
	if !sub.OptOut {
		t.Error("opt-out was silently reverted by contact")
	}
	if total, reachable, err := st.CountSubscribers(); err != nil || total != 1 || reachable != 0 {
		t.Fatalf("counts = %d reachable of %d, %v; want 0 of 1", reachable, total, err)
	}
}

// TestOptOutWithoutRow: /stop must stick even for a chat nothing has recorded yet,
// or the unsubscribe is lost and the next broadcast contradicts it.
func TestOptOutWithoutRow(t *testing.T) {
	st := subStore(t)
	if err := st.SetSubscriberOptOut(777, true, 1700000000); err != nil {
		t.Fatalf("opt out: %v", err)
	}
	sub, err := st.SubscriberByChat(777)
	if err != nil || sub == nil {
		t.Fatalf("SubscriberByChat: %+v, %v", sub, err)
	}
	if !sub.OptOut {
		t.Fatal("opt-out not recorded for an unknown chat")
	}
}

func TestSetSubscriberBlocked(t *testing.T) {
	st := subStore(t)
	if err := st.UpsertSubscriber(555, 0, "", "", "", 1700000000); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.SetSubscriberBlocked(555, 1700000900); err != nil {
		t.Fatalf("blocked: %v", err)
	}
	sub, err := st.SubscriberByChat(555)
	if err != nil {
		t.Fatalf("SubscriberByChat: %v", err)
	}
	// The row survives: a block can be undone by the person, and the next message
	// they send re-activates them.
	if sub.Active || sub.BlockedAt != 1700000900 {
		t.Fatalf("block not recorded: %+v", sub)
	}
	if _, reachable, err := st.CountSubscribers(); err != nil || reachable != 0 {
		t.Fatalf("reachable = %d, %v; want 0", reachable, err)
	}
}

// TestSubscriberBackfill covers the 0032 backfill on a database that already has
// linked users. Without it the first broadcast after an upgrade reaches only the
// people who happened to write to the bot since — which reads as a broken feature,
// not an empty table.
func TestSubscriberBackfill(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backfill.db")

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Simulate a pre-0032 install: users linked to chats, no subscriber rows.
	if _, err := st.db.Exec(`DELETE FROM tg_subscribers`); err != nil {
		t.Fatalf("clear: %v", err)
	}
	linked, err := st.CreateUser("linked", "uuid-1", "pw", "tok-1", 0, 0, 0)
	if err != nil {
		t.Fatalf("create linked: %v", err)
	}
	if err := st.SetUserTelegramChat(linked.ID, 900001); err != nil {
		t.Fatalf("link: %v", err)
	}
	if _, err := st.CreateUser("unlinked", "uuid-2", "pw", "tok-2", 0, 0, 0); err != nil {
		t.Fatalf("create unlinked: %v", err)
	}
	if _, err := st.db.Exec(`DELETE FROM schema_migrations WHERE version LIKE '0032%'`); err != nil {
		t.Fatalf("rewind migration: %v", err)
	}
	if _, err := st.db.Exec(`DROP TABLE tg_subscribers`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	st.Close()

	// Re-opening replays 0032 against the populated database.
	st, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st.Close()

	sub, err := st.SubscriberByChat(900001)
	if err != nil || sub == nil {
		t.Fatalf("linked user not backfilled: %+v, %v", sub, err)
	}
	if sub.UserID != linked.ID || !sub.Active || sub.OptOut {
		t.Fatalf("backfilled row wrong: %+v", sub)
	}
	// A user with no chat has nowhere to receive anything, so they must not appear.
	if total, _, err := st.CountSubscribers(); err != nil || total != 1 {
		t.Fatalf("total = %d, %v; want only the linked user", total, err)
	}
}
