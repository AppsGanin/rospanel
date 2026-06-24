package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/msTimofeev/rospanel/internal/model"
)

// dateRange reads ?from=&to= (YYYY-MM-DD), defaulting to the last 30 days in the
// operator's configured timezone.
func (rt *Router) dateRange(r *http.Request) (from, to string) {
	to = r.URL.Query().Get("to")
	from = r.URL.Query().Get("from")
	now := time.Now().In(rt.mgr.Location())
	if to == "" {
		to = now.Format("2006-01-02")
	}
	if from == "" {
		from = now.AddDate(0, 0, -29).Format("2006-01-02")
	}
	return from, to
}

func (rt *Router) statsSeries(w http.ResponseWriter, r *http.Request) {
	from, to := rt.dateRange(r)
	var userID int64
	if v := r.URL.Query().Get("user_id"); v != "" {
		userID, _ = strconv.ParseInt(v, 10, 64)
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

func (rt *Router) statsByUser(w http.ResponseWriter, r *http.Request) {
	from, to := rt.dateRange(r)
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
	if err := rt.mgr.SetResetPeriod(id, req.Period); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeOK(w)
}
