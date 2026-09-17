package store

import (
	"strings"
	"testing"
)

// Every lookup by a partially indexed column reaches the index. The planner's answer
// is the test: a "SCAN users" here is a full read of the table on every subscription
// fetch or bot message, which no functional test can see.
func TestUserLookupsUseTheirIndexes(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	for _, tc := range []struct {
		name, sql, index string
	}{
		{"subscription token", userBySubTokenSQL, "idx_users_sub_token"},
		{"telegram chat", userByTelegramChatSQL, "idx_users_tg_chat"},
		{"telegram chat detach", detachTelegramChatSQL, "idx_users_tg_chat"},
		{"previous chat drop", dropPrevTelegramChatSQL, "idx_users_tg_prev_chat"},
		{"detached account by previous chat", detachedUserByPrevChatSQL, "idx_users_tg_prev_chat"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := st.db.Query(`EXPLAIN QUERY PLAN `+tc.sql, int64(42))
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var plan []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				plan = append(plan, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			got := strings.Join(plan, " | ")
			if !strings.Contains(got, "USING INDEX "+tc.index) && !strings.Contains(got, "USING COVERING INDEX "+tc.index) {
				t.Fatalf("plan does not use %s: %s", tc.index, got)
			}
			if strings.Contains(got, "TEMP B-TREE") {
				t.Fatalf("plan sorts in a temporary b-tree: %s", got)
			}
		})
	}
}

// Repeating the index condition changes no answer: a lookup by the excluded value finds
// nothing (as it did — callers refuse it before the query), and the detach statements
// leave rows without a chat alone.
func TestUserLookupsByChatAndToken(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	a, err := st.CreateUser("a", "uuid-a", "pw", "tok-a", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateUser("b", "uuid-b", "pw", "tok-b", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if u, err := st.GetUserBySubToken("tok-b"); err != nil || u.ID != b.ID {
		t.Fatalf("token lookup: %v %v", u, err)
	}
	if _, err := st.GetUserBySubToken("tok-none"); err == nil {
		t.Fatal("an unknown token found a user")
	}

	if err := st.SetUserTelegramChat(a.ID, 777); err != nil {
		t.Fatal(err)
	}
	if u, err := st.GetUserByTelegramChatID(777); err != nil || u.ID != a.ID {
		t.Fatalf("chat lookup: %v %v", u, err)
	}
	// Moving the chat to b detaches it from a.
	if err := st.SetUserTelegramChat(b.ID, 777); err != nil {
		t.Fatal(err)
	}
	if u, err := st.GetUserByTelegramChatID(777); err != nil || u.ID != b.ID {
		t.Fatalf("chat after the move: %v %v", u, err)
	}
	if u, _ := st.GetUser(a.ID); u.TgChatID != 0 {
		t.Fatalf("a kept the chat: %d", u.TgChatID)
	}

	// Unlinking remembers the chat; the newest detached account comes back first.
	if err := st.ClearUserTelegramChat(b.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserTelegramChat(a.ID, 777); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetDetachedUserByPrevChat(777); err == nil {
		t.Fatal("a chat that is linked again still restores the account it left")
	}
	if err := st.ClearUserTelegramChat(a.ID); err != nil {
		t.Fatal(err)
	}
	if u, err := st.GetDetachedUserByPrevChat(777); err != nil || u.ID != a.ID {
		t.Fatalf("detached account: %v %v", u, err)
	}
	if _, err := st.db.Exec(`UPDATE users SET tg_prev_chat_id = 777 WHERE id = ?`, b.ID); err != nil {
		t.Fatal(err)
	}
	if u, err := st.GetDetachedUserByPrevChat(777); err != nil || u.ID != b.ID {
		t.Fatalf("the newest detached account should come first: %v %v", u, err)
	}
}

// A count of a few users' devices reads those users' rows by the primary key, not the
// window of everyone online.
func TestDeviceCountOfFewUsesTheirRows(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	rows, err := st.db.Query(`EXPLAIN QUERY PLAN `+activeDeviceCountsOfSQL, "[1,2,3]", int64(0))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	got := strings.Join(plan, " | ")
	if !strings.Contains(got, "sqlite_autoindex_connections_1 (user_id=?") {
		t.Fatalf("plan does not look users up by key: %s", got)
	}
}
