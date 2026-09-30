package server

import (
	"errors"
	"net/http"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/model"
)

// POST /v1/signup: self-registration for the operator's own website. See
// core.Manager.Signup for the rules it keeps and POST /v1/users for the operator's
// own, rule-free, way of making an account.

type (
	apiSignupReq struct {
		// ExternalID is the site's own id for its client (an e-mail, an account
		// number), compared exactly. Signing up again with it returns the same account.
		ExternalID string `json:"external_id"`
		Name       string `json:"name,omitempty"`   // the account's name in the panel; external_id when empty
		Source     string `json:"source,omitempty"` // where the client came from (utm, ad), as a /start tag
		Ref        string `json:"ref,omitempty"`    // the invite code of the user who referred them
		Invite     string `json:"invite,omitempty"` // the registration code, when sign-up is by invitation
		IP         string `json:"ip,omitempty"`     // the client's address, for the per-address rate limit
	}
	// apiSignupResp is the account (created or existing) or the pending request.
	apiSignupResp struct {
		Status string `json:"status"` // created | existing | pending
		UserID int64  `json:"user_id,omitempty"`
		// User is the account in full: always for a new one, and for an existing one
		// only to a key that may read users.
		User      *userView `json:"user,omitempty"`
		RequestID int64     `json:"request_id,omitempty"`
	}
)

func (rt *Router) apiSignup(w http.ResponseWriter, r *http.Request) {
	var req apiSignupReq
	if !apiDecode(w, r, &req) {
		return
	}
	res, err := rt.mgr.Signup(r.Context(), core.SignupRequest{
		ExternalID: req.ExternalID, Name: req.Name, Source: req.Source,
		Ref: req.Ref, Invite: req.Invite, IP: req.IP,
	})
	if errors.Is(err, core.ErrSignupBusy) {
		w.Header().Set("Retry-After", "60")
		writeAPIErr(w, http.StatusTooManyRequests, "rate_limited", "too many sign-ups — try again in a minute")
		return
	}
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	out := apiSignupResp{Status: res.Status, RequestID: res.RequestID}
	code := http.StatusAccepted
	if res.User != nil {
		out.UserID = res.User.ID
		code = http.StatusOK
		if res.Status == core.SignupCreated {
			code = http.StatusCreated
		}
	}
	// An existing account is someone else's sign-up as far as this call knows: a key
	// that may only sign up must not read accounts by guessing their ids.
	if res.User != nil && (res.Status == core.SignupCreated || apiPerms(r).Has(model.PermUsersView)) {
		set, err := rt.mgr.Store().GetSettings()
		if err != nil {
			writeAPIManagerErr(w, err)
			return
		}
		rt.applyTLSHints(set)
		view := rt.userViewFor(*res.User, set, "")
		out.User = &view
	}
	writeAPIData(w, code, out)
}
