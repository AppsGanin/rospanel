package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Broadcast persistence. See migrations/0033_broadcasts.sql for why the recipient
// list lives on disk and the counters do not.

// targetBatch caps how many recipients one INSERT statement carries. SQLite's
// variable limit is what's being respected here, not row count.
const targetBatch = 400

// CreateBroadcast inserts a broadcast, paused. It is started separately, once the
// caller has finished putting everything in place — an attachment is written to disk
// after the row exists (it is named by id), and a worker that saw the row as running
// in between would find no file and stop the run.
func (s *Store) CreateBroadcast(b *model.Broadcast, now int64) (int64, error) {
	buttons := ""
	if len(b.Buttons) > 0 {
		raw, err := json.Marshal(b.Buttons)
		if err != nil {
			return 0, err
		}
		buttons = string(raw)
	}
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO broadcasts (created_by, text, media_kind, media_name, buttons_json,
		                         audience, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		b.CreatedBy, b.Text, b.MediaKind, b.MediaName, buttons,
		b.Audience, model.BroadcastPaused, now,
	).Scan(&id)
	return id, err
}

// AddBroadcastTargets materialises the audience snapshot. Inserted in one
// transaction so a broadcast is never left with a half-built recipient list that the
// worker would happily treat as complete.
func (s *Store) AddBroadcastTargets(broadcastID int64, chatIDs []int64) error {
	if len(chatIDs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed
	for start := 0; start < len(chatIDs); start += targetBatch {
		end := min(start+targetBatch, len(chatIDs))
		chunk := chatIDs[start:end]
		args := make([]any, 0, len(chunk)*2)
		values := make([]string, 0, len(chunk))
		for _, id := range chunk {
			values = append(values, "(?, ?)")
			args = append(args, broadcastID, id)
		}
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO broadcast_targets (broadcast_id, chat_id) VALUES `+
				strings.Join(values, ","), args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// NextPendingTargets returns up to limit recipients still awaiting delivery.
func (s *Store) NextPendingTargets(broadcastID int64, limit int) ([]int64, error) {
	rows, err := s.db.Query(
		`SELECT chat_id FROM broadcast_targets
		 WHERE broadcast_id = ? AND state = ? ORDER BY chat_id LIMIT ?`,
		broadcastID, model.TargetPending, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// MarkTarget records one delivery outcome. The state moves off 'pending' whatever
// happened, so a recipient is attempted once per run and a resume can't repeat it.
func (s *Store) MarkTarget(broadcastID, chatID int64, state, errMsg string, at int64) error {
	_, err := s.db.Exec(
		`UPDATE broadcast_targets
		 SET state = ?, error = ?, attempts = attempts + 1, sent_at = ?
		 WHERE broadcast_id = ? AND chat_id = ?`,
		state, errMsg, at, broadcastID, chatID)
	return err
}

// SetBroadcastMediaFileID caches the file_id Telegram assigned on the first upload,
// so the remaining recipients are served by id instead of re-uploading the file.
func (s *Store) SetBroadcastMediaFileID(id int64, fileID string) error {
	_, err := s.db.Exec(`UPDATE broadcasts SET media_file_id = ? WHERE id = ?`, fileID, id)
	return err
}

// SetBroadcastStatus moves a broadcast between running/paused/done/cancelled.
// finishedAt is stamped only for the terminal states; started_at is stamped on the
// first move to running and never overwritten, so resuming keeps the original launch
// time rather than pretending the run began at the resume.
func (s *Store) SetBroadcastStatus(id int64, status string, at int64) error {
	if status == model.BroadcastRunning {
		_, err := s.db.Exec(
			`UPDATE broadcasts SET status = ?,
			        started_at = CASE WHEN started_at = 0 THEN ? ELSE started_at END
			 WHERE id = ?`, status, at, id)
		return err
	}
	_, err := s.db.Exec(
		`UPDATE broadcasts SET status = ?, finished_at = ? WHERE id = ?`, status, at, id)
	return err
}

// RetryFailedBroadcast puts failed recipients back in the queue and resumes the run.
// 'blocked' rows are left alone: Telegram will refuse them again for the same reason.
func (s *Store) RetryFailedBroadcast(id, now int64) (int, error) {
	res, err := s.db.Exec(
		`UPDATE broadcast_targets SET state = ?, error = ''
		 WHERE broadcast_id = ? AND state = ?`,
		model.TargetPending, id, model.TargetFailed)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n > 0 {
		// Re-open a run that had already finished, and clear its end stamp: it is
		// finished again only once the retried recipients drain.
		if err := s.SetBroadcastStatus(id, model.BroadcastRunning, now); err != nil {
			return 0, err
		}
		if _, err := s.db.Exec(`UPDATE broadcasts SET finished_at = 0 WHERE id = ?`, id); err != nil {
			return 0, err
		}
	}
	return int(n), nil
}

// NextRunningBroadcast returns the oldest broadcast still being delivered, or nil.
// Oldest-first so a queue drains in the order it was launched.
func (s *Store) NextRunningBroadcast() (*model.Broadcast, error) {
	b, err := s.scanBroadcast(`WHERE status = ? ORDER BY id LIMIT 1`, model.BroadcastRunning)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return b, err
}

// GetBroadcast returns one broadcast with its progress counters.
func (s *Store) GetBroadcast(id int64) (*model.Broadcast, error) {
	b, err := s.scanBroadcast(`WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	return b, s.fillCounts(b)
}

// ListBroadcasts returns the newest broadcasts with their progress counters.
func (s *Store) ListBroadcasts(limit int) ([]model.Broadcast, error) {
	rows, err := s.db.Query(broadcastSelect+` ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Broadcast
	for rows.Next() {
		b, err := scanBroadcastRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.fillCounts(&out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

const broadcastSelect = `SELECT id, created_by, text, media_kind, media_file_id, media_name,
       buttons_json, audience, status, created_at, started_at, finished_at FROM broadcasts`

type rowScanner interface{ Scan(dest ...any) error }

func scanBroadcastRow(r rowScanner) (*model.Broadcast, error) {
	var b model.Broadcast
	var buttons string
	if err := r.Scan(&b.ID, &b.CreatedBy, &b.Text, &b.MediaKind, &b.MediaFileID, &b.MediaName,
		&buttons, &b.Audience, &b.Status, &b.CreatedAt, &b.StartedAt, &b.FinishedAt); err != nil {
		return nil, err
	}
	if buttons != "" {
		// A row written by a newer version could carry something this build can't
		// read; dropping the buttons is better than failing the whole broadcast.
		_ = json.Unmarshal([]byte(buttons), &b.Buttons)
	}
	return &b, nil
}

func (s *Store) scanBroadcast(where string, args ...any) (*model.Broadcast, error) {
	return scanBroadcastRow(s.db.QueryRow(broadcastSelect+" "+where, args...))
}

// fillCounts derives the progress figures from the recipient rows — the single
// source of truth for what actually happened.
func (s *Store) fillCounts(b *model.Broadcast) error {
	rows, err := s.db.Query(
		`SELECT state, COUNT(*) FROM broadcast_targets WHERE broadcast_id = ? GROUP BY state`, b.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return err
		}
		b.Total += n
		switch state {
		case model.TargetSent:
			b.Sent = n
		case model.TargetFailed:
			b.Failed = n
		case model.TargetBlocked:
			b.Blocked = n
		}
	}
	return rows.Err()
}
