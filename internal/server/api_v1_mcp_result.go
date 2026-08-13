package server

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/AppsGanin/rospanel/internal/mcp"
)

// Fitting one tool result under maxMCPResult.
//
// The obvious way to bound a response is to cut the string, and that is what this
// did: the assistant was handed half of an object, a syntax error where the closing
// brace should be, and a sentence at the end asking it to believe that the rest
// existed. Everything it could do with that was a guess.
//
// So the list is shortened by ELEMENTS instead. Drop whole rows off the end until
// the answer fits, then say so in the `meta` block the paged endpoints already
// carry — `limit` becomes the count actually returned, `total` still says how many
// there are. The result stays valid JSON, and "you got a prefix, ask for the rest
// with offset" is a thing every caller of this API already knows how to read.
//
// The string cut survives as the fallback for what cannot be shortened that way: a
// single enormous row, a non-JSON body, an endpoint whose payload is not a list.

// shrinkMCPResult returns text unchanged when it fits, and otherwise the longest
// prefix of it that does — by rows where the payload is a list, by bytes otherwise.
func shrinkMCPResult(text string) string {
	if len(text) <= maxMCPResult {
		return text
	}
	if out, ok := shrinkJSONList(text); ok {
		return out
	}
	return cutRunes(text)
}

// shrinkJSONList rebuilds a `{"data": [...], "meta": {...}}` response with as many
// leading rows as fit. It reports false for anything that isn't such a response, or
// that cannot be made to fit by dropping rows.
func shrinkJSONList(text string) (string, bool) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &top); err != nil {
		return "", false
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(top["data"], &rows); err != nil || len(rows) < 2 {
		// Not a list, or a single row that is itself over the ceiling — dropping rows
		// cannot help, and dropping the only one would answer a question with nothing.
		return "", false
	}

	// Rebuilding is how the size is measured, because the meta block changes size as
	// the count does and predicting that exactly is more arithmetic than it is worth.
	// Each pass scales the count by how far over it was, so this lands in two or
	// three iterations rather than walking down a row at a time.
	for n := len(rows); n > 0; {
		out, err := rebuildPage(top, rows[:n], len(rows))
		if err != nil {
			return "", false
		}
		if len(out) <= maxMCPResult {
			return string(out), true
		}
		next := n * maxMCPResult / len(out) * 15 / 16 // proportional, with a margin
		if next >= n {
			next = n - 1
		}
		n = next
	}
	return "", false
}

// rebuildPage renders the response with `kept` rows out of `total`, and a meta block
// that says which of the two the caller is holding.
func rebuildPage(top map[string]json.RawMessage, kept []json.RawMessage, total int) ([]byte, error) {
	data, err := json.Marshal(kept)
	if err != nil {
		return nil, err
	}
	meta := map[string]any{}
	if raw, ok := top["meta"]; ok {
		if err := json.Unmarshal(raw, &meta); err != nil {
			meta = map[string]any{}
		}
	}
	// `limit` already means "the window actually used" (see api_v1_paging.go), so a
	// caller paginating on limit/offset walks the rest without being taught anything
	// new. The note is for the reader that doesn't do arithmetic on a meta block.
	meta["limit"] = len(kept)
	if _, ok := meta["total"]; !ok {
		meta["total"] = total
	}
	meta["truncated"] = "response exceeded the MCP size ceiling: " +
		"these are the first rows only — page through the rest with limit/offset"

	out := make(map[string]json.RawMessage, len(top)+1)
	for k, v := range top {
		out[k] = v
	}
	out["data"] = data
	if out["meta"], err = json.Marshal(meta); err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// cutRunes is the fallback: a byte cut, moved back to a rune boundary so the tail of
// a Cyrillic name doesn't become a replacement character, plus the warning that has
// to be in the text because there is no longer a structure to put it in.
func cutRunes(text string) string {
	cut := maxMCPResult
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "\n…truncated: ask for a smaller page (limit/offset)"
}

// withMCPWindow gives a list call a page size when the assistant supplied none, so
// the ceiling is a backstop rather than the thing that shapes every answer. Anything
// the caller asked for explicitly is left exactly as it is.
func withMCPWindow(t mcp.Tool, args map[string]any) map[string]any {
	if !t.HasParam("limit") {
		return args
	}
	if _, ok := args["limit"]; ok {
		return args
	}
	out := make(map[string]any, len(args)+1)
	for k, v := range args {
		out[k] = v
	}
	out["limit"] = mcpListDefaultLimit
	return out
}
