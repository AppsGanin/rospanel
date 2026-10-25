package server

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Alert rules (core/manager_alerts.go): the operator's thresholds on a server's
// figures, and the history of when they fired.

// alertsResp is the rules plus the metrics the form can offer.
type alertsResp struct {
	Rules   []model.AlertRule      `json:"rules"`
	Metrics []model.AlertMetricDef `json:"metrics"`
}

func (rt *Router) getAlerts(w http.ResponseWriter, _ *http.Request) {
	rules, err := rt.mgr.AlertRules()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, alertsResp{Rules: rules, Metrics: model.AlertMetricCatalog})
}

func (rt *Router) createAlert(w http.ResponseWriter, r *http.Request) {
	var in model.AlertRuleInput
	if !decodeJSON(w, r, &in) {
		return
	}
	rule, err := rt.mgr.SaveAlertRule(r.Context(), 0, in)
	if err != nil {
		writeAlertErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (rt *Router) updateAlert(w http.ResponseWriter, r *http.Request, id int64) {
	var in model.AlertRuleInput
	if !decodeJSON(w, r, &in) {
		return
	}
	rule, err := rt.mgr.SaveAlertRule(r.Context(), id, in)
	if err != nil {
		writeAlertErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (rt *Router) deleteAlert(w http.ResponseWriter, r *http.Request, id int64) {
	if err := rt.mgr.DeleteAlertRule(r.Context(), id); err != nil {
		writeAlertErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) alertEvents(w http.ResponseWriter, r *http.Request) {
	limit, before := alertPage(r)
	ev, err := rt.mgr.AlertEvents(limit, before)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": ev})
}

// alertPage reads the history cursor: a page size and the id to continue below.
func alertPage(r *http.Request) (limit int, before int64) {
	q := r.URL.Query()
	limit, _ = strconv.Atoi(q.Get("limit"))
	before, _ = strconv.ParseInt(q.Get("before"), 10, 64)
	return limit, before
}

// writeAlertErr answers a rule that does not exist with the panel's own code, and
// anything else the usual way.
func writeAlertErr(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeErrCode(w, http.StatusNotFound, "err.alertNotFound", "правило не найдено")
		return
	}
	writeManagerErr(w, err)
}

// The same over /v1.

func (rt *Router) apiListAlerts(w http.ResponseWriter, _ *http.Request) {
	rules, err := rt.mgr.AlertRules()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, rules)
}

func (rt *Router) apiAlertMetrics(w http.ResponseWriter, _ *http.Request) {
	writeAPIData(w, http.StatusOK, model.AlertMetricCatalog)
}

func (rt *Router) apiAlertEvents(w http.ResponseWriter, r *http.Request) {
	limit, before := alertPage(r)
	ev, err := rt.mgr.AlertEvents(limit, before)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, ev)
}

func (rt *Router) apiCreateAlert(w http.ResponseWriter, r *http.Request) {
	rt.apiSaveAlert(w, r, 0, http.StatusCreated)
}

func (rt *Router) apiUpdateAlert(w http.ResponseWriter, r *http.Request, id int64) {
	rt.apiSaveAlert(w, r, id, http.StatusOK)
}

func (rt *Router) apiSaveAlert(w http.ResponseWriter, r *http.Request, id int64, status int) {
	var in model.AlertRuleInput
	if !apiDecode(w, r, &in) {
		return
	}
	rule, err := rt.mgr.SaveAlertRule(r.Context(), id, in)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, status, rule)
}

func (rt *Router) apiDeleteAlert(w http.ResponseWriter, r *http.Request, id int64) {
	if err := rt.mgr.DeleteAlertRule(r.Context(), id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]bool{"ok": true})
}
