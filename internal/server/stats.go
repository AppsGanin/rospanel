package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// statsWindow resolves ?from=&to= (YYYY-MM-DD) into a range, defaulting to the last
// 30 days in the operator's configured timezone. The error it returns is an i18n
// code and its Russian default, because the two surfaces that call it render errors
// differently — the panel translates, the API answers in its envelope — and the
// alternative was the v1 API doing none of this at all: it passed the raw strings
// through, so an omitted `from` became `day BETWEEN '' AND ''` and the caller got an
// empty array for a question the panel answers on its own dashboard.
func (rt *Router) statsWindow(r *http.Request) (from, to string, errCode, errMsg string) {
	now := time.Now().In(rt.mgr.Location())
	to = r.URL.Query().Get("to")
	from = r.URL.Query().Get("from")
	if to == "" {
		to = now.Format("2006-01-02")
	} else if !validDate(to) {
		return "", "", "err.badTo", "неверный параметр to (ожидается YYYY-MM-DD)"
	}
	if from == "" {
		from = now.AddDate(0, 0, -29).Format("2006-01-02")
	} else if !validDate(from) {
		return "", "", "err.badFrom", "неверный параметр from (ожидается YYYY-MM-DD)"
	}
	if from > to { // lexicographic ordering is correct for zero-padded YYYY-MM-DD
		return "", "", "err.fromAfterTo", "from не может быть позже to"
	}
	return from, to, "", ""
}

// dateRange is statsWindow for the panel surface: on error it writes the response
// and returns ok=false.
func (rt *Router) dateRange(w http.ResponseWriter, r *http.Request) (from, to string, ok bool) {
	from, to, code, msg := rt.statsWindow(r)
	if code != "" {
		writeErrCode(w, http.StatusBadRequest, code, msg)
		return "", "", false
	}
	return from, to, true
}

// apiDateRange is statsWindow for the external API, which reports the same
// rejection in its own envelope. The English wording comes from the shared error
// catalog by key, like every other rejection on this surface.
func (rt *Router) apiDateRange(w http.ResponseWriter, r *http.Request) (from, to string, ok bool) {
	from, to, code, msg := rt.statsWindow(r)
	if code != "" {
		writeAPIRejected(w, code, msg, nil)
		return "", "", false
	}
	return from, to, true
}

func (rt *Router) statsSeries(w http.ResponseWriter, r *http.Request) {
	from, to, ok := rt.dateRange(w, r)
	if !ok {
		return
	}
	var userID int64
	if v := r.URL.Query().Get("user_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 0 {
			writeErrCode(w, http.StatusBadRequest, "err.badUserID", "неверный user_id")
			return
		}
		userID = id
	}
	pts, err := rt.mgr.StatsSeries(userID, from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if pts == nil {
		pts = []model.DailyPoint{}
	}
	writeJSON(w, http.StatusOK, pts)
}

// statsNodes splits the period's traffic by the server that carried it. user_id
// narrows it to one person (the user card); omitted, it covers everyone (the stats
// page). Server names are resolved server-side so the caller needs no node list —
// and an operator without rights to the Nodes tab still gets the breakdown.
func (rt *Router) statsNodes(w http.ResponseWriter, r *http.Request) {
	from, to, ok := rt.dateRange(w, r)
	if !ok {
		return
	}
	var userID int64
	if v := r.URL.Query().Get("user_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 0 {
			writeErrCode(w, http.StatusBadRequest, "err.badUserID", "неверный user_id")
			return
		}
		userID = id
	}
	rows, err := rt.mgr.NodeTrafficBreakdown(userID, from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rows == nil {
		rows = []core.NodeTraffic{}
	}
	writeJSON(w, http.StatusOK, rows)
}

func (rt *Router) statsByUser(w http.ResponseWriter, r *http.Request) {
	from, to, ok := rt.dateRange(w, r)
	if !ok {
		return
	}
	totals, err := rt.mgr.StatsByUser(from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if totals == nil {
		totals = []model.UserTotal{}
	}
	writeJSON(w, http.StatusOK, totals)
}

// statsCountries returns the geo breakdown of recent client connections — distinct
// source IPs per country — for the connection map. The window is the connection
// retention window (not the ?from/?to traffic range), since it reflects live sources.
func (rt *Router) statsCountries(w http.ResponseWriter, _ *http.Request) {
	countries, err := rt.mgr.ConnectionCountries()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if countries == nil {
		countries = []model.CountryStat{}
	}
	writeJSON(w, http.StatusOK, countries)
}

// sitesLimit reads ?limit=, clamped to a sane range. The view is a top-N by
// definition, so an out-of-range value is clamped rather than rejected — there is
// no correct answer to reject in favour of.
func sitesLimit(r *http.Request, def int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		return def
	}
	if n < 1 {
		return 1
	}
	if n > 200 {
		return 200
	}
	return n
}

// statsAbuse returns the fleet's recent blocklist matches, newest first.
func (rt *Router) statsAbuse(w http.ResponseWriter, r *http.Request) {
	rows, err := rt.mgr.RecentAbuse(sitesLimit(r, 50))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rows == nil {
		rows = []store.AbuseMatch{}
	}
	writeJSON(w, http.StatusOK, rows)
}

// userAbuse returns one user's blocklist matches, newest first.
func (rt *Router) userAbuse(w http.ResponseWriter, r *http.Request, id int64) {
	rows, err := rt.mgr.UserAbuse(id, sitesLimit(r, 20))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rows == nil {
		rows = []store.AbuseMatch{}
	}
	writeJSON(w, http.StatusOK, rows)
}

func (rt *Router) statsReset(w http.ResponseWriter, _ *http.Request) {
	if err := rt.mgr.ResetStats(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w)
}

func (rt *Router) setResetPeriod(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Period string `json:"period"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetResetPeriod(r.Context(), id, req.Period); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeOK(w)
}
