package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/plugin/jsvm"
)

const (
	maxAPIBody     = 1 << 20
	maxAPIResponse = 4 << 20
	maxLogLine     = 4 << 10
)

// hostFunc binds the plugin's __host calls to this instance. It runs inside a
// call, with inst.mu held by that call — so it must not take inst.mu.
func (inst *instance) hostFunc(o callOpts) jsvm.Host {
	return func(ctx context.Context, op string, arg []byte) ([]byte, error) {
		res, err := inst.op(ctx, op, arg, o)
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	}
}

func decode[T any](arg []byte) (T, error) {
	var v T
	if err := json.Unmarshal(arg, &v); err != nil {
		return v, fmt.Errorf("bad arguments: %w", err)
	}
	return v, nil
}

// op answers one host operation. Each returns a JSON-able value; an error is
// thrown in the plugin.
func (inst *instance) op(ctx context.Context, op string, arg []byte, o callOpts) (any, error) {
	switch op {
	case "plugin.info":
		return map[string]string{"id": inst.id, "version": inst.rec.Version}, nil
	case "config.get":
		cfg := map[string]string{}
		for k, v := range inst.rec.Config {
			cfg[k] = v
		}
		return cfg, nil
	case "log":
		a, err := decode[struct {
			Level  string          `json:"level"`
			Msg    string          `json:"msg"`
			Fields json.RawMessage `json:"fields"`
		}](arg)
		if err != nil {
			return nil, err
		}
		msg := a.Msg
		if len(a.Fields) > 0 && string(a.Fields) != "null" {
			msg += " " + string(a.Fields)
		}
		inst.logf(a.Level, "%s", msg)
		return nil, nil
	case "t":
		a, err := decode[struct {
			Key    string            `json:"key"`
			Params map[string]string `json:"params"`
			Lang   string            `json:"lang"`
		}](arg)
		if err != nil {
			return nil, err
		}
		lang := a.Lang
		if lang == "" {
			lang = o.lang
		}
		return inst.pkg.Translate(lang, a.Key, a.Params), nil
	case "api":
		return inst.opAPI(ctx, arg, o)
	case "http.fetch":
		return inst.opFetch(ctx, arg)
	}
	if strings.HasPrefix(op, "kv.") || strings.HasPrefix(op, "db.") {
		return inst.opDB(ctx, op, arg)
	}
	if strings.HasPrefix(op, "crypto.") {
		return cryptoOp(op, arg)
	}
	return nil, fmt.Errorf("unknown operation %q", op)
}

func (inst *instance) opDB(ctx context.Context, op string, arg []byte) (any, error) {
	db := inst.db
	if db == nil {
		return nil, errors.New("the plugin's database is not open")
	}
	type kvArg struct {
		Key    string `json:"key"`
		Value  string `json:"value"`
		Prefix string `json:"prefix"`
		After  string `json:"after"`
		Limit  int    `json:"limit"`
	}
	type sqlArg struct {
		SQL  string `json:"sql"`
		Args []any  `json:"args"`
	}
	switch op {
	case "kv.get":
		a, err := decode[kvArg](arg)
		if err != nil {
			return nil, err
		}
		v, ok, err := db.KVGet(ctx, a.Key)
		return map[string]any{"found": ok, "value": v}, err
	case "kv.set":
		a, err := decode[kvArg](arg)
		if err != nil {
			return nil, err
		}
		return nil, db.KVSet(ctx, a.Key, a.Value)
	case "kv.delete":
		a, err := decode[kvArg](arg)
		if err != nil {
			return nil, err
		}
		return nil, db.KVDelete(ctx, a.Key)
	case "kv.list":
		a, err := decode[kvArg](arg)
		if err != nil {
			return nil, err
		}
		return db.KVList(ctx, a.Prefix, a.After, a.Limit)
	case "db.exec":
		a, err := decode[sqlArg](arg)
		if err != nil {
			return nil, err
		}
		return db.Exec(ctx, a.SQL, a.Args)
	case "db.query":
		a, err := decode[sqlArg](arg)
		if err != nil {
			return nil, err
		}
		return db.Query(ctx, a.SQL, a.Args)
	case "db.begin":
		return nil, db.Begin(ctx)
	case "db.commit":
		return nil, db.Commit(ctx)
	case "db.rollback":
		return nil, db.Rollback(ctx)
	}
	return nil, fmt.Errorf("unknown operation %q", op)
}

// apiResult is what panel.api returns: the body parsed when it is JSON.
type apiResult struct {
	Status int `json:"status"`
	Body   any `json:"body"`
}

func (inst *instance) opAPI(ctx context.Context, arg []byte, o callOpts) (any, error) {
	call := inst.host.deps.API
	if call == nil {
		return nil, errors.New("panel.api is not available")
	}
	a, err := decode[struct {
		Method string          `json:"method"`
		Path   string          `json:"path"`
		Body   json.RawMessage `json:"body"`
	}](arg)
	if err != nil {
		return nil, err
	}
	method := strings.ToUpper(a.Method)
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return nil, fmt.Errorf("panel.api: method %q", a.Method)
	}
	if o.readOnly && method != http.MethodGet {
		return nil, errors.New("panel.api: only GET inside a decision hook")
	}
	if !strings.HasPrefix(a.Path, "/v1/") || strings.HasPrefix(a.Path, "/v1/mcp") || strings.Contains(a.Path, "..") {
		return nil, fmt.Errorf("panel.api: path %q is not a /v1 route", a.Path)
	}
	var body []byte
	if len(a.Body) > 0 && string(a.Body) != "null" {
		body = a.Body
	}
	if len(body) > maxAPIBody {
		return nil, fmt.Errorf("panel.api: body over %d KB", maxAPIBody>>10)
	}
	status, resp, err := call(ctx, APIRequest{
		Plugin: inst.id, Perms: inst.rec.GrantedPerms, ReadOnly: o.readOnly,
		Method: method, Path: a.Path, Body: body,
	})
	if err != nil {
		return nil, err
	}
	if len(resp) > maxAPIResponse {
		return nil, fmt.Errorf("panel.api: answer over %d MB — page with limit/offset", maxAPIResponse>>20)
	}
	res := apiResult{Status: status, Body: string(resp)}
	var parsed any
	if json.Unmarshal(resp, &parsed) == nil {
		res.Body = parsed
	}
	return res, nil
}

// FetchRequest is one panel.http.fetch call.
type FetchRequest struct {
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body"`
	TimeoutMS int               `json:"timeout_ms"`
}

// FetchResponse is its answer.
type FetchResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

func (inst *instance) opFetch(ctx context.Context, arg []byte) (any, error) {
	f := inst.host.deps.Fetch
	if f == nil {
		return nil, errors.New("panel.http.fetch is not available")
	}
	req, err := decode[FetchRequest](arg)
	if err != nil {
		return nil, err
	}
	if req.TimeoutMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMS)*time.Millisecond)
		defer cancel()
	}
	return f.Fetch(ctx, inst.rec.GrantedNet, req)
}

// --- the plugin's log ---

const logLines = 1000

// LogLine is one line of a plugin's log.
type LogLine struct {
	At    int64  `json:"at"`
	Level string `json:"level"`
	Msg   string `json:"msg"`
}

func (inst *instance) logf(level, format string, args ...any) {
	switch level {
	case "info", "warn", "error":
	default:
		level = "info"
	}
	msg := fmt.Sprintf(format, args...)
	if len(msg) > maxLogLine {
		msg = msg[:maxLogLine] + "…"
	}
	inst.logs.add(LogLine{At: time.Now().Unix(), Level: level, Msg: msg})
	lg := inst.host.log.With("plugin", inst.id)
	switch level {
	case "error":
		lg.Warn(firstLine(msg)) // a plugin's error is the plugin's, not the panel's
	case "warn":
		lg.Info(firstLine(msg))
	default:
		lg.Debug(firstLine(msg))
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// ring is a fixed-size log buffer with its own lock: the admin panel reads a
// plugin's log while a call into it holds inst.mu.
type ring struct {
	mu   sync.Mutex
	buf  []LogLine
	next int
	full bool
}

func newRing(n int) *ring { return &ring{buf: make([]LogLine, n)} }

func (r *ring) add(l LogLine) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.next] = l
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
}

func (r *ring) lines() []LogLine {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		return append([]LogLine(nil), r.buf[:r.next]...)
	}
	return append(append([]LogLine(nil), r.buf[r.next:]...), r.buf[:r.next]...)
}
