package server

import (
	"net/http"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Device binding over the external API. The panel and the subscription page already
// manage these; this is the same thing for an integration — a shop bot handing a
// customer a "forget my old phone" button, or a support tool doing it for them.
//
// It also decides what an AI assistant can do with devices: the MCP tool list is
// generated from the OpenAPI document, so a route added here becomes a tool without
// anyone maintaining a second list (and stays read-only unless the operator passes
// --allow-write).

// apiDeviceList is the response shape: the roster plus the cap it is counted
// against, so a caller doesn't have to fetch the user and the settings to know
// whether "3 devices" is fine or full.
type apiDeviceList struct {
	Devices []model.Device `json:"devices"`
	Limit   int            `json:"limit"`   // 0 = unlimited
	Enabled bool           `json:"enabled"` // device binding switched on panel-wide
}

// apiUnbindDeviceReq releases one device, or every one of them.
type apiUnbindDeviceReq struct {
	HWID string `json:"hwid,omitempty"` // the id the client reports in x-hwid
	All  bool   `json:"all,omitempty"`  // release every device of this user
}

// apiUnbindResp reports how many slots were freed.
type apiUnbindResp struct {
	Removed int64 `json:"removed"`
}

func (rt *Router) apiUserDevices(w http.ResponseWriter, r *http.Request, id int64) {
	u, err := rt.mgr.Store().GetUser(id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	set, err := rt.mgr.Settings()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	devices, err := rt.mgr.UserDevices(id)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	// Windowed like the other lists, though a device roster is small by construction
	// (it is capped): the shape stays the same across the API, and "small by
	// construction" is exactly the assumption a cap change would quietly break.
	window, meta := page(r, devices)
	if window == nil {
		window = []model.Device{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": apiDeviceList{
			Devices: window,
			Limit:   set.DeviceCap(*u),
			Enabled: set.HWIDEnabled,
		},
		"meta": meta,
	})
}

func (rt *Router) apiUnbindDevice(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiUnbindDeviceReq
	if !apiDecode(w, r, &req) {
		return
	}
	if req.All {
		n, err := rt.mgr.UnbindAllDevices(r.Context(), id)
		if err != nil {
			writeAPIManagerErr(w, err)
			return
		}
		writeAPIData(w, http.StatusOK, apiUnbindResp{Removed: n})
		return
	}
	// Cleaned the same way the subscription surface cleans the header it came from,
	// so an id that round-trips through an integration still matches the stored row.
	hwid := model.CleanHWID(strings.TrimSpace(req.HWID))
	if hwid == "" {
		writeAPIErr(w, http.StatusBadRequest, "bad_request", "hwid is required (or pass all=true)")
		return
	}
	ok, err := rt.mgr.UnbindDevice(r.Context(), id, hwid)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if !ok {
		writeAPIErr(w, http.StatusNotFound, "not_found", "device not found")
		return
	}
	writeAPIData(w, http.StatusOK, apiUnbindResp{Removed: 1})
}
