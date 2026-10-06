package store

import (
	"github.com/AppsGanin/rospanel/internal/model"
)

// LegalDocs returns every legal document, an unset one empty.
func (s *Store) LegalDocs() (map[string]model.LegalDoc, error) {
	out := make(map[string]model.LegalDoc, len(model.LegalKinds))
	for _, k := range model.LegalKinds {
		out[k] = model.LegalDoc{Kind: k}
	}
	rows, err := s.db.Query(`SELECT kind, body, updated_at FROM legal_docs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d model.LegalDoc
		if err := rows.Scan(&d.Kind, &d.Body, &d.UpdatedAt); err != nil {
			return nil, err
		}
		if model.ValidLegalKind(d.Kind) {
			out[d.Kind] = d
		}
	}
	return out, rows.Err()
}

// SetLegalDoc stores a document. Its date moves only when the text does, so
// "updated" means what it says when a settings page re-saves what it loaded.
func (s *Store) SetLegalDoc(kind, body string, now int64) error {
	_, err := s.db.Exec(`INSERT INTO legal_docs (kind, body, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(kind) DO UPDATE SET body = excluded.body, updated_at = excluded.updated_at
		WHERE legal_docs.body <> excluded.body`, kind, body, now)
	return err
}

// EnsureLegalPath gives the documents their random address segment, once.
func (s *Store) EnsureLegalPath(gen func() string) error {
	_, err := s.db.Exec(`UPDATE settings SET legal_path = ? WHERE id = 1 AND legal_path = ''`, gen())
	return err
}
