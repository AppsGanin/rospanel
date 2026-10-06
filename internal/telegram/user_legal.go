package telegram

import (
	"context"
	"fmt"
	"html"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
)

// The operator's legal documents in the bot: a line under the welcome naming what
// signing up accepts, and a "Documents" button in the account menu. Only what has
// text is shown; with neither, the bot says nothing about them.

// legalDoc is one document the bot links to.
type legalDoc struct {
	kind string
	url  string
}

// legalDocs lists the documents that have text and an address.
func (s *UserService) legalDocs(set *model.Settings) []legalDoc {
	docs, err := s.store.LegalDocs()
	if err != nil {
		return nil
	}
	var out []legalDoc
	for _, kind := range model.LegalKinds {
		if docs[kind].Body == "" {
			continue
		}
		if url := sub.LegalURL(set, kind); url != "" {
			out = append(out, legalDoc{kind: kind, url: url})
		}
	}
	return out
}

// legalAcceptLine is the welcome's "by signing up you accept …", or "".
func (s *UserService) legalAcceptLine(set *model.Settings, lang i18n.Lang) string {
	docs := s.legalDocs(set)
	links := make([]any, 0, len(docs))
	for _, d := range docs {
		name := i18n.T(lang, "user.legalTermsAcc")
		if d.kind == model.LegalPrivacy {
			name = i18n.T(lang, "user.legalPrivacyAcc")
		}
		links = append(links, fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(d.url), html.EscapeString(name)))
	}
	switch len(links) {
	case 0:
		return ""
	case 1:
		return i18n.T(lang, "user.legalAcceptOne", links...)
	default:
		return i18n.T(lang, "user.legalAcceptBoth", links...)
	}
}

// legalMenuRow is the account menu's "Documents" button, or nil with none.
func (s *UserService) legalMenuRow(set *model.Settings, lang i18n.Lang) []InlineButton {
	if len(s.legalDocs(set)) == 0 {
		return nil
	}
	return []InlineButton{{Text: i18n.T(lang, "user.btnLegal"), CallbackData: "vu:legal"}}
}

// showLegal answers "Documents": a button for each, opening its page.
func (s *UserService) showLegal(ctx context.Context, client *Client, chatID, msgID int64, set *model.Settings) {
	lang := s.lang(chatID)
	var rows [][]InlineButton
	for _, d := range s.legalDocs(set) {
		rows = append(rows, []InlineButton{{Text: sub.LegalTitle(d.kind, lang), URL: d.url}})
	}
	rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnBack"), CallbackData: "vu:menu"}})
	s.edit(ctx, client, chatID, msgID, i18n.T(lang, "user.legalTitle"), rows)
}
