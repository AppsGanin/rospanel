package telegram

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/backup"
	"github.com/AppsGanin/rospanel/internal/cron"
	"github.com/AppsGanin/rospanel/internal/model"
)

// backupLoop fires scheduled backups: it wakes every minute and lets maybeBackup
// decide whether the configured schedule is due.
func (s *Service) backupLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.maybeBackup(ctx)
		}
	}
}

// maybeBackup sends a backup when the operator's cron schedule matches the current
// minute (in the operator timezone). lastFired (seeded to the startup minute in
// New) guards against firing twice in one minute and against a restart re-firing a
// schedule that already matched the minute the process came up.
func (s *Service) maybeBackup(ctx context.Context) {
	set, err := s.store.GetSettings()
	if err != nil || !set.TGBotEnabled || strings.TrimSpace(set.TGBotToken) == "" ||
		strings.TrimSpace(set.TGBackupCron) == "" {
		return
	}
	chats := set.TelegramChatIDs()
	if len(chats) == 0 {
		return
	}
	sched, err := cron.Parse(set.TGBackupCron)
	if err != nil {
		return
	}
	nowL := time.Now().In(s.panel.Location())
	if !sched.Match(nowL) {
		return
	}
	minute := nowL.Truncate(time.Minute)
	if minute.Equal(s.lastFired) {
		return
	}
	s.lastFired = minute
	s.runScheduledBackup(ctx, set, chats)
}

func (s *Service) runScheduledBackup(ctx context.Context, set *model.Settings, chats []int64) {
	client := s.clientFor(strings.TrimSpace(set.TGBotToken))
	if err := SendBackup(ctx, client, chats, s.dataDir, s.panel.BackupManifest(),
		s.store.Checkpoint, "Автоматическая резервная копия"); err != nil {
		log.Printf("telegram: scheduled backup: %v", err)
		return
	}
	log.Printf("telegram: scheduled backup sent to %d chat(s)", len(chats))
}

// SendBackup checkpoints the DB, builds a fresh tar.gz of the data directory, and
// uploads it to each chat as a document. It's shared by the scheduler and the
// panel's "send test backup" action. A per-chat send failure doesn't abort the
// rest; the first error is returned.
func SendBackup(ctx context.Context, client *Client, chats []int64, dataDir string,
	manifest backup.Manifest, checkpoint func() error, caption string) error {
	if checkpoint != nil {
		_ = checkpoint() // flush WAL so the archived DB is complete (best-effort)
	}
	tmp, err := os.CreateTemp("", "rospanel-tg-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	err = backup.WriteWithManifest(dataDir, manifest, tmp)
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}

	name := "rospanel-backup-" + time.Now().Format("20060102-150405") + ".tar.gz"
	var firstErr error
	for _, id := range chats {
		f, oerr := os.Open(tmp.Name())
		if oerr != nil {
			if firstErr == nil {
				firstErr = oerr
			}
			continue
		}
		serr := client.SendDocument(ctx, id, name, caption, f)
		f.Close()
		if serr != nil && firstErr == nil {
			firstErr = serr
		}
	}
	return firstErr
}
