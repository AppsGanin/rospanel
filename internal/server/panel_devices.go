package server

import (
	"net/http"
	"strings"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

// The user card's Devices list: which installs hold this account's device slots,
// and the buttons that take a slot back. See internal/core/manager_devices.go for
// how a slot is claimed in the first place.

// userDevices lists the devices bound to a user.
func (rt *Router) userDevices(w http.ResponseWriter, _ *http.Request, id int64) {
	devices, err := rt.mgr.UserDevices(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	u, err := rt.mgr.Store().GetUser(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	// The cap travels with the list so the card can show "2 of 3" without the
	// frontend re-deriving which of the two limits applies to this user.
	writeJSON(w, http.StatusOK, map[string]any{
		"devices": devices,
		"limit":   set.DeviceCap(*u),
		"enabled": set.HWIDEnabled,
	})
}

// unbindUserDevice releases one device slot. The HWID is client-supplied text, so it
// arrives in the body rather than the path — it can contain anything a URL segment
// would have to be escaped for.
func (rt *Router) unbindUserDevice(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		HWID string `json:"hwid"`
		All  bool   `json:"all"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.All {
		n, err := rt.mgr.UnbindAllDevices(r.Context(), id)
		if err != nil {
			writeManagerErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"removed": n})
		return
	}
	hwid := strings.TrimSpace(req.HWID)
	if hwid == "" {
		writeErrCode(w, http.StatusBadRequest, "err.deviceHWIDEmpty", "не указано устройство")
		return
	}
	ok, err := rt.mgr.UnbindDevice(r.Context(), id, hwid)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if !ok {
		writeErrCode(w, http.StatusNotFound, "err.deviceNotFound", "устройство не найдено")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": 1})
}

// saveHWIDSettings persists the device-binding settings.
func (rt *Router) saveHWIDSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled       bool `json:"enabled"`
		Require       bool `json:"require"`
		FallbackLimit int  `json:"fallback_limit"`
		TTLDays       int  `json:"ttl_days"`
		// CountMode rides along because it is the same screen and the same Save: which
		// counter enforces the limit is a device setting, not a separate feature.
		// Pointer, so "absent" and "empty" are distinguishable and this surface refuses
		// exactly what /v1 refuses. A bare "" used to mean "leave alone" here and
		// "invalid" there.
		CountMode *string `json:"count_mode"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.FallbackLimit < 0 || req.TTLDays < 0 {
		writeErrCode(w, http.StatusBadRequest, "err.badValue", "значение не может быть отрицательным")
		return
	}
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if req.CountMode != nil {
		// Validated in the manager, so this screen, /v1 and the MCP tool refuse the same
		// values. Applied first: a rejected mode must not leave the HWID half saved.
		if err := rt.mgr.SetDeviceCountMode(*req.CountMode); err != nil {
			writeManagerErr(w, err)
			return
		}
	}
	set.HWIDEnabled, set.HWIDRequire = req.Enabled, req.Require
	set.HWIDFallbackLimit, set.HWIDTTLDays = req.FallbackLimit, req.TTLDays
	if err := rt.mgr.Store().SetHWIDSettings(set); err != nil {
		writeManagerErr(w, err)
		return
	}
	// Deliberately no user sync: none of these settings reach the proxy config. They
	// govern who may FETCH a subscription, which is decided per request against the
	// stored row. Syncing here rewrote config.json and woke every node for a change
	// none of them can see.
	writeOK(w)
}

// hwidSettingsView is the settings block the panel renders (part of the settings
// payload, not its own GET).
func hwidSettingsView(set *model.Settings) map[string]any {
	return map[string]any{
		"enabled":        set.HWIDEnabled,
		"require":        set.HWIDRequire,
		"fallback_limit": set.HWIDFallbackLimit,
		"ttl_days":       set.HWIDTTLDays,
		"count_mode":     set.DeviceCountModeOr(),
	}
}
