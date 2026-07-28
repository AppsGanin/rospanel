package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/model"
)

// maxJSONBody caps every admin JSON request body. The admin API is authenticated
// and low-volume, so a single generous limit is simpler than per-route tuning.
const maxJSONBody = 1 << 18 // 256 KB

// writeJSON encodes v as the response body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr writes a {"error": msg} body with the given status.
func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeErrCode is writeErr with a dictionary code the panel translates. The message
// still travels: it is the fallback for a code the panel does not know, so an older
// build shows a sentence rather than a raw key.
func writeErrCode(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": msg, "code": code})
}

// writeErrDetail is writeErrCode for a message that ends in someone else's text —
// a store failure, a Telegram refusal, an ACME error. The detail travels as an
// argument rather than glued onto msg: the panel renders the code, and a detail
// baked into the fallback string would simply be dropped on the floor there.
func writeErrDetail(w http.ResponseWriter, status int, code, msg, detail string) {
	writeJSON(w, status, map[string]any{
		"error": msg + detail,
		"code":  code,
		"args":  map[string]any{"detail": detail},
	})
}

// writeOK writes the standard {"ok": true} success body.
func writeOK(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// writeManagerErr maps a manager error to its HTTP status: a core.ValidationError
// (bad operator input) → 400, anything else (a server/store fault) → 500. The
// message is surfaced to the operator either way.
func writeManagerErr(w http.ResponseWriter, err error) {
	var ve *core.ValidationError
	if errors.As(err, &ve) {
		// The code and its arguments ride along so the panel can render the message
		// in the admin's own language; err.Error() is the fallback for a code the
		// panel has no entry for.
		writeCoded(w, ve.Code, ve.Args, err.Error())
		return
	}
	// A model-layer validator reached the handler without passing through core —
	// a route that validates the payload before handing it over. Same contract,
	// same 400.
	var fe *model.FieldError
	if errors.As(err, &fe) {
		writeCoded(w, fe.Code, fe.Args, err.Error())
		return
	}
	// Not operator input — a store or server fault. No code: these are not a
	// vocabulary the panel should be translating, and the text is diagnostic.
	writeErr(w, http.StatusInternalServerError, err.Error())
}

// writeCoded writes a 400 carrying a dictionary code and its arguments, with msg
// as the fallback wording for a code the panel has no entry for.
func writeCoded(w http.ResponseWriter, code string, args map[string]any, msg string) {
	body := map[string]any{"error": msg}
	if code != "" {
		body["code"] = code
	}
	if len(args) > 0 {
		body["args"] = args
	}
	writeJSON(w, http.StatusBadRequest, body)
}

// decodeJSON reads a size-limited JSON body into dst. It rejects any body whose
// Content-Type isn't application/json — together with the CSRF guard this blocks the
// cross-site "<form enctype=text/plain>" trick that smuggles a JSON-shaped body. On
// failure it writes a 4xx and returns false, so handlers can
// `if !decodeJSON(w, r, &req) { return }`.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	// Require application/json, INCLUDING when Content-Type is absent — the SPA's
	// fetch wrapper always sets it, and a missing header would otherwise slip past
	// this check and let the cross-site "<form enctype=text/plain>" trick smuggle a
	// JSON-shaped body without a CORS preflight.
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeErrCode(w, http.StatusUnsupportedMediaType, "err.expectJSON", "ожидается application/json")
		return false
	}
	// Bound a slow-trickle request body per-handler (these bodies are tiny). Done
	// here rather than via a server-wide ReadTimeout, which would also kill the
	// long-lived SSE streams — those never go through decodeJSON.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(30 * time.Second))
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody)).Decode(dst); err != nil {
		writeErrCode(w, http.StatusBadRequest, "err.badRequestBody", "неверное тело запроса")
		return false
	}
	return true
}

// pathID parses the {id} path segment. On failure it writes a 400 and returns false.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, "err.badID", "неверный id")
		return 0, false
	}
	return id, true
}

// sseStart sets the Server-Sent Events headers and returns the flusher. If the
// ResponseWriter can't stream, it writes a 500 and returns false.
func sseStart(w http.ResponseWriter) (http.Flusher, bool) {
	// Walk the Unwrap chain: a mutating route (e.g. the SSH-provision POST) is wrapped
	// by the audit middleware's auditStatus, which forwards Flush only via Unwrap, so
	// a direct w.(http.Flusher) on it fails.
	flusher, ok := unwrapFlusher(w)
	if !ok {
		writeErrCode(w, http.StatusInternalServerError, "err.streamingUnsupported", "стриминг не поддерживается")
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering
	return flusher, true
}

// unwrapFlusher finds the http.Flusher through any middleware wrappers that expose
// Unwrap() http.ResponseWriter (the pattern http.NewResponseController relies on).
func unwrapFlusher(w http.ResponseWriter) (http.Flusher, bool) {
	for {
		if f, ok := w.(http.Flusher); ok {
			return f, true
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil, false
		}
		w = u.Unwrap()
	}
}

// sseSend writes one SSE data frame and flushes it. Returns false if the client
// has disconnected.
func sseSend(w http.ResponseWriter, flusher http.Flusher, data string) bool {
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return false
	}
	flusher.Flush()
	return true
}
