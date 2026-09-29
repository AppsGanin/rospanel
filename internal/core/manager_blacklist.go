package core

import (
	"bufio"
	"bytes"
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/netguard"
)

// The shared blacklist: Telegram accounts other VPN services banned for reselling,
// scanning, sharing or fraud. With it on, a listed account cannot register in the
// user bot; an account already registered is only marked in its card — the
// operator decides what to do with a customer who may be paying.

// DefaultBlacklistURL is the community list the setting starts from.
const DefaultBlacklistURL = "https://raw.githubusercontent.com/BEDOLAGA-DEV/VPN-BLACKLIST/main/blacklist.txt"

const (
	// blacklistEvery is how often the list is fetched again.
	blacklistEvery = 6 * time.Hour
	// blacklistMaxBytes bounds the download: the list is ~100 KB today.
	blacklistMaxBytes = 8 << 20
	// blacklistReasonMax trims the comment kept per account.
	blacklistReasonMax = 200
)

func blacklistURL(set *model.Settings) string {
	if u := strings.TrimSpace(set.BlacklistURL); u != "" {
		return u
	}
	return DefaultBlacklistURL
}

// parseBlacklist reads "<telegram id> # reason" lines. Anything that does not start
// with a number is skipped, so a comment line or a header does not fail the list.
func parseBlacklist(body []byte) (map[int64]string, error) {
	out := map[int64]string{}
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		idPart, reason, _ := strings.Cut(line, "#")
		id, err := strconv.ParseInt(strings.TrimSpace(idPart), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		reason = strings.TrimSpace(reason)
		if r := []rune(reason); len(r) > blacklistReasonMax {
			reason = string(r[:blacklistReasonMax]) + "…"
		}
		out[id] = reason
	}
	return out, sc.Err()
}

// RefreshBlacklist fetches the list and replaces the stored copy. A failed fetch,
// or a body with no accounts in it, keeps the copy already stored and records why.
func (m *Manager) RefreshBlacklist(ctx context.Context) error {
	set, err := m.Settings()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	body, err := netguard.Get(ctx, blacklistURL(set), blacklistMaxBytes)
	if err != nil {
		_ = m.store.SetBlacklistError(err.Error())
		return err
	}
	// A body at the cap was cut: its last line may be half an id, and the rest of the
	// list is missing — keep the copy already stored.
	if len(body) >= blacklistMaxBytes {
		_ = m.store.SetBlacklistError("the list is larger than 8 MB")
		return invalidCode("err.blacklistTooBig", "список больше 8 МБ")
	}
	entries, err := parseBlacklist(body)
	if err != nil {
		_ = m.store.SetBlacklistError(err.Error())
		return err
	}
	if len(entries) == 0 {
		err := invalidCode("err.blacklistEmpty", "в списке нет ни одного Telegram ID")
		_ = m.store.SetBlacklistError("no Telegram ids in the list")
		return err
	}
	if err := m.store.ReplaceBlacklist(entries, time.Now().Unix()); err != nil {
		return err
	}
	logInfo("blacklist: refreshed", "accounts", len(entries))
	return nil
}

// RunBlacklistLoop keeps the list fresh while it is enabled.
func (m *Manager) RunBlacklistLoop(ctx context.Context) {
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if set, err := m.Settings(); err == nil && set.BlacklistEnabled &&
			time.Since(time.Unix(set.BlacklistSyncedAt, 0)) >= blacklistEvery-time.Minute {
			if err := m.RefreshBlacklist(ctx); err != nil {
				logErr("blacklist: refresh failed", "err", err)
			}
		}
		timer.Reset(10 * time.Minute)
	}
}

// Blacklisted reports whether a Telegram account is on the stored list, and why —
// whether or not enforcement is on (the user card shows it either way).
func (m *Manager) Blacklisted(tgID int64) (string, bool) {
	return m.store.BlacklistReason(tgID)
}

// RegistrationBlacklisted is Blacklisted when enforcement is on, false otherwise.
func (m *Manager) RegistrationBlacklisted(tgID int64) bool {
	set, err := m.Settings()
	if err != nil || !set.BlacklistEnabled {
		return false
	}
	_, listed := m.store.BlacklistReason(tgID)
	return listed
}

// BlacklistStatus is the settings page's view of the list.
type BlacklistStatus struct {
	Enabled    bool   `json:"enabled"`
	URL        string `json:"url"`
	DefaultURL string `json:"default_url"`
	Count      int    `json:"count"`
	SyncedAt   int64  `json:"synced_at"`
	Error      string `json:"error,omitempty"`
}

// BlacklistInfo reads the list's settings and state.
func (m *Manager) BlacklistInfo() (BlacklistStatus, error) {
	set, err := m.Settings()
	if err != nil {
		return BlacklistStatus{}, err
	}
	return BlacklistStatus{
		Enabled: set.BlacklistEnabled, URL: set.BlacklistURL, DefaultURL: DefaultBlacklistURL,
		Count: m.store.BlacklistCount(), SyncedAt: set.BlacklistSyncedAt, Error: set.BlacklistError,
	}, nil
}

// SaveBlacklist stores the settings. Turning it on with no list stored yet, or
// pointing it elsewhere, fetches the list at once, so the page shows the count
// straight away; a stored copy past its age is left to the loop. A failed fetch is
// reported but the setting stays saved — the loop retries.
func (m *Manager) SaveBlacklist(ctx context.Context, enabled bool, url string) error {
	url = strings.TrimSpace(url)
	if url == DefaultBlacklistURL {
		url = ""
	}
	if url != "" {
		if err := netguard.ValidateFetchURL(url); err != nil {
			return invalidCode("err.blacklistURL", "адрес списка: {{err}}", map[string]any{"err": err.Error()})
		}
	}
	prev, err := m.Settings()
	if err != nil {
		return err
	}
	if err := m.store.SetBlacklistSettings(enabled, url); err != nil {
		return err
	}
	if enabled && (prev.BlacklistURL != url || m.store.BlacklistCount() == 0) {
		return m.RefreshBlacklist(ctx)
	}
	return nil
}
