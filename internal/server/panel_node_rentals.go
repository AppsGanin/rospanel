package server

import (
	"net/http"
	"strings"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

type nodeRentalResp struct {
	Settings              model.NodeRentalSettings `json:"settings"`
	Tenants               []model.NodeTenant       `json:"tenants"`
	AllocatedSpeedLimit   int                      `json:"allocated_speed_limit"`
	AllocatedQuotaPercent int                      `json:"allocated_quota_percent"`
	ShareLink             string                   `json:"share_link,omitempty"`
}

type importRentedReq struct {
	ShareLink string `json:"share_link"`
	Name      string `json:"name"`
}

func (rt *Router) nodeRentalSettings(w http.ResponseWriter, _ *http.Request, id int64) {
	settings, err := rt.mgr.GetNodeRentalSettings(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	tenants, err := rt.mgr.ListNodeTenants(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	quota, speed, _ := rt.mgr.CalculateTenantResourceShare(id)

	var shareLink string
	if settings.ShareEnabled {
		shareLink, _ = rt.mgr.GenerateNodeShareLink(id)
	}

	writeJSON(w, http.StatusOK, nodeRentalResp{
		Settings:              *settings,
		Tenants:               tenants,
		AllocatedSpeedLimit:   speed,
		AllocatedQuotaPercent: quota,
		ShareLink:             shareLink,
	})
}

func (rt *Router) saveNodeRentalSettings(w http.ResponseWriter, r *http.Request, id int64) {
	var req model.NodeRentalSettings
	if !decodeJSON(w, r, &req) {
		return
	}
	updated, err := rt.mgr.UpdateNodeRentalSettings(id, req)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (rt *Router) getNodeShareLink(w http.ResponseWriter, _ *http.Request, id int64) {
	link, err := rt.mgr.GenerateNodeShareLink(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"share_link": link})
}

func (rt *Router) importRentedNode(w http.ResponseWriter, r *http.Request) {
	var req importRentedReq
	if !decodeJSON(w, r, &req) {
		return
	}
	req.ShareLink = strings.TrimSpace(req.ShareLink)
	if req.ShareLink == "" {
		writeErrCode(w, http.StatusBadRequest, "err.invalidShareLink", "неверная или повреждённая ссылка шеринга ноды")
		return
	}
	node, err := rt.mgr.ImportRentedNode(req.ShareLink, req.Name)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, node)
}

func (rt *Router) nodeReservedPorts(w http.ResponseWriter, _ *http.Request, id int64) {
	ports, err := rt.mgr.GetNodeReservedPorts(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if ports == nil {
		ports = []model.PortInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ports": ports})
}

func (rt *Router) deleteNodeTenant(w http.ResponseWriter, r *http.Request, id int64) {
	tenantID := r.PathValue("tenantId")
	if tenantID == "" {
		writeErrCode(w, http.StatusBadRequest, "err.tenantNotFound", "арендатор не найден")
		return
	}
	if err := rt.mgr.DeleteNodeTenant(id, tenantID); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (rt *Router) handleRentalSync(w http.ResponseWriter, r *http.Request) {
	var req model.NodeRentalSyncReq
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := rt.mgr.ProcessRentalSync(req)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

