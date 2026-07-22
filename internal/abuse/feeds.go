package abuse

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Feed is one downloadable category list.
type Feed struct {
	Category Category
	// URLs are mirrors, tried in order. GitHub raw is sometimes DPI-degraded in RU,
	// so a jsDelivr mirror follows — the same reason package geo carries two.
	URLs []string
}

// Feeds are the lists the panel ships with — IP reputation only, on purpose.
//
// Domain feeds were shipped first and then dropped: a domain can only be matched
// when the destination reaches the panel AS a domain, and on real traffic it usually
// does not. Clients resolve DNS off the tunnel and encrypt the TLS SNI (ECH), so the
// access log records a bare IP; measured on a live server it was 23 IPs to every 4
// domains, and those 4 were connectivity checks. Three domain lists cost ~12 MB of
// memory and a daily download to match almost nothing.
//
// What survives is the one list that sees that traffic. FireHOL level 1 is the
// high-confidence, low-false-positive aggregate (spamhaus DROP, dshield, feodo/C2
// trackers, …) — the tier FireHOL itself marks safe to block outright, curated to
// exclude CDNs and shared hosting, which is what an advisory abuse view needs.
//
// The operator's own list is IP/CIDR too, for the same reason.
var Feeds = []Feed{
	{Category: CatBadIP, URLs: []string{
		"https://raw.githubusercontent.com/firehol/blocklist-ipsets/master/firehol_level1.netset",
		"https://cdn.jsdelivr.net/gh/firehol/blocklist-ipsets@master/firehol_level1.netset",
	}},
}

const (
	// maxFeedBytes caps a download. The largest feed is ~9.4 MB; this leaves room for
	// growth while refusing to stream an unbounded body into memory.
	maxFeedBytes = 64 << 20
	// refreshInterval is how often feeds are re-fetched. These lists move on a scale
	// of days, so the 5-minute reload some tools default to buys nothing and costs
	// the mirror bandwidth on every panel.
	refreshInterval = 24 * time.Hour
	// staleAfter is how old a cached copy may be before boot re-downloads it rather
	// than waiting for the first tick.
	staleAfter = 20 * time.Hour
)

// Store downloads, caches and loads the feeds into a Matcher.
type Store struct {
	dir     string
	matcher *Matcher
	client  *http.Client

	// cfg guards the operator config: which categories are active and the custom list.
	// enabled nil means every category is on (the default before Configure runs).
	cfgMu   sync.Mutex
	enabled map[Category]bool
	custom  string

	// refreshing admits one refresh at a time. The operator's "refresh now" button
	// spawns a goroutine per click and the route has no rate limit, so without this
	// two passes fight: sweepTempFiles removes every .dl-* it finds, including the
	// other pass's in-flight download, which then fails its rename and logs a
	// download error for a transfer that was actually fine.
	refreshing atomic.Bool
}

// NewStore returns a store caching feeds under dir.
func NewStore(dir string) *Store {
	return &Store{
		dir:     dir,
		matcher: New(),
		client:  &http.Client{Timeout: 5 * time.Minute},
	}
}

// Configure applies operator settings — which categories are active and the custom
// list — and reloads the matcher from cache to match (no network). A disabled
// category is cleared from the matcher. Called at boot and on every settings save.
func (s *Store) Configure(enabled map[Category]bool, custom string) {
	s.cfgMu.Lock()
	s.enabled = enabled
	s.custom = custom
	s.cfgMu.Unlock()

	for _, cat := range feedCats() {
		if s.catEnabled(cat) {
			if err := s.loadFile(cat); err != nil && !errors.Is(err, os.ErrNotExist) {
				slog.Warn("abuse: reload on configure failed", "category", cat, "err", err)
			}
		} else {
			s.matcher.Clear(cat)
		}
	}
	s.applyCustom()
}

// catEnabled reports whether a category is active. nil config ⇒ all on.
func (s *Store) catEnabled(cat Category) bool {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	return s.enabled == nil || s.enabled[cat]
}

// applyCustom loads the operator's own IP list into the custom category, or clears
// it when custom is disabled.
func (s *Store) applyCustom() {
	if !s.catEnabled(CatCustom) {
		s.matcher.Clear(CatCustom)
		return
	}
	s.cfgMu.Lock()
	content := s.custom
	s.cfgMu.Unlock()
	s.matcher.SetIP(CatCustom, ParseCustom(content))
}

// Matcher is the live matcher. Never nil; empty until lists load.
func (s *Store) Matcher() *Matcher { return s.matcher }

// FileInfo is one category's state, for the settings UI.
type FileInfo struct {
	Category string `json:"category"`
	Title    string `json:"title"`
	Enabled  bool   `json:"enabled"`
	Present  bool   `json:"present"`           // a cached feed on disk, or a non-empty custom list
	Entries  int    `json:"entries"`           // entries currently loaded in the matcher
	Size     int64  `json:"size,omitempty"`    // cached feed size (feeds only)
	Updated  int64  `json:"updated,omitempty"` // cached feed mtime (feeds only)
}

// Status reports each category's state: enabled, loaded entry count, and (for
// downloaded feeds) the cached copy's size and age.
func (s *Store) Status() []FileInfo {
	counts := s.matcher.Counts()
	out := make([]FileInfo, 0, len(Feeds)+1)
	for _, cat := range feedCats() {
		fi := FileInfo{
			Category: string(cat), Title: cat.Title(),
			Enabled: s.catEnabled(cat), Entries: counts[cat],
		}
		if st, err := os.Stat(s.path(cat)); err == nil {
			fi.Present, fi.Size, fi.Updated = true, st.Size(), st.ModTime().Unix()
		}
		out = append(out, fi)
	}
	// Custom has no cached file — it lives in settings.
	s.cfgMu.Lock()
	customSet := strings.TrimSpace(s.custom) != ""
	s.cfgMu.Unlock()
	out = append(out, FileInfo{
		Category: string(CatCustom), Title: CatCustom.Title(),
		Enabled: s.catEnabled(CatCustom), Present: customSet, Entries: counts[CatCustom],
	})
	return out
}

func feedCats() []Category {
	out := make([]Category, 0, len(Feeds))
	for _, f := range Feeds {
		out = append(out, f.Category)
	}
	return out
}

func (s *Store) path(cat Category) string {
	return filepath.Join(s.dir, string(cat)+".txt")
}

// LoadCached loads the enabled feeds' cached copies plus the custom list, without
// network. Called at boot so a panel that has run before starts matching
// immediately rather than being blind until the first download finishes.
func (s *Store) LoadCached() {
	for _, cat := range feedCats() {
		if !s.catEnabled(cat) {
			continue
		}
		if err := s.loadFile(cat); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("abuse: cannot load cached list", "category", cat, "err", err)
		}
	}
	s.applyCustom()
}

func (s *Store) loadFile(cat Category) error {
	f, err := os.Open(s.path(cat))
	if err != nil {
		return err
	}
	defer f.Close()
	entries, err := ParseIPList(f)
	if err != nil {
		return err
	}
	s.matcher.SetIP(cat, entries)
	return nil
}

// Refresh downloads any feed whose cache is missing or stale, then reloads it.
// The operator's own list is never downloaded, only re-read.
func (s *Store) Refresh(ctx context.Context, force bool) {
	// One pass at a time — see the refreshing field. Dropping the overlapping call is
	// right rather than queueing it: it would fetch the same lists the running pass is
	// already fetching.
	if !s.refreshing.CompareAndSwap(false, true) {
		slog.Info("abuse: refresh already in progress, skipping")
		return
	}
	defer s.refreshing.Store(false)

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		slog.Error("abuse: cannot create list dir", "dir", s.dir, "err", err)
		return
	}
	s.sweepTempFiles() // reclaim any .dl-* left by a crash between CreateTemp and Rename
	for _, feed := range Feeds {
		if ctx.Err() != nil {
			return
		}
		if !s.catEnabled(feed.Category) {
			s.matcher.Clear(feed.Category) // disabled: keep it out of the matcher, don't fetch
			continue
		}
		if !force && !s.stale(feed.Category) {
			continue
		}
		if err := s.download(ctx, feed); err != nil {
			// Keep whatever is cached: a stale list matches far more than no list, and
			// the mirror being down is not the operator's problem to act on.
			slog.Warn("abuse: feed download failed, keeping cached copy",
				"category", feed.Category, "err", err)
			continue
		}
		if err := s.loadFile(feed.Category); err != nil {
			slog.Error("abuse: cannot load downloaded feed", "category", feed.Category, "err", err)
		}
	}
	// Custom is settings-driven; re-apply in case it changed.
	s.applyCustom()
}

// sweepTempFiles removes partial downloads orphaned by a crash between CreateTemp
// and Rename. Best-effort: a failure here must not stop a refresh.
func (s *Store) sweepTempFiles() {
	matches, err := filepath.Glob(filepath.Join(s.dir, ".dl-*"))
	if err != nil {
		return
	}
	for _, p := range matches {
		_ = os.Remove(p)
	}
}

func (s *Store) stale(cat Category) bool {
	st, err := os.Stat(s.path(cat))
	if err != nil {
		return true
	}
	return time.Since(st.ModTime()) > staleAfter
}

// download fetches a feed to a temp file and renames it into place, so a truncated
// transfer never replaces a good cached list.
func (s *Store) download(ctx context.Context, feed Feed) error {
	var lastErr error
	for _, url := range feed.URLs {
		err := s.fetchTo(ctx, url, s.path(feed.Category))
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("%s: %w", url, err)
	}
	return lastErr
}

func (s *Store) fetchTo(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp(s.dir, ".dl-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName) // no-op once renamed
	}()

	// Read one byte past the cap so a body of exactly maxFeedBytes is accepted and
	// only a genuinely larger one trips the guard.
	n, err := io.Copy(tmp, io.LimitReader(resp.Body, maxFeedBytes+1))
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("empty body")
	}
	if n > maxFeedBytes {
		return fmt.Errorf("body exceeded %d bytes", maxFeedBytes)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
}

// Run keeps the feeds fresh until ctx is cancelled. Intended as a goroutine.
func (s *Store) Run(ctx context.Context) {
	s.Refresh(ctx, false)
	t := time.NewTicker(refreshInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Refresh(ctx, false)
		}
	}
}
