package core

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AppsGanin/rospanel/internal/model"
)

// LegalDocs returns the operator's legal documents (model.LegalDoc), an unset one
// empty.
func (m *Manager) LegalDocs() (map[string]model.LegalDoc, error) {
	return m.store.LegalDocs()
}

// SaveLegalDocs stores the documents given (kind → Markdown; a kind left out is kept),
// and gives them their address the first time one is written.
func (m *Manager) SaveLegalDocs(docs map[string]string) error {
	for kind, body := range docs {
		if !model.ValidLegalKind(kind) {
			return invalidCode("err.legalKind", "неизвестный документ {{value}}", map[string]any{"value": kind})
		}
		if n := utf8.RuneCountInString(body); n > model.MaxLegalDocLen {
			return invalidCode("err.legalTooLong", "документ длиннее {{max}} символов (сейчас {{count}})",
				map[string]any{"max": model.MaxLegalDocLen, "count": n})
		}
	}
	if err := m.store.EnsureLegalPath(func() string {
		b := make([]byte, 12)
		_, _ = rand.Read(b)
		return hex.EncodeToString(b)
	}); err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, kind := range model.LegalKinds {
		body, ok := docs[kind]
		if !ok {
			continue
		}
		body = strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
		if err := m.store.SetLegalDoc(kind, body, now); err != nil {
			return err
		}
	}
	return nil
}
