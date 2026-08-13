package server

import (
	"net/http"
)

// Paging for the list endpoints.
//
// Every list here was unbounded: a panel with ten thousand payment orders answered
// /v1/billing/orders with all of them, and the caller had no way to ask for less.
// That is a problem for an integration polling on a timer, and a worse one for an
// assistant, whose context the whole list has to fit into.
//
// The window is the same everywhere — ?limit and ?offset, with a "meta" block
// carrying the total — so a caller learns it once. Where a list was already paged
// (users, and the journals, which page by cursor because their rows shift), that
// shape is kept rather than a second one invented.

// defaultPageLimit is what a caller gets when they ask for no window at all.
//
// Not "everything": the point of this file is that an unbounded default is how a
// small integration works fine for a year and then times out. Callers who genuinely
// want the lot ask for it (limit<=0 is still honoured as "all remaining"), which is
// a deliberate act rather than an accident.
const defaultPageLimit = 100

// maxPageLimit caps one response however large a limit is asked for. It bounds the
// memory a single request can cost the panel — and, for the assistant reading these
// through MCP, the context one call can consume.
const maxPageLimit = 1000

// page windows a slice from the request's ?limit/?offset and returns the window
// with the meta block describing it.
//
// limit absent  ⇒ defaultPageLimit
// limit <= 0    ⇒ everything from offset (an explicit "give me all of it")
// limit > max   ⇒ clamped to maxPageLimit, and meta says what was actually used
func page[T any](r *http.Request, items []T) ([]T, map[string]int) {
	total := len(items)
	q := r.URL.Query()
	offset := clampNonNeg(atoiOr(q.Get("offset"), 0))
	limit := atoiOr(q.Get("limit"), defaultPageLimit)
	if limit > maxPageLimit {
		limit = maxPageLimit
	}
	if offset > total {
		offset = total
	}
	out := items[offset:]
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out, map[string]int{"total": total, "offset": offset, "limit": limit}
}

// writeAPIPage writes a windowed list in the envelope the paged endpoints share.
func writeAPIPage[T any](w http.ResponseWriter, r *http.Request, items []T) {
	if items == nil {
		items = []T{}
	}
	window, meta := page(r, items)
	if window == nil {
		window = []T{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": window, "meta": meta})
}
