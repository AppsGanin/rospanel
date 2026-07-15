package store

import (
	"database/sql"
	"errors"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Moderated self-registration requests. A request is a signup held for an admin
// decision; approving it is what actually creates the user (see core.Manager).

// CreateRegistrationRequest inserts a pending request for a chat. chat_id is unique,
// so a chat with an existing request keeps the first one (ErrRegistrationPending).
func (s *Store) CreateRegistrationRequest(chatID int64, name string, now int64) (*model.RegistrationRequest, error) {
	res, err := s.db.Exec(
		`INSERT INTO registration_requests (chat_id, name, created_at) VALUES (?, ?, ?)
		 ON CONFLICT(chat_id) DO NOTHING`,
		chatID, name, now)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrRegistrationPending
	}
	return s.GetRegistrationRequestByChat(chatID)
}

// ErrRegistrationPending means the chat already has a pending request.
var ErrRegistrationPending = errors.New("registration already pending")

// GetRegistrationRequest returns one request by id.
func (s *Store) GetRegistrationRequest(id int64) (*model.RegistrationRequest, error) {
	return s.scanRegistrationRequest(`WHERE id = ?`, id)
}

// GetRegistrationRequestByChat returns a chat's pending request, or nil when none.
func (s *Store) GetRegistrationRequestByChat(chatID int64) (*model.RegistrationRequest, error) {
	r, err := s.scanRegistrationRequest(`WHERE chat_id = ?`, chatID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

func (s *Store) scanRegistrationRequest(where string, args ...any) (*model.RegistrationRequest, error) {
	var r model.RegistrationRequest
	err := s.db.QueryRow(
		`SELECT id, chat_id, name, created_at FROM registration_requests `+where, args...).
		Scan(&r.ID, &r.ChatID, &r.Name, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ListRegistrationRequests returns all pending requests, oldest first.
func (s *Store) ListRegistrationRequests() ([]model.RegistrationRequest, error) {
	rows, err := s.db.Query(`SELECT id, chat_id, name, created_at FROM registration_requests ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.RegistrationRequest
	for rows.Next() {
		var r model.RegistrationRequest
		if err := rows.Scan(&r.ID, &r.ChatID, &r.Name, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRegistrationRequest removes a request (after approval or rejection).
func (s *Store) DeleteRegistrationRequest(id int64) error {
	_, err := s.db.Exec(`DELETE FROM registration_requests WHERE id = ?`, id)
	return err
}
