package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin"
	"github.com/AppsGanin/rospanel/internal/plugin/builder"
	"github.com/AppsGanin/rospanel/internal/plugin/devkit"
	"github.com/AppsGanin/rospanel/internal/plugin/jsvm"
	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/version"
)

// Plugins written in the panel: drafts made in the rule builder or the code
// editor, checked, tested and tried in a sandbox, then installed through the same
// consent screen as any package — or downloaded to carry on elsewhere.
//
// A sandbox run is the author tools' harness (internal/plugin/devkit) on the
// panel's own compiled engine: a host of its own on a temporary directory, the
// internet and panel.api mocked unless a trial run asks for them. One runs at a
// time — the panel shares its one CPU with every subscription it serves.

const (
	maxDraftFiles = 200
	maxDraftBytes = manifest.MaxPackage
	sandboxWait   = 5 * time.Second
	testTimeout   = 60 * time.Second
	runTimeout    = 30 * time.Second
	maxSandboxOut = 64 << 10
)

var (
	draftPathRe = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+){0,4}$`)
	sandboxSlot = make(chan struct{}, 1)
)

// draftFile is one file as the editor moves it: text, or base64 for a binary one
// (a theme's images).
type draftFile struct {
	Path   string `json:"path"`
	Text   string `json:"text,omitempty"`
	Base64 string `json:"base64,omitempty"`
}

type draftView struct {
	Draft    model.PluginDraft `json:"draft"`
	Files    []draftFile       `json:"files"`
	Spec     *builder.Spec     `json:"spec,omitempty"`     // builder mode: the rules
	Problems []string          `json:"problems,omitempty"` // builder mode: why the rules made no plugin
	Events   []string          `json:"events"`             // what a rule may start on
	// Catalog: what each event carries — the fields a rule can read, with samples.
	Catalog builder.Catalog `json:"catalog"`
	// Installed: the plugin of this ID the panel runs, and whether it runs this very
	// code — the draft is a copy; edits reach the plugin only when applied.
	Installed *draftInstalled `json:"installed,omitempty"`
}

type draftInstalled struct {
	Version string `json:"version"`
	Applied bool   `json:"applied"` // the draft packs to the installed package
}

// draftView is viewDraft with what the panel runs of it.
func (rt *Router) draftView(d *model.PluginDraft, problems []string) draftView {
	v := viewDraft(d, problems)
	if rt.plugins == nil || d.PluginID == "" {
		return v
	}
	info, err := rt.plugins.Get(d.PluginID)
	if err != nil {
		return v
	}
	v.Installed = &draftInstalled{Version: info.Version}
	if raw, _, err := devkit.PackFiles(d.Files); err == nil {
		sum := sha256.Sum256(raw)
		v.Installed.Applied = hex.EncodeToString(sum[:]) == info.SHA256 || rt.sameFiles(d.PluginID, raw)
	}
	return v
}

// sameFiles: the package raw holds the very files of the installed one — which may
// have been zipped by another tool, so its bytes (and sha256) differ.
func (rt *Router) sameFiles(id string, raw []byte) bool {
	installed, err := rt.plugins.Package(id)
	if err != nil {
		return false
	}
	a, errA := manifest.Unpack(installed)
	b, errB := manifest.Unpack(raw)
	if errA != nil || errB != nil || len(a) != len(b) {
		return false
	}
	for name, body := range a {
		if other, ok := b[name]; !ok || !bytes.Equal(body, other) {
			return false
		}
	}
	return true
}

// draftItem is a draft as the plugins list shows it: beside the installed plugin of
// its id (whether that runs the draft's code), or on its own while not installed.
type draftItem struct {
	model.PluginDraft
	Installed bool `json:"installed"`
	Applied   bool `json:"applied"`
}

func (rt *Router) listPluginDrafts(w http.ResponseWriter, _ *http.Request) {
	ds, err := rt.mgr.Store().ListPluginDrafts()
	if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "err.internal", "внутренняя ошибка сервера")
		return
	}
	out := make([]draftItem, 0, len(ds))
	for _, d := range ds {
		item := draftItem{PluginDraft: d}
		if full, err := rt.mgr.Store().GetPluginDraft(d.ID); err == nil {
			if inst := rt.draftView(full, nil).Installed; inst != nil {
				item.Installed, item.Applied = true, inst.Applied
			}
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"drafts": out})
}

// pluginDraftTypes is rospanel.d.ts, for the editor's completion.
func (rt *Router) pluginDraftTypes(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(devkit.TypesFile())
}

// createPluginDraft starts a draft: {from: template|builder|installed, plugin_id},
// or a zip in the body (a package or the sources an earlier download gave).
func (rt *Router) createPluginDraft(w http.ResponseWriter, r *http.Request) {
	d := &model.PluginDraft{Mode: model.DraftCode, CreatedBy: adminName(r)}
	from := "zip"
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/zip") {
		r.Body = http.MaxBytesReader(w, r.Body, manifest.MaxPackage+1)
		raw, err := io.ReadAll(r.Body)
		if err != nil || len(raw) > manifest.MaxPackage {
			writeErrCode(w, http.StatusRequestEntityTooLarge, "err.pluginTooLarge", "пакет больше 5 МБ")
			return
		}
		files, err := manifest.Unpack(raw)
		if err != nil {
			writeErrDetail(w, http.StatusBadRequest, "err.pluginInvalid", "пакет не подходит: ", err.Error())
			return
		}
		d.Files = files
		if _, ok := files[builder.SpecFile]; ok {
			d.Mode = model.DraftBuilder
		}
	} else {
		var req struct {
			From     string `json:"from"`
			PluginID string `json:"plugin_id"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		from = req.From
		var err error
		switch req.From {
		case "template":
			d.Files, err = devkit.TemplateFiles(req.PluginID, version.Version)
		case "builder":
			d.Mode = model.DraftBuilder
			d.Files, err = builder.Compile(starterSpec(req.PluginID), version.Version)
		case "installed":
			var raw []byte
			if rt.plugins == nil {
				err = plugin.ErrNotFound
			} else if raw, err = rt.plugins.Package(req.PluginID); err == nil {
				d.Files, err = manifest.Unpack(raw)
			}
		default:
			err = errors.New("from: template, builder or installed")
		}
		if err != nil {
			writeErrDetail(w, http.StatusBadRequest, "err.pluginInvalid", "пакет не подходит: ", err.Error())
			return
		}
	}
	if problem := checkDraftFiles(d.Files); problem != "" {
		writeErrDetail(w, http.StatusBadRequest, "err.pluginInvalid", "пакет не подходит: ", problem)
		return
	}
	describeDraft(d)
	// One draft per plugin: opening an installed plugin again goes back to its draft,
	// and a second draft under the same id is refused rather than piling up.
	if d.PluginID != "" {
		existing, err := rt.mgr.Store().PluginDraftFor(d.PluginID)
		if err != nil {
			writeErrCode(w, http.StatusInternalServerError, "err.internal", "внутренняя ошибка сервера")
			return
		}
		if existing != 0 {
			if from == "installed" {
				rt.getPluginDraft(w, r, existing)
				return
			}
			writeErrDetail(w, http.StatusConflict, "err.draftExists", "черновик этого плагина уже есть — откройте его в списке: ", d.PluginID)
			return
		}
	}
	err := rt.mgr.Store().SavePluginDraft(d)
	if errors.Is(err, store.ErrDraftExists) {
		// Made by a request racing this one (a double click).
		if existing, _ := rt.mgr.Store().PluginDraftFor(d.PluginID); existing != 0 && from == "installed" {
			rt.getPluginDraft(w, r, existing)
			return
		}
		writeErrDetail(w, http.StatusConflict, "err.draftExists", "черновик этого плагина уже есть — откройте его в списке: ", d.PluginID)
		return
	}
	if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "err.internal", "внутренняя ошибка сервера")
		return
	}
	writeJSON(w, http.StatusOK, rt.draftView(d, nil))
}

// starterSpec is the builder's first rule: a log line for each new user.
func starterSpec(id string) builder.Spec {
	return builder.Spec{ID: id, Version: "1.0.0", Name: titleOf(id), Rules: []builder.Rule{{
		Event: model.WebhookUserCreated, Actions: []builder.Action{{Type: "log", Text: "New user: {{user.name}}"}},
	}}}
}

func titleOf(id string) string {
	if id == "" {
		return ""
	}
	return strings.ToUpper(id[:1]) + strings.ReplaceAll(id[1:], "-", " ")
}

func (rt *Router) getPluginDraft(w http.ResponseWriter, _ *http.Request, id int64) {
	d, ok := rt.loadDraft(w, id)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, rt.draftView(d, nil))
}

func (rt *Router) loadDraft(w http.ResponseWriter, id int64) (*model.PluginDraft, bool) {
	d, err := rt.mgr.Store().GetPluginDraft(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErrCode(w, http.StatusNotFound, "err.draftNotFound", "черновик не найден")
		return nil, false
	}
	if err != nil {
		writeErrDetail(w, http.StatusInternalServerError, "err.draftUnreadable", "черновик не читается: ", err.Error())
		return nil, false
	}
	return d, true
}

// savePluginDraft stores an edit: {spec} in the builder (the files are made from
// it; rules that do not hold are kept and the problems answered), {files} in the
// code editor (the whole set), {mode: "code"} to leave the builder for good.
func (rt *Router) savePluginDraft(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Name  *string       `json:"name"`
		Spec  *builder.Spec `json:"spec"`
		Files []draftFile   `json:"files"`
		Mode  string        `json:"mode"`
	}
	if !decodeJSONLimit(w, r, &req, 3*maxDraftBytes) {
		return
	}
	// The edit replaces what is there; should another write (a version bump on
	// apply) land between the read and the write, it is read again and redone.
	for range 3 {
		d, ok := rt.loadDraft(w, id)
		if !ok {
			return
		}
		problems, done := rt.applyDraftEdit(w, d, req.Name, req.Spec, req.Files, req.Mode)
		if done {
			return
		}
		err := rt.mgr.Store().SavePluginDraft(d)
		switch {
		case errors.Is(err, store.ErrDraftChanged):
			continue
		case errors.Is(err, store.ErrDraftExists):
			writeErrDetail(w, http.StatusConflict, "err.draftExists", "черновик этого плагина уже есть — откройте его в списке: ", d.PluginID)
		case err != nil:
			writeErrCode(w, http.StatusInternalServerError, "err.internal", "внутренняя ошибка сервера")
		default:
			writeJSON(w, http.StatusOK, rt.draftView(d, problems))
		}
		return
	}
	writeErrCode(w, http.StatusConflict, "err.draftChanged", "черновик только что изменился — повторите")
}

// applyDraftEdit puts an edit into the draft read; done when it answered already
// (the edit does not fit the draft).
func (rt *Router) applyDraftEdit(w http.ResponseWriter, d *model.PluginDraft, name *string, spec *builder.Spec, in []draftFile, mode string) (problems []string, done bool) {
	if name != nil {
		d.Name = strings.TrimSpace(*name)
	}
	switch {
	case spec != nil:
		if d.Mode != model.DraftBuilder {
			writeErrCode(w, http.StatusConflict, "err.draftNotBuilder", "этот черновик редактируется кодом")
			return nil, true
		}
		files, err := builder.Compile(*spec, version.Version)
		var pr manifest.Problems
		switch {
		case errors.As(err, &pr):
			// Kept as they are, half-made rules included: the operator carries on.
			// The files made from the last rules that held stay, but nothing runs
			// or installs them while these have problems (rulesProblems).
			problems = pr
			raw, _ := json.MarshalIndent(spec, "", "  ")
			d.Files[builder.SpecFile] = append(raw, '\n')
		case err != nil:
			writeErrDetail(w, http.StatusBadRequest, "err.pluginInvalid", "пакет не подходит: ", err.Error())
			return nil, true
		default:
			// The settings a trial run uses stay the operator's: new keys are added.
			if cur, ok := d.Files["dev.config.json"]; ok {
				files["dev.config.json"] = mergeDevConfig(cur, files["dev.config.json"])
			}
			d.Files = files
		}
	case in != nil:
		if d.Mode == model.DraftBuilder {
			writeErrCode(w, http.StatusConflict, "err.draftBuilder", "этот черновик собирается конструктором")
			return nil, true
		}
		files, problem := filesFrom(in)
		if problem != "" {
			writeErrDetail(w, http.StatusBadRequest, "err.pluginInvalid", "пакет не подходит: ", problem)
			return nil, true
		}
		d.Files = files
	case mode == model.DraftCode:
		d.Mode = model.DraftCode
		delete(d.Files, builder.SpecFile)
	}
	describeDraft(d)
	return problems, false
}

// rulesProblems is why a builder draft's rules make no plugin: its files are then
// those of the last rules that held, and must not be checked, run or installed as
// if they were what the operator sees.
func rulesProblems(d *model.PluginDraft) []string {
	if d.Mode != model.DraftBuilder {
		return nil
	}
	var s builder.Spec
	if err := json.Unmarshal(d.Files[builder.SpecFile], &s); err != nil {
		return []string{builder.SpecFile + ": " + err.Error()}
	}
	return builder.Validate(s)
}

// rulesHold answers the rules' problems, if any; true when there are none.
func rulesHold(w http.ResponseWriter, d *model.PluginDraft) bool {
	p := rulesProblems(d)
	if len(p) == 0 {
		return true
	}
	writeErrDetail(w, http.StatusBadRequest, "err.draftRules", "в правилах есть ошибки — исправьте их в конструкторе: ", strings.Join(p, "; "))
	return false
}

func (rt *Router) deletePluginDraft(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.Store().DeletePluginDraft(id); err != nil {
		writeErrCode(w, http.StatusInternalServerError, "err.internal", "внутренняя ошибка сервера")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// checkPluginDraft packs the draft and reads it as the install would.
func (rt *Router) checkPluginDraft(w http.ResponseWriter, r *http.Request, id int64) {
	d, ok := rt.loadDraft(w, id)
	if !ok {
		return
	}
	if p := rulesProblems(d); len(p) > 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "problems": p})
		return
	}
	raw, skipped, err := devkit.PackFiles(d.Files)
	if err != nil {
		writeErrDetail(w, http.StatusBadRequest, "err.pluginInvalid", "пакет не подходит: ", err.Error())
		return
	}
	out := map[string]any{"ok": true, "skipped": skipped, "size": len(raw)}
	pkg, err := manifest.Read(raw, version.Version)
	var pr manifest.Problems
	switch {
	case errors.As(err, &pr):
		out["ok"], out["problems"] = false, []string(pr)
	case err != nil:
		out["ok"], out["problems"] = false, []string{err.Error()}
	default:
		out["manifest"], out["exports"] = pkg.Manifest, pkg.Manifest.Exports()
		// The manifest holds; now the code, loaded as a start would load it — in the
		// one sandbox slot, as tests and runs are: it is the draft's own code.
		if rt.plugins != nil {
			if !takeSandbox(w, r) {
				return
			}
			defer func() { <-sandboxSlot }()
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), runTimeout)
			defer cancel()
			if err := rt.plugins.Probe(ctx, raw); err != nil {
				out["ok"], out["problems"] = false, []string{err.Error()}
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// testPluginDraft runs the draft's test.js in the sandbox.
func (rt *Router) testPluginDraft(w http.ResponseWriter, r *http.Request, id int64) {
	d, ok := rt.loadDraft(w, id)
	if !ok || !rulesHold(w, d) {
		return
	}
	if !takeSandbox(w, r) {
		return
	}
	defer func() { <-sandboxSlot }()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), testTimeout)
	defer cancel()
	out := &capped{max: maxSandboxOut}
	results, err := devkit.RunTestFiles(ctx, d.Files, devkit.TestOptions{
		PanelVersion: version.Version, Out: out, Engine: rt.sandboxEngine(), NoReport: true,
	})
	resp := map[string]any{"results": results, "output": out.String()}
	if results == nil {
		resp["results"] = []devkit.TestResult{}
	}
	if err != nil {
		resp["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// runPluginDraft tries the draft once: an event to onEvent, or a call of an
// export. Mocked by default; real_http sends its requests out (to its allowlist,
// through the same guarded fetcher a plugin has), real_api lets panel.api read
// the panel — reads only, and only what both the draft asks and the admin holds.
func (rt *Router) runPluginDraft(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Event    string          `json:"event"`
		Data     json.RawMessage `json:"data"`
		Export   string          `json:"export"`
		Arg      json.RawMessage `json:"arg"`
		RealHTTP bool            `json:"real_http"`
		RealAPI  bool            `json:"real_api"`
	}
	if !decodeJSONLimit(w, r, &req, 1<<20) {
		return
	}
	d, ok := rt.loadDraft(w, id)
	if !ok || !rulesHold(w, d) {
		return
	}
	raw, _, err := devkit.PackFiles(d.Files)
	if err != nil {
		writeErrDetail(w, http.StatusBadRequest, "err.pluginInvalid", "пакет не подходит: ", err.Error())
		return
	}
	var cfg devkit.DevConfig
	if b, ok := d.Files["dev.config.json"]; ok {
		_ = json.Unmarshal(b, &cfg)
	}
	if !takeSandbox(w, r) {
		return
	}
	defer func() { <-sandboxSlot }()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), runTimeout)
	defer cancel()
	opts := devkit.Options{Settings: cfg.Settings, PanelVersion: version.Version, Engine: rt.sandboxEngine()}
	if req.RealAPI {
		opts.API = rt.draftAPI(r, d)
	}
	h, err := devkit.Start(ctx, raw, opts)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error(), "logs": []plugin.LogLine{}, "calls": []devkit.Call{}})
		return
	}
	defer h.Close()
	h.RealHTTP = req.RealHTTP
	var res json.RawMessage
	switch {
	case req.Event != "":
		res, err = h.Event(ctx, req.Event, req.Data)
	case req.Export != "":
		var arg any
		if len(req.Arg) > 0 {
			arg = req.Arg
		}
		res, err = h.Call(ctx, req.Export, arg)
	default:
		err = errors.New("choose an event or an export to call")
	}
	out := map[string]any{"result": res, "logs": h.Logs(), "calls": h.Calls()}
	if err != nil {
		out["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

// draftAPI answers a trial run's panel.api: GET only, with the permissions both the
// draft asks for and the admin running it holds.
func (rt *Router) draftAPI(r *http.Request, d *model.PluginDraft) plugin.APICaller {
	held := callerPerms(r)
	var asked []string
	var m struct {
		Permissions []string `json:"permissions"`
	}
	_ = json.Unmarshal(d.Files["plugin.json"], &m)
	for p := range model.NewPermSet(m.Permissions) {
		if held.Has(p) {
			asked = append(asked, p)
		}
	}
	return func(ctx context.Context, req plugin.APIRequest) (int, []byte, error) {
		if req.Method != http.MethodGet {
			return 0, nil, errors.New("a trial run only reads the panel: panel.api " + req.Method + " is mocked in tests")
		}
		req.Plugin, req.Perms, req.ReadOnly = fmt.Sprintf("draft-%d", d.ID), asked, true
		return rt.PluginAPI(ctx, req)
	}
}

// downloadPluginDraft gives the package (?kind=package) or every source file.
func (rt *Router) downloadPluginDraft(w http.ResponseWriter, r *http.Request, id int64) {
	d, ok := rt.loadDraft(w, id)
	if !ok {
		return
	}
	name := d.PluginID
	if name == "" {
		name = fmt.Sprintf("draft-%d", d.ID)
	}
	var raw []byte
	var err error
	if r.URL.Query().Get("kind") == "package" {
		if !rulesHold(w, d) {
			return
		}
		raw, _, err = devkit.PackFiles(d.Files)
		name += "-" + d.Version + ".zip"
	} else {
		// The sources travel: the secrets a trial run used stay behind.
		raw, err = devkit.ZipFiles(withoutSecrets(d.Files))
		name += "-src.zip"
	}
	if err != nil {
		writeErrDetail(w, http.StatusBadRequest, "err.pluginInvalid", "пакет не подходит: ", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, name))
	_, _ = w.Write(raw)
}

// inspectPluginDraft packs the draft for the consent screen: the install or update
// that follows is the ordinary one, password and permissions included.
func (rt *Router) inspectPluginDraft(w http.ResponseWriter, _ *http.Request, id int64) {
	h := rt.pluginHost(w)
	if h == nil {
		return
	}
	d, ok := rt.loadDraft(w, id)
	if !ok || !rulesHold(w, d) {
		return
	}
	// Applied over the installed plugin, the draft takes a version above it: the card
	// and a rollback then tell the two apart.
	bumped := ""
	if info, err := h.Get(d.PluginID); err == nil && !versionAbove(d.Version, info.Version) {
		next := bumpPatch(info.Version)
		if err := setDraftVersion(d, next); err != nil {
			writeErrDetail(w, http.StatusBadRequest, "err.pluginInvalid", "пакет не подходит: ", err.Error())
			return
		}
		describeDraft(d)
		// Saved only over what was read: an edit that came in meanwhile is not lost.
		err := rt.mgr.Store().SavePluginDraft(d)
		if errors.Is(err, store.ErrDraftChanged) {
			writeErrCode(w, http.StatusConflict, "err.draftChanged", "черновик только что изменился — повторите")
			return
		}
		if err != nil {
			writeErrCode(w, http.StatusInternalServerError, "err.internal", "внутренняя ошибка сервера")
			return
		}
		bumped = next
	}
	raw, _, err := devkit.PackFiles(d.Files)
	if err != nil {
		writeErrDetail(w, http.StatusBadRequest, "err.pluginInvalid", "пакет не подходит: ", err.Error())
		return
	}
	rt.respondInspectionBumped(w, h, raw, bumped)
}

// versionAbove reports whether X.Y.Z a is above b; an unreadable one is not.
func versionAbove(a, b string) bool {
	pa, okA := semver(a)
	pb, okB := semver(b)
	if !okA || !okB {
		return false
	}
	for i := range 3 {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func semver(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// bumpPatch is the version after v: 1.2.3 → 1.2.4 (an unreadable one → 0.0.1).
func bumpPatch(v string) string {
	p, ok := semver(v)
	if !ok {
		return "0.0.1"
	}
	return fmt.Sprintf("%d.%d.%d", p[0], p[1], p[2]+1)
}

// versionRe finds plugin.json's version to rewrite it in place, keeping the rest of
// the file as its author wrote it.
var versionRe = regexp.MustCompile(`("version"\s*:\s*")[^"]*(")`)

// setDraftVersion puts a version into the draft: the builder's rules (and the files
// they make), or plugin.json as written.
func setDraftVersion(d *model.PluginDraft, v string) error {
	if d.Mode == model.DraftBuilder {
		var spec builder.Spec
		if err := json.Unmarshal(d.Files[builder.SpecFile], &spec); err != nil {
			return err
		}
		spec.Version = v
		files, err := builder.Compile(spec, version.Version)
		if err != nil {
			return err
		}
		if cur, ok := d.Files["dev.config.json"]; ok {
			files["dev.config.json"] = mergeDevConfig(cur, files["dev.config.json"])
		}
		d.Files = files
		return nil
	}
	mf, ok := d.Files["plugin.json"]
	if !ok {
		return errors.New("plugin.json is missing")
	}
	loc := versionRe.FindSubmatchIndex(mf)
	if loc == nil {
		return errors.New(`plugin.json has no "version"`)
	}
	out := append([]byte{}, mf[:loc[3]]...)
	out = append(out, v...)
	out = append(out, mf[loc[4]:]...)
	d.Files["plugin.json"] = out
	return nil
}

// sandboxEngine is the panel's engine when plugins run here: the guest is already
// compiled, which on a small box is a second saved per run.
func (rt *Router) sandboxEngine() *jsvm.Engine {
	if rt.plugins == nil {
		return nil
	}
	return rt.plugins.Engine()
}

// takeSandbox waits briefly for the one sandbox slot.
func takeSandbox(w http.ResponseWriter, r *http.Request) bool {
	t := time.NewTimer(sandboxWait)
	defer t.Stop()
	select {
	case sandboxSlot <- struct{}{}:
		return true
	case <-t.C:
	case <-r.Context().Done():
	}
	writeErrCode(w, http.StatusTooManyRequests, "err.sandboxBusy", "песочница занята другим запуском — повторите через несколько секунд")
	return false
}

// --- files ---

func viewDraft(d *model.PluginDraft, problems []string) draftView {
	v := draftView{Draft: *d, Problems: problems, Files: []draftFile{}, Events: model.WebhookEventCatalog,
		Catalog: builder.EventCatalog()}
	names := make([]string, 0, len(d.Files))
	for p := range d.Files {
		names = append(names, p)
	}
	slices.Sort(names)
	for _, p := range names {
		b := d.Files[p]
		if utf8.Valid(b) && !bytes.ContainsRune(b, 0) {
			v.Files = append(v.Files, draftFile{Path: p, Text: string(b)})
		} else {
			v.Files = append(v.Files, draftFile{Path: p, Base64: base64.StdEncoding.EncodeToString(b)})
		}
	}
	if d.Mode == model.DraftBuilder {
		var s builder.Spec
		if json.Unmarshal(d.Files[builder.SpecFile], &s) == nil {
			v.Spec = &s
		}
	}
	return v
}

func filesFrom(in []draftFile) (devkit.Files, string) {
	out := devkit.Files{}
	for _, f := range in {
		if _, dup := out[f.Path]; dup {
			return nil, f.Path + " appears twice"
		}
		if f.Base64 != "" {
			b, err := base64.StdEncoding.DecodeString(f.Base64)
			if err != nil {
				return nil, f.Path + ": bad base64"
			}
			out[f.Path] = b
		} else {
			out[f.Path] = []byte(f.Text)
		}
	}
	return out, checkDraftFiles(out)
}

func checkDraftFiles(files devkit.Files) string {
	if len(files) > maxDraftFiles {
		return fmt.Sprintf("at most %d files", maxDraftFiles)
	}
	total := 0
	for p, b := range files {
		if !draftPathRe.MatchString(p) || strings.Contains(p, "..") {
			return fmt.Sprintf("%q: a path of a-z, 0-9, . _ - and /", p)
		}
		total += len(b)
	}
	if total > maxDraftBytes {
		return fmt.Sprintf("larger than %d MB together", maxDraftBytes>>20)
	}
	return ""
}

// describeDraft reads the plugin's id, version and name from its plugin.json for
// the list.
func describeDraft(d *model.PluginDraft) {
	var m struct {
		ID      string        `json:"id"`
		Version string        `json:"version"`
		Name    manifest.Text `json:"name"`
	}
	_ = json.Unmarshal(d.Files["plugin.json"], &m)
	d.PluginID, d.Version = m.ID, m.Version
	if name := strings.TrimSpace(m.Name.Get("")); name != "" {
		d.Name = name
	} else if d.Name == "" {
		d.Name = m.ID
	}
}

// withoutSecrets is the files with the values of the plugin's secret settings
// blanked in dev.config.json — what a trial run used may be a real token.
func withoutSecrets(files devkit.Files) devkit.Files {
	raw, ok := files["dev.config.json"]
	if !ok {
		return files
	}
	var m struct {
		Settings []struct {
			Key  string `json:"key"`
			Kind string `json:"kind"`
		} `json:"settings"`
	}
	_ = json.Unmarshal(files["plugin.json"], &m)
	var cfg map[string]any
	if json.Unmarshal(raw, &cfg) != nil {
		// Not readable, so not sorted out: it stays behind whole.
		out := maps.Clone(files)
		delete(out, "dev.config.json")
		return out
	}
	settings, _ := cfg["settings"].(map[string]any)
	for _, f := range m.Settings {
		if _, set := settings[f.Key]; set && f.Kind == "secret" {
			settings[f.Key] = ""
		}
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return files
	}
	out := maps.Clone(files)
	out["dev.config.json"] = append(b, '\n')
	return out
}

// mergeDevConfig keeps the operator's trial values for the settings the rules still
// have; a setting the rules no longer need goes, a new one comes with its sample.
func mergeDevConfig(cur, next []byte) []byte {
	var a, b devkit.DevConfig
	if json.Unmarshal(cur, &a) != nil || a.Settings == nil || json.Unmarshal(next, &b) != nil {
		return next
	}
	for k := range b.Settings {
		if v, ok := a.Settings[k]; ok {
			b.Settings[k] = v
		}
	}
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return next
	}
	return append(out, '\n')
}

// capped keeps the first max bytes written to it.
type capped struct {
	bytes.Buffer
	max int
}

func (c *capped) Write(b []byte) (int, error) {
	if room := c.max - c.Len(); room > 0 {
		c.Buffer.Write(b[:min(len(b), room)])
	}
	return len(b), nil
}

func adminName(r *http.Request) string {
	if a, ok := sessionAdminFrom(r.Context()); ok {
		return a.Username
	}
	return ""
}
