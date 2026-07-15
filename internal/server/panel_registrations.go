package server

import (
	"net/http"

	"github.com/AppsGanin/rospanel/internal/model"
)

// listRegistrations returns the moderated self-registration queue plus whether the
// user bot is currently in moderation mode (so the UI can show the tab even with an
// empty queue, and hide it otherwise).
func (rt *Router) listRegistrations(w http.ResponseWriter, _ *http.Request) {
	reqs, err := rt.mgr.ListRegistrationRequests()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if reqs == nil {
		reqs = []model.RegistrationRequest{}
	}
	moderation := false
	if set, err := rt.mgr.Settings(); err == nil && set != nil {
		moderation = set.RegMode() == model.RegModeration
	}
	writeJSON(w, http.StatusOK, map[string]any{"moderation": moderation, "requests": reqs})
}

// approveRegistration creates the account for a pending request and links its chat.
func (rt *Router) approveRegistration(w http.ResponseWriter, r *http.Request, id int64) {
	if err := rt.mgr.ApproveRegistrationRequest(r.Context(), id); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// rejectRegistration drops a pending request without creating a user.
func (rt *Router) rejectRegistration(w http.ResponseWriter, r *http.Request, id int64) {
	if err := rt.mgr.RejectRegistrationRequest(r.Context(), id); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}
