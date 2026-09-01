package server

import (
	"net/http"
)

// The admin's own open sessions. Like the second factor (panel_totp.go), every route
// acts on the CALLER: the list is theirs, the id they revoke is checked against
// their own account in the same statement, and there is no path that names another
// admin. The owner ends a colleague's sessions the way they always did — by
// resetting that admin's password, which revokes them all.

// listSessions returns the caller's live sessions, most recently used first, with
// the one making this request marked so the screen can say "this device".
func (rt *Router) listSessions(w http.ResponseWriter, r *http.Request) {
	a, ok := sessionAdminFrom(r.Context())
	if !ok {
		writeErrCode(w, http.StatusUnauthorized, "err.unauthorized", "не авторизован")
		return
	}
	list, err := rt.mgr.Store().ListAdminSessions(a.ID)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	for i := range list {
		list[i].Current = list[i].ID == a.SessionID
	}
	writeJSON(w, http.StatusOK, list)
}

// revokeSession ends one of the caller's other sessions. The current one is
// refused rather than ended: signing yourself out is what the logout button is
// for, and an accidental tap here must not drop the admin mid-task.
func (rt *Router) revokeSession(w http.ResponseWriter, r *http.Request, id int64) {
	a, ok := sessionAdminFrom(r.Context())
	if !ok {
		writeErrCode(w, http.StatusUnauthorized, "err.unauthorized", "не авторизован")
		return
	}
	if id == a.SessionID {
		writeErrCode(w, http.StatusBadRequest, "err.sessionIsCurrent", "это текущая сессия — выйдите через кнопку выхода")
		return
	}
	gone, err := rt.mgr.Store().DeleteAdminSessionByID(a.ID, id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if !gone {
		// Unknown, expired, or somebody else's: all three read the same from here,
		// which is the point — the id says nothing about whose it was.
		writeErrCode(w, http.StatusNotFound, "err.sessionNotFound", "сессия не найдена")
		return
	}
	writeOK(w)
}

// revokeOtherSessions is "sign out everywhere else": every session of the caller
// except this one. Answers with how many were ended, so the screen can say so.
func (rt *Router) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	a, ok := sessionAdminFrom(r.Context())
	if !ok {
		writeErrCode(w, http.StatusUnauthorized, "err.unauthorized", "не авторизован")
		return
	}
	n, err := rt.mgr.Store().DeleteOtherAdminSessions(a.ID, a.SessionID)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": n})
}
