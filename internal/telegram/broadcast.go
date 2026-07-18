package telegram

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// BroadcastService delivers mass messages through the user bot. It is a poller over
// the store, not a queue in memory: the recipient list lives in broadcast_targets,
// so a restart mid-run resumes instead of losing or repeating anything.
//
// Pacing is left entirely to the package rate limiter (ratelimit.go) — every send
// goes through Client.send, which reserves a slot and honours a 429's retry_after. A
// second regulator here would only let the two of them together exceed the ceiling
// each was written to respect.
type BroadcastService struct {
	store   *store.Store
	dataDir string

	mu          sync.Mutex
	client      *Client
	clientToken string
}

const (
	// broadcastBatch is how many recipients are claimed per pass. Small enough that
	// a pause or cancel takes effect within seconds rather than at the end of the run.
	broadcastBatch = 50
	// broadcastWorkers overlaps network round-trips. The limiter still caps the
	// actual rate; without any overlap the send rate would be one per round-trip
	// (~10/s on a 100 ms link) instead of the ~30/s the limiter allows.
	broadcastWorkers = 8
	// broadcastIdle is the pause between polls when nothing is running.
	broadcastIdle = 5 * time.Second
)

// BroadcastMediaDir is where the panel stores an uploaded attachment until the first
// recipient turns it into a Telegram file_id.
func BroadcastMediaDir(dataDir string) string { return filepath.Join(dataDir, "broadcasts") }

// BroadcastMediaPath is the attachment's file for one broadcast.
func BroadcastMediaPath(dataDir string, id int64) string {
	return filepath.Join(BroadcastMediaDir(dataDir), strconv.FormatInt(id, 10))
}

// NewBroadcast builds the delivery worker. Call Run to start it.
func NewBroadcast(st *store.Store, dataDir string) *BroadcastService {
	return &BroadcastService{store: st, dataDir: dataDir}
}

func (s *BroadcastService) clientFor(token string) *Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil || s.clientToken != token {
		s.client = NewClient(token)
		s.clientToken = token
	}
	return s.client
}

// Run delivers running broadcasts until ctx is cancelled.
func (s *BroadcastService) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		worked := s.step(ctx)
		if !worked && !sleep(ctx, broadcastIdle) {
			return
		}
	}
}

// step delivers at most one batch and reports whether it had anything to do.
func (s *BroadcastService) step(ctx context.Context) bool {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("telegram broadcast: panic recovered: %v", r)
		}
	}()

	set, err := s.store.GetSettings()
	if err != nil || !set.TGUserBotEnabled || strings.TrimSpace(set.TGUserBotToken) == "" {
		return false
	}
	b, err := s.store.NextRunningBroadcast()
	if err != nil || b == nil {
		if err != nil {
			log.Printf("telegram broadcast: pick: %v", err)
		}
		return false
	}
	targets, err := s.store.NextPendingTargets(b.ID, broadcastBatch)
	if err != nil {
		log.Printf("telegram broadcast %d: targets: %v", b.ID, err)
		return false
	}
	if len(targets) == 0 {
		s.finish(b)
		return true
	}

	client := s.clientFor(strings.TrimSpace(set.TGUserBotToken))
	// An attachment is uploaded once, to the first recipient, and every later send
	// reuses the file_id — otherwise the same bytes go over the wire once per person.
	if b.MediaKind != "" && b.MediaFileID == "" {
		if !s.primeMedia(ctx, client, b, targets[0]) {
			return true
		}
		targets = targets[1:]
	}
	s.deliver(ctx, client, b, targets)
	return true
}

// finish marks a drained broadcast done and drops its uploaded file.
func (s *BroadcastService) finish(b *model.Broadcast) {
	if err := s.store.SetBroadcastStatus(b.ID, model.BroadcastDone, time.Now().Unix()); err != nil {
		log.Printf("telegram broadcast %d: finish: %v", b.ID, err)
		return
	}
	s.removeMedia(b.ID)
	log.Printf("telegram broadcast %d: finished", b.ID)
}

// primeMedia sends the attachment to the first recipient and caches the file_id it
// comes back with. Reports whether the run may continue.
func (s *BroadcastService) primeMedia(ctx context.Context, client *Client, b *model.Broadcast, chatID int64) bool {
	f, err := os.Open(BroadcastMediaPath(s.dataDir, b.ID))
	if err != nil {
		// The file is gone (manually removed, or the panel's data dir was restored
		// without it). Sending the text alone would silently drop what the operator
		// attached, so stop and say so rather than deliver something else.
		log.Printf("telegram broadcast %d: media missing: %v", b.ID, err)
		if err := s.store.SetBroadcastStatus(b.ID, model.BroadcastPaused, 0); err != nil {
			log.Printf("telegram broadcast %d: pause: %v", b.ID, err)
		}
		return false
	}
	defer f.Close()

	name := b.MediaName
	if name == "" {
		name = "file"
	}
	var fileID string
	if b.MediaKind == "photo" {
		fileID, err = client.UploadPhoto(ctx, chatID, name, b.Text, f)
	} else {
		fileID, err = client.UploadDocument(ctx, chatID, name, b.Text, f)
	}
	if err != nil {
		s.record(b.ID, chatID, err)
		// Not fatal to the run: this one recipient may be blocked. The next pass
		// tries to prime on somebody else.
		return false
	}
	s.record(b.ID, chatID, nil)
	if fileID == "" {
		// Delivered, but Telegram returned no id to reuse — every later recipient
		// would re-upload. Pause rather than quietly burn the bandwidth.
		log.Printf("telegram broadcast %d: no file_id returned; pausing", b.ID)
		if err := s.store.SetBroadcastStatus(b.ID, model.BroadcastPaused, 0); err != nil {
			log.Printf("telegram broadcast %d: pause: %v", b.ID, err)
		}
		return false
	}
	if err := s.store.SetBroadcastMediaFileID(b.ID, fileID); err != nil {
		log.Printf("telegram broadcast %d: cache file_id: %v", b.ID, err)
		return false
	}
	b.MediaFileID = fileID
	return true
}

// deliver sends one batch, overlapping round-trips across a few workers while the
// package limiter keeps the aggregate rate legal.
func (s *BroadcastService) deliver(ctx context.Context, client *Client, b *model.Broadcast, targets []int64) {
	if len(targets) == 0 {
		return
	}
	ch := make(chan int64)
	var wg sync.WaitGroup
	for range min(broadcastWorkers, len(targets)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for chatID := range ch {
				s.record(b.ID, chatID, s.sendOne(ctx, client, b, chatID))
			}
		}()
	}
	for _, chatID := range targets {
		select {
		case <-ctx.Done():
			close(ch)
			wg.Wait()
			return
		case ch <- chatID:
		}
	}
	close(ch)
	wg.Wait()
}

func (s *BroadcastService) sendOne(ctx context.Context, client *Client, b *model.Broadcast, chatID int64) error {
	rows := broadcastRows(b.Buttons)
	switch b.MediaKind {
	case "photo":
		return client.SendPhotoID(ctx, chatID, b.MediaFileID, b.Text, rows)
	case "document":
		return client.SendDocumentID(ctx, chatID, b.MediaFileID, b.Text, rows)
	default:
		return client.SendMenu(ctx, chatID, b.Text, rows)
	}
}

// record writes one delivery outcome. A permanent refusal is stored as "blocked" and
// the subscriber is deactivated, so every later broadcast stops spending a send slot
// on a chat that can never receive again.
func (s *BroadcastService) record(broadcastID, chatID int64, sendErr error) {
	now := time.Now().Unix()
	state, msg := model.TargetSent, ""
	switch {
	case sendErr == nil:
	case isBlockedByUser(sendErr):
		state, msg = model.TargetBlocked, sendErr.Error()
		if err := s.store.SetSubscriberBlocked(chatID, now); err != nil {
			log.Printf("telegram broadcast: mark %d blocked: %v", chatID, err)
		}
	default:
		state, msg = model.TargetFailed, sendErr.Error()
	}
	if err := s.store.MarkTarget(broadcastID, chatID, state, msg, now); err != nil {
		log.Printf("telegram broadcast %d: mark %d: %v", broadcastID, chatID, err)
	}
}

func (s *BroadcastService) removeMedia(id int64) {
	if err := os.Remove(BroadcastMediaPath(s.dataDir, id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("telegram broadcast %d: remove media: %v", id, err)
	}
}

// IsUnreachable reports whether Telegram refused permanently because it will never
// deliver to that chat — the user blocked the bot, never started it, or the account
// is gone. Exported so the panel can turn it into an instruction the operator can
// act on instead of a raw API error.
func IsUnreachable(err error) bool { return isBlockedByUser(err) }

// BroadcastButtonRows renders the URL buttons for a broadcast (exported so a test
// send from the panel renders them identically to the real run).
func BroadcastButtonRows(buttons []model.BroadcastButton) [][]InlineButton {
	return broadcastRows(buttons)
}

// broadcastRows renders the URL buttons, one per row so long labels stay readable.
func broadcastRows(buttons []model.BroadcastButton) [][]InlineButton {
	if len(buttons) == 0 {
		return nil
	}
	rows := make([][]InlineButton, 0, len(buttons))
	for _, b := range buttons {
		rows = append(rows, []InlineButton{{Text: b.Text, URL: b.URL}})
	}
	return rows
}
