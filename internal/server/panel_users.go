package server

import (
	"net/http"
	"sort"
	"strings"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
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
		// HoldSeconds starts the term on the first connection instead of a date.
		HoldSeconds int64 `json:"hold_seconds"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErrCode(w, http.StatusBadRequest, "err.nameRequired2", "укажите имя")
		return
	}
	u, err := rt.mgr.CreateUserWithTerm(r.Context(), req.Name, req.DataLimit, req.ExpireAt, req.HoldSeconds)
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
	// The route opens to either permission; each action needs its own — deleting in
	// bulk needs what DELETE /api/users/{id} needs, the rest what the edits need.
	if !callerPerms(r).Has(bulkActionPerm(req.Action)) {
		writeErrCode(w, http.StatusForbidden, "err.forbidden", "недостаточно прав")
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
		DeviceLimit int   `json:"device_limit"`
		// ExpireAt is the end date (0 = none). Pointer, like everything about the term
		// below: a save that does not mention the term leaves it exactly as the store
		// has it, rather than posting back whatever the caller last read.
		ExpireAt *int64 `json:"expire_at"`
		// SpeedLimit is kbit/s (0 = unlimited). Pointer so a caller that doesn't
		// mention it leaves the cap alone — the bots and older integrations post this
		// body without it, and reading a missing field as 0 would silently lift
		// everyone's cap the first time they change a quota.
		SpeedLimit *int `json:"speed_limit"`
		// HoldSeconds is a term that starts on the first connection, in place of
		// expire_at. 0 takes a pending one away.
		HoldSeconds *int64 `json:"hold_seconds"`
		// SeenExpireAt and SeenHoldSeconds are the term the panel's form was opened on.
		// With them the term is changed only if it is still that one: a key picked up,
		// or a period paid for, while the card sat open is refused instead of being
		// written over.
		SeenExpireAt    *int64 `json:"seen_expire_at"`
		SeenHoldSeconds *int64 `json:"seen_hold_seconds"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.DeviceLimit < 0 {
		writeErrCode(w, http.StatusBadRequest, "err.deviceLimitNegative2", "лимит устройств не может быть отрицательным")
		return
	}
	expireAt, hold := int64(0), int64(0)
	if req.ExpireAt != nil {
		expireAt = *req.ExpireAt
	}
	if req.HoldSeconds != nil {
		hold = *req.HoldSeconds
	}
	// Judged before anything is written: a hold refused after the limits landed would
	// leave half of one save applied.
	if req.HoldSeconds != nil {
		if err := core.ValidateHold(hold); err != nil {
			writeManagerErr(w, err)
			return
		}
		if hold > 0 && expireAt > 0 {
			writeErrCode(w, http.StatusBadRequest, "err.holdWithExpiry", "либо дата окончания, либо срок с первого подключения — не оба сразу")
			return
		}
	}
	var err error
	switch {
	case req.ExpireAt == nil && req.HoldSeconds == nil:
		err = rt.mgr.SetUserQuota(r.Context(), id, req.DataLimit, req.DeviceLimit)
	case req.SeenExpireAt != nil && req.SeenHoldSeconds != nil:
		err = rt.mgr.SetUserLimitsSeen(r.Context(), id, req.DataLimit, expireAt, hold, req.DeviceLimit,
			*req.SeenExpireAt, *req.SeenHoldSeconds)
	default:
		// A caller that states a term without saying what it saw: applied as stated.
		if req.ExpireAt != nil {
			err = rt.mgr.SetUserLimits(r.Context(), id, req.DataLimit, expireAt, req.DeviceLimit)
		} else {
			err = rt.mgr.SetUserQuota(r.Context(), id, req.DataLimit, req.DeviceLimit)
		}
		if err == nil && req.HoldSeconds != nil {
			err = rt.mgr.SetUserHold(r.Context(), id, hold)
		}
	}
	if err != nil {
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

// userHappLink returns the user's subscription as an encrypted Happ link, "" while
// encrypted links are switched off. Asked for one user at a time, not carried in the
// user list: each link is an RSA-4096 encryption, and a list of thousands would pay
// for thousands of links nobody opened.
func (rt *Router) userHappLink(w http.ResponseWriter, _ *http.Request, id int64) {
	u, err := rt.mgr.Store().GetUser(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	link, err := sub.HappLink(set, u.SubToken)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"link": link})
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

func (rt *Router) userConnections(w http.ResponseWriter, r *http.Request, id int64) {
	conns, err := rt.mgr.Connections(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	// can_ban says whether the ban button belongs on the row: only for a caller who may
	// ban (the ban routes need security.manage), and only for an address a ban may touch — not a tunnel
	// address, a trusted one, a server's, or the caller's own.
	type connView struct {
		model.Connection
		CanBan bool `json:"can_ban"`
	}
	out := make([]connView, len(conns))
	var check func(string) error
	if callerPerms(r).Has(model.PermSecurityManage) && len(conns) > 0 {
		check = rt.mgr.BanChecker(clientIP(r))
	}
	for i, c := range conns {
		out[i] = connView{Connection: c, CanBan: check != nil && !c.Banned && check(c.IP) == nil}
	}
	writeJSON(w, http.StatusOK, out)
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
