package server

import (
	"encoding/json"
	"fmt"
	"github.com/AppsGanin/rospanel/internal/actor"
	"github.com/AppsGanin/rospanel/internal/model"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/AppsGanin/rospanel/internal/core"
)

// Importing users from another panel (Marzban, 3x-ui). Two calls: inspect uploads
// the file and answers with what it holds; import creates the rows the operator
// kept. The upload is read into a temp file the parser opens read-only, and
// deleted before the response goes out — nothing of the other panel's database
// stays on this machine.

// maxImportUpload bounds the upload. A 3x-ui database with thousands of users is
// a few megabytes; Marzban's, with its traffic history tables, can run to a few
// hundred. Same ceiling as a backup restore.
const maxImportUpload = 512 << 20

func (rt *Router) importInspect(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(10 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, maxImportUpload)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErrCode(w, http.StatusBadRequest, "err.uploadParseError", "ошибка разбора загрузки")
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, "err.importNoFile", "выберите файл")
		return
	}
	defer f.Close()

	tmp, err := os.CreateTemp("", "rospanel-import-*")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, f); err != nil {
		tmp.Close()
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	tmp.Close()

	preview, err := rt.mgr.ImportPreview(tmp.Name())
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (rt *Router) importUsers(w http.ResponseWriter, r *http.Request) {
	// The body carries one candidate per user, each a few hundred bytes; the
	// batch cap in core bounds it, this bounds a client that ignores the cap.
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	var req core.ImportRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := rt.mgr.ImportUsers(r.Context(), req)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// exportUsers hands back every user as this panel's own export file — the format
// importUsers reads, so one install's users move to another with their
// credentials, limits and usage intact.
//
// The file carries UUIDs, passwords, subscription tokens and tunnel keys. That is
// no more than the panel already shows whoever can open a user card, but it is all
// of it in one file, so the download is admin-level (like a backup) and lands in
// the panel log.
func (rt *Router) exportUsers(w http.ResponseWriter, r *http.Request) {
	out, err := rt.mgr.ExportUsers()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	a := actor.From(r.Context())
	rt.mgr.AddAdminAudit(model.AdminAudit{
		Action:    model.AuditUsersExported,
		ActorKind: a.Kind,
		ActorName: a.Name,
		IP:        clientIP(r),
		Details:   map[string]any{"users": len(out.Users)},
	})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="rospanel-users-%s.json"`,
		time.Now().Format("2006-01-02")))
	_, _ = w.Write(body)
}
