package server

import (
	"net/http"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
)

// The operator's legal documents: their public pages, the settings that edit them,
// and the API that hands them to an external system.

// serveLegal answers /<sub path>/<legal path>/<kind>: the document's page, or the
// decoy for anything else — an unknown kind or an empty document looks like any
// other missing page.
func (rt *Router) serveLegal(w http.ResponseWriter, r *http.Request, set *model.Settings, kind string) {
	docs, err := rt.mgr.LegalDocs()
	doc, ok := docs[kind]
	if err != nil || !ok || doc.Body == "" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		rt.currentDecoy().ServeHTTP(w, r)
		return
	}
	page, err := sub.LegalPage(set, doc, i18n.FromAcceptLanguage(r.Header.Get("Accept-Language")), rt.mgr.Location())
	if err != nil {
		rt.currentDecoy().ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(page)
}

// legalView is one document as the settings page and the API show it.
type legalView struct {
	Markdown  string `json:"markdown"`
	HTML      string `json:"html"`
	URL       string `json:"url"`        // its public page; "" while it is empty
	UpdatedAt int64  `json:"updated_at"` // 0 = never set
}

// legalViews is every document, rendered, with its address.
func (rt *Router) legalViews() (map[string]legalView, error) {
	docs, err := rt.mgr.LegalDocs()
	if err != nil {
		return nil, err
	}
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		return nil, err
	}
	out := make(map[string]legalView, len(docs))
	for _, kind := range model.LegalKinds {
		d := docs[kind]
		v := legalView{Markdown: d.Body, UpdatedAt: d.UpdatedAt}
		if d.Body != "" {
			h, err := sub.RenderMarkdown(d.Body)
			if err != nil {
				return nil, err
			}
			v.HTML, v.URL = string(h), sub.LegalURL(set, kind)
		}
		out[kind] = v
	}
	return out, nil
}

// legalLinks is each non-empty document's public address, for the subscription
// page and its API view.
func (rt *Router) legalLinks(set *model.Settings) (terms, privacy string) {
	docs, err := rt.mgr.LegalDocs()
	if err != nil {
		return "", ""
	}
	if docs[model.LegalTerms].Body != "" {
		terms = sub.LegalURL(set, model.LegalTerms)
	}
	if docs[model.LegalPrivacy].Body != "" {
		privacy = sub.LegalURL(set, model.LegalPrivacy)
	}
	return terms, privacy
}

func (rt *Router) getLegal(w http.ResponseWriter, _ *http.Request) {
	v, err := rt.legalViews()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// maxLegalBody bounds a save or a preview: both documents at model.MaxLegalDocLen
// characters, each up to four bytes, with room for JSON's escapes — so the length
// check that answers is SaveLegalDocs's own, not a cut-off body.
const maxLegalBody = 4 << 20

// legalSaveReq is the settings page's save: a document left out is kept.
type legalSaveReq struct {
	Terms   *string `json:"terms"`
	Privacy *string `json:"privacy"`
}

func (rt *Router) saveLegal(w http.ResponseWriter, r *http.Request) {
	var req legalSaveReq
	if !decodeJSONLimit(w, r, &req, maxLegalBody) {
		return
	}
	docs := map[string]string{}
	if req.Terms != nil {
		docs[model.LegalTerms] = *req.Terms
	}
	if req.Privacy != nil {
		docs[model.LegalPrivacy] = *req.Privacy
	}
	if err := rt.mgr.SaveLegalDocs(docs); err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.getLegal(w, r)
}

// previewLegal renders Markdown as the document's page will, for the editor's preview.
func (rt *Router) previewLegal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Markdown string `json:"markdown"`
	}
	if !decodeJSONLimit(w, r, &req, maxLegalBody) {
		return
	}
	h, err := sub.RenderMarkdown(req.Markdown)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"html": string(h)})
}

// apiLegal is GET /v1/legal: both documents — Markdown, rendered HTML, public page
// and date — for an external system to show or link.
func (rt *Router) apiLegal(w http.ResponseWriter, _ *http.Request) {
	v, err := rt.legalViews()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, apiLegalResp{Terms: v[model.LegalTerms], Privacy: v[model.LegalPrivacy]})
}

// apiLegalResp names the documents for the OpenAPI document.
type apiLegalResp struct {
	Terms   legalView `json:"terms"`   // the user agreement
	Privacy legalView `json:"privacy"` // the privacy policy
}
