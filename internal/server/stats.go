package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/model"
)

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// dateRange reads ?from=&to= (YYYY-MM-DD), defaulting to the last 30 days in the
// operator's configured timezone. Malformed or reversed ranges get a 400 (rather
// than silently returning empty/garbage results); on error it returns ok=false
// after writing the response.
func (rt *Router) dateRange(w http.ResponseWriter, r *http.Request) (from, to string, ok bool) {
	now := time.Now().In(rt.mgr.Location())
	to = r.URL.Query().Get("to")
	from = r.URL.Query().Get("from")
	if to == "" {
		to = now.Format("2006-01-02")
	} else if !validDate(to) {
		writeErr(w, http.StatusBadRequest, "неверный параметр to (ожидается YYYY-MM-DD)")
		return "", "", false
	}
	if from == "" {
		from = now.AddDate(0, 0, -29).Format("2006-01-02")
	} else if !validDate(from) {
		writeErr(w, http.StatusBadRequest, "неверный параметр from (ожидается YYYY-MM-DD)")
		return "", "", false
	}
	if from > to { // lexicographic ordering is correct for zero-padded YYYY-MM-DD
		writeErr(w, http.StatusBadRequest, "from не может быть позже to")
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
			writeErr(w, http.StatusBadRequest, "неверный user_id")
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
			writeErr(w, http.StatusBadRequest, "неверный user_id")
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
