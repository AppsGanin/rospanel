package server

import (
	"net/http"

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/model"
)

// The user card's AmneziaWG keys: one per device beyond the main one. See
// internal/core/manager_awg_devices.go.

// awgKeyView is one key as the card and the subscription page list it.
type awgKeyView struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	HWID      string `json:"hwid,omitempty"`
	Addr      string `json:"addr"`
	CreatedAt int64  `json:"created_at"`
	// Active is false for a key past the user's device cap (a cap lowered after it
	// was made): it is a peer nowhere until the cap allows it again.
	Active bool `json:"active"`
}

func awgKeyViews(list []model.AWGDevice, capKeys int) []awgKeyView {
	out := make([]awgKeyView, 0, len(list))
	active := model.ActiveAWGDevices(len(list), capKeys)
	for i, d := range list {
		v := awgKeyView{ID: d.ID, Name: d.Name, HWID: d.HWID, CreatedAt: d.CreatedAt, Active: i < active}
		if a, ok := awg.ClientAddr(d.Slot); ok {
			v.Addr = a.String()
		}
		out = append(out, v)
	}
	return out
}

// userAWGKeys lists a user's device keys and the cap (main key included).
func (rt *Router) userAWGKeys(w http.ResponseWriter, _ *http.Request, id int64) {
	list, capacity, err := rt.mgr.UserAWGKeys(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": awgKeyViews(list, capacity), "limit": capacity, "enabled": rt.mgr.AWGInUse()})
}

// addUserAWGKey makes a device key by hand.
func (rt *Router) addUserAWGKey(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	d, _, err := rt.mgr.AddUserAWGKey(r.Context(), id, req.Name, "")
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, awgKeyViews([]model.AWGDevice{*d}, 2)[0])
}

// renameUserAWGKey renames a device key; the id is in the body, as with devices.
func (rt *Router) renameUserAWGKey(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		KeyID int64  `json:"key_id"`
		Name  string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.RenameUserAWGKey(id, req.KeyID, req.Name); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// deleteUserAWGKey removes a device key.
func (rt *Router) deleteUserAWGKey(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		KeyID int64 `json:"key_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.DeleteUserAWGKey(r.Context(), id, req.KeyID); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}
