package server

import (
	"net/http"
	"sort"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/telegram"
)

func (rt *Router) listUsers(w http.ResponseWriter, r *http.Request) {
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	users, err := rt.mgr.Store().ListUsers()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.applyTLSHints(set)
	bot := botUsername(r.Context(), set.TGUserBotToken, set.TelegramProxyURL())
	custom := rt.localInbounds()
	groupsMap, _ := rt.mgr.GroupsForAllUsers()
	accessMap, _ := rt.mgr.Store().AccessMap()
	views := make([]userView, 0, len(users))
	for _, u := range users {
		views = append(views, makeUserView(u, set, bot, custom, groupsMap[u.ID], model.AccessOf(accessMap, u.ID)))
	}
	writeJSON(w, http.StatusOK, views)
}

func (rt *Router) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		DataLimit int64  `json:"data_limit"`
		ExpireAt  int64  `json:"expire_at"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErrCode(w, http.StatusBadRequest, "err.nameRequired2", "укажите имя")
		return
	}
	u, err := rt.mgr.CreateUser(r.Context(), req.Name, req.DataLimit, req.ExpireAt)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.applyTLSHints(set)
	writeJSON(w, http.StatusCreated, rt.userViewFor(*u, set, botUsername(r.Context(), set.TGUserBotToken, set.TelegramProxyURL())))
}

// bulkUsers applies one action to a set of users in a single pass (one Xray sync),
// for the multi-select toolbar. See core.BulkUserAction for the supported actions.
func (rt *Router) bulkUsers(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs    []int64 `json:"ids"`
		Action string  `json:"action"`
		Days   int     `json:"days"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	affected, err := rt.mgr.BulkUserAction(r.Context(), req.IDs, req.Action, req.Days)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"affected": affected})
}

func (rt *Router) deleteUser(w http.ResponseWriter, r *http.Request, id int64) {
	if err := rt.mgr.DeleteUser(r.Context(), id); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) resetUserTraffic(w http.ResponseWriter, r *http.Request, id int64) {
	if err := rt.mgr.ResetTraffic(r.Context(), id); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) setUserLimits(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		DataLimit   int64 `json:"data_limit"`
		ExpireAt    int64 `json:"expire_at"`
		DeviceLimit int   `json:"device_limit"`
		// SpeedLimit is kbit/s (0 = unlimited). Pointer so a caller that doesn't
		// mention it leaves the cap alone — the bots and older integrations post this
		// body without it, and reading a missing field as 0 would silently lift
		// everyone's cap the first time they change a quota.
		SpeedLimit *int `json:"speed_limit"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.DeviceLimit < 0 {
		writeErrCode(w, http.StatusBadRequest, "err.deviceLimitNegative2", "лимит устройств не может быть отрицательным")
		return
	}
	if err := rt.mgr.SetUserLimits(r.Context(), id, req.DataLimit, req.ExpireAt, req.DeviceLimit); err != nil {
		writeManagerErr(w, err)
		return
	}
	if req.SpeedLimit != nil {
		if err := rt.mgr.SetUserSpeedLimit(r.Context(), id, *req.SpeedLimit); err != nil {
			writeManagerErr(w, err)
			return
		}
	}
	writeOK(w)
}

// rotateSubToken issues a new subscription URL for a user. The old link stops
// working; protocol credentials are unchanged.
func (rt *Router) rotateSubToken(w http.ResponseWriter, r *http.Request, id int64) {
	u, err := rt.mgr.RotateSubToken(r.Context(), id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.applyTLSHints(set)
	writeJSON(w, http.StatusOK, rt.userViewFor(*u, set, botUsername(r.Context(), set.TGUserBotToken, set.TelegramProxyURL())))
}

// unlinkUserTelegram detaches a VPN user's linked Telegram chat (admin action).
func (rt *Router) unlinkUserTelegram(w http.ResponseWriter, r *http.Request, id int64) {
	if err := rt.mgr.UnlinkUserTelegram(r.Context(), id); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// genUserTelegramLink mints a fresh one-time bind deep link for a user. The code
// expires after model.TelegramLinkCodeTTL and is burned once used.
func (rt *Router) genUserTelegramLink(w http.ResponseWriter, r *http.Request, id int64) {
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	bot := botUsername(r.Context(), set.TGUserBotToken, set.TelegramProxyURL())
	if !set.TGUserBotEnabled || bot == "" {
		writeErrCode(w, http.StatusBadRequest, "err.userBotUnavailable", "пользовательский бот выключен или недоступен")
		return
	}
	code, err := rt.mgr.GenerateUserTgLinkCode(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deep_link":   telegram.UserDeepLink(bot, code),
		"expires_sec": int(model.TelegramLinkCodeTTL.Seconds()),
	})
}

func (rt *Router) userConnections(w http.ResponseWriter, _ *http.Request, id int64) {
	conns, err := rt.mgr.Connections(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if conns == nil {
		conns = []model.Connection{}
	}
	writeJSON(w, http.StatusOK, conns)
}

// renameUser updates a user's display name.
func (rt *Router) renameUser(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeErrCode(w, http.StatusBadRequest, "err.nameEmpty", "имя не может быть пустым")
		return
	}
	if err := rt.mgr.RenameUser(r.Context(), id, name); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) setUserEnabled(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetUserEnabled(r.Context(), id, req.Enabled); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// setUserNote replaces the operator's note on a user (empty clears it).
func (rt *Router) setUserNote(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Note string `json:"note"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetUserNote(r.Context(), id, req.Note); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// setUserTags replaces a user's tag list (empty clears it).
func (rt *Router) setUserTags(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Tags []string `json:"tags"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetUserTags(r.Context(), id, req.Tags); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// userTags lists every tag in use with how many users carry it, for the list
// page's filter and the tag editor's suggestions.
func (rt *Router) userTags(w http.ResponseWriter, _ *http.Request) {
	counts, err := rt.mgr.Store().AllUserTags()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tagCounts(counts))
}

// tagCount is one tag and how many users carry it.
type tagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// tagCounts turns the store's map into a sorted list: most used first, ties by
// name, so the filter dropdown reads the same on every load.
func tagCounts(counts map[string]int) []tagCount {
	out := make([]tagCount, 0, len(counts))
	for t, n := range counts {
		out = append(out, tagCount{Tag: t, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Tag < out[j].Tag
	})
	return out
}
