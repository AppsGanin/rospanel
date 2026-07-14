package server

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/provision"
)

// provisionReq carries the SSH credentials used to install a node remotely. They
// are used only for the duration of the install and never persisted.
type provisionReq struct {
	SSHHost     string `json:"ssh_host"`
	SSHPort     int    `json:"ssh_port"`
	SSHUser     string `json:"ssh_user"`
	SSHPassword string `json:"ssh_password"`
	SSHKey      string `json:"ssh_key"`
	SSHKeyPass  string `json:"ssh_key_passphrase"`
}

// provisionNode installs an already-created node onto a remote server over SSH,
// streaming the install log back over SSE. It mints a fresh join token for the
// install so the operator never has to copy one. The node appears online in the
// panel once its agent connects.
func (rt *Router) provisionNode(w http.ResponseWriter, r *http.Request, id int64) {
	var req provisionReq
	if !decodeJSON(w, r, &req) {
		return
	}
	req.SSHHost = strings.TrimSpace(req.SSHHost)
	req.SSHUser = strings.TrimSpace(req.SSHUser)
	if req.SSHHost == "" || req.SSHUser == "" {
		writeErr(w, http.StatusBadRequest, "укажите адрес сервера и SSH-пользователя")
		return
	}
	if req.SSHPassword == "" && req.SSHKey == "" {
		writeErr(w, http.StatusBadRequest, "укажите SSH-пароль или приватный ключ")
		return
	}
	node, err := rt.mgr.GetNode(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if node == nil {
		writeErr(w, http.StatusNotFound, "нода не найдена")
		return
	}

	// The panel installs the node with its OWN binary (uploaded over SSH), not a
	// GitHub download — so the node runs the exact same build, which is the only way
	// this works for an unreleased build and avoids version skew.
	self, err := os.Executable()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "не удалось определить путь к бинарю панели")
		return
	}

	// Fresh install token for this run.
	token, err := rt.mgr.RegenJoinToken(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	set, _ := rt.mgr.Store().GetSettings()
	nodePath := ""
	if set != nil {
		nodePath = set.NodeAPIPath
	}
	joinURL := panelPublicURL(r) + "/" + nodePath + "/" + nodeapi.PathPrefix + "/join#" + token
	installArgs := []string{"install", "--join", joinURL}
	if !rt.mgr.HasValidCert() {
		installArgs = append(installArgs, "--insecure") // panel cert isn't CA-valid
	}

	ip := clientIP(r)
	if !rt.streams.acquire(ip) {
		writeErr(w, http.StatusTooManyRequests, "слишком много активных потоков")
		return
	}
	defer rt.streams.release(ip)
	flusher, ok := sseStart(w)
	if !ok {
		return
	}

	// The install (binary upload + node install + ACME) can take a couple of minutes;
	// give it room, but bail if the operator closes the page (request context cancels).
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Minute)
	defer cancel()

	creds := provision.Credentials{
		Host:       req.SSHHost,
		Port:       req.SSHPort,
		User:       req.SSHUser,
		Password:   req.SSHPassword,
		PrivateKey: req.SSHKey,
		Passphrase: req.SSHKeyPass,
	}
	// The install streams stdout and stderr from two goroutines; serialize the SSE
	// writes so the two never touch the ResponseWriter concurrently (it isn't safe
	// for concurrent use).
	var emitMu sync.Mutex
	emit := func(line string) {
		emitMu.Lock()
		sseSend(w, flusher, line)
		emitMu.Unlock()
	}

	_, err = provision.Install(ctx, creds, self, installArgs, emit)
	if err != nil {
		emit("ОШИБКА: " + err.Error())
		emit("event:error")
		return
	}
	// Give the freshly-started agent a moment; the UI polls the node list for online.
	time.Sleep(500 * time.Millisecond)
	emit("Установка завершена. Нода подключится к панели в течение минуты.")
	emit("event:done")
}
