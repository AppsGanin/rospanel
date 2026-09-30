package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/store"
)

// signInitData builds initData the way Telegram signs it.
func signInitData(token string, fields map[string]string) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+fields[k])
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(token))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(lines, "\n")))
	v := url.Values{}
	for k, val := range fields {
		v.Set(k, val)
	}
	v.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return v.Encode()
}

func TestVerifyInitData(t *testing.T) {
	t.Parallel()
	const token = "123:ABC"
	now := time.Now()
	fields := map[string]string{
		"auth_date":   strconv.FormatInt(now.Unix(), 10),
		"user":        `{"id":777,"first_name":"Ann","language_code":"ru"}`,
		"start_param": "ref_XYZ",
		"signature":   "sig",
	}
	u, start, err := VerifyInitData(signInitData(token, fields), token, now)
	if err != nil || u.ID != 777 || start != "ref_XYZ" {
		t.Fatalf("verify = %+v %q %v", u, start, err)
	}
	if _, _, err := VerifyInitData(signInitData("999:OTHER", fields), token, now); err == nil {
		t.Fatal("initData signed by another bot passed")
	}
	tampered := strings.Replace(signInitData(token, fields), "777", "778", 1)
	if _, _, err := VerifyInitData(tampered, token, now); err == nil {
		t.Fatal("tampered initData passed")
	}
	fields["auth_date"] = strconv.FormatInt(now.Add(-48*time.Hour).Unix(), 10)
	if _, _, err := VerifyInitData(signInitData(token, fields), token, now); err == nil {
		t.Fatal("stale initData passed")
	}
}

func TestMiniAppEnter(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "mini.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := &Manager{store: st}
	ctx := context.Background()
	// Closed sign-up: no account, a reason.
	res, err := m.MiniAppEnter(ctx, MiniAppUser{ID: 501, FirstName: "Bob"}, "")
	if err != nil || res.UserID != 0 || res.Reason != "sub.miniRegClosed" {
		t.Fatalf("closed = %+v %v", res, err)
	}
	if err := st.ExecForTest(`UPDATE settings SET tg_user_reg_enabled = 1, tg_user_reg_mode = 'open', ref_mode = 'percent', billing_enabled = 1`); err != nil {
		t.Fatal(err)
	}
	inviter, _ := st.CreateUser("inviter", "uuid-i", "pw", "tok-i", 0, 0, 0)
	code, err := m.RefCode(inviter.ID)
	if err != nil {
		t.Fatal(err)
	}
	res, err = m.MiniAppEnter(ctx, MiniAppUser{ID: 502, FirstName: "Cat"}, "ref_"+code)
	if err != nil || res.UserID == 0 {
		t.Fatalf("open = %+v %v", res, err)
	}
	u, _ := st.GetUser(res.UserID)
	if u.TgChatID != 502 {
		t.Fatalf("chat not linked: %+v", u)
	}
	if w, _ := st.GetWalletLite(u.ID); w.ReferrerID != inviter.ID {
		t.Fatalf("referrer = %d, want %d", w.ReferrerID, inviter.ID)
	}
	again, _ := m.MiniAppEnter(ctx, MiniAppUser{ID: 502, FirstName: "Cat"}, "")
	if again.UserID != u.ID {
		t.Fatalf("second entry = %+v", again)
	}
}

// Opening the app many times at once makes one account.
func TestMiniAppEnterConcurrent(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "mini2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.ExecForTest(`UPDATE settings SET tg_user_reg_enabled = 1, tg_user_reg_mode = 'open'`); err != nil {
		t.Fatal(err)
	}
	m := &Manager{store: st}
	var wg sync.WaitGroup
	ids := make([]int64, 16)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, _ := m.MiniAppEnter(context.Background(), MiniAppUser{ID: 900, FirstName: "Dan"}, "")
			ids[i] = res.UserID
		}(i)
	}
	wg.Wait()
	users, _ := st.ListUsers()
	if len(users) != 1 {
		t.Fatalf("%d accounts from one Telegram user", len(users))
	}
	for _, id := range ids {
		if id != users[0].ID {
			t.Fatalf("an entry got account %d, want %d", id, users[0].ID)
		}
	}
}

// One chat retrying does not use up everyone's sign-ups.
func TestMiniAppSignupsPerChat(t *testing.T) {
	t.Parallel()
	var l signupLimiter
	now := time.Now()
	if !l.allow(now, "tg:1") || l.allow(now, "tg:1") {
		t.Fatal("a chat got two slots in a minute")
	}
	if !l.allow(now, "tg:2") {
		t.Fatal("another chat was refused")
	}
	// Any one busy key refuses the attempt, and a refused attempt holds no slot.
	if l.allow(now, "ext:a", "tg:2") {
		t.Fatal("a busy key did not refuse")
	}
	if !l.allow(now, "ext:a") {
		t.Fatal("a refused attempt kept its other key busy")
	}
	if !l.allow(now.Add(61*time.Second), "tg:1") {
		t.Fatal("a key stayed busy past its minute")
	}
}
