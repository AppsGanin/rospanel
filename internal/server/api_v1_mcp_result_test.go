package server

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Shu1t3/rospanel-shu1t3/internal/mcp"
)

// bigPage builds a `{"data": […], "meta": …}` response of n rows of about size bytes
// each — the shape every paged endpoint answers with.
func bigPage(n, size int) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = `{"id":` + strconv.Itoa(i) + `,"blob":"` + strings.Repeat("x", size) + `"}`
	}
	total := strconv.Itoa(n)
	return `{"data":[` + strings.Join(rows, ",") +
		`],"meta":{"total":` + total + `,"offset":0,"limit":` + total + `}}`
}

// The point of the whole file: an oversized page comes back as JSON that parses.
// Cutting the string left the assistant holding a syntax error and a promise.
func TestMCPResultTruncatesWholeRows(t *testing.T) {
	const rows = 400
	out := shrinkMCPResult(bigPage(rows, 2000)) // ~830 KB in
	if len(out) > maxMCPResult {
		t.Fatalf("still over the ceiling: %d bytes", len(out))
	}
	// …and it uses the budget it was given. Dropping rows until something fits is
	// trivially satisfied by returning one of them, which is not an answer.
	if len(out) < maxMCPResult*3/4 {
		t.Errorf("used %d of %d bytes — too much of the page thrown away", len(out), maxMCPResult)
	}

	var got struct {
		Data []json.RawMessage `json:"data"`
		Meta map[string]any    `json:"meta"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("truncated result is not valid JSON: %v", err)
	}
	if len(got.Data) == 0 || len(got.Data) >= rows {
		t.Fatalf("kept %d of %d rows", len(got.Data), rows)
	}
	// Every row that survived is whole — a half-decoded one would have failed above,
	// but the count is what a caller pages on.
	if got.Meta["limit"] != float64(len(got.Data)) {
		t.Errorf("meta.limit = %v, want the %d rows actually returned", got.Meta["limit"], len(got.Data))
	}
	if got.Meta["total"] != float64(rows) {
		t.Errorf("meta.total = %v, want the full %d", got.Meta["total"], rows)
	}
	if _, ok := got.Meta["truncated"]; !ok {
		t.Error("nothing in meta says the answer is a prefix")
	}
}

// A response that fits must come back untouched — no meta invented, no bytes moved.
func TestMCPResultLeavesFittingAnswersAlone(t *testing.T) {
	in := bigPage(10, 100)
	if out := shrinkMCPResult(in); out != in {
		t.Errorf("a %d-byte answer was rewritten", len(in))
	}
}

// What cannot be shortened by dropping rows still has to be bounded, and cutting
// mid-rune would turn the tail of a Cyrillic name into a replacement character.
func TestMCPResultFallsBackOnRuneBoundary(t *testing.T) {
	blob := `{"data":{"log":"` + strings.Repeat("щ", maxMCPResult) + `"}}`
	out := shrinkMCPResult(blob)
	if !utf8.ValidString(out) {
		t.Error("the fallback cut in the middle of a rune")
	}
	if !strings.Contains(out, "truncated") {
		t.Error("the fallback drops bytes without saying so")
	}
	// One row bigger than the ceiling is the same case: there is nothing to drop.
	single := `{"data":[{"blob":"` + strings.Repeat("x", maxMCPResult) + `"}]}`
	if out := shrinkMCPResult(single); !strings.Contains(out, "truncated") {
		t.Error("a single oversized row was neither shortened nor flagged")
	}
}

// The ceiling is the backstop; the window is what keeps answers small in the first
// place. And a route whose body is a tarball is not offered as a tool at all.
func TestMCPWindowAndToolOptOut(t *testing.T) {
	byName := map[string]mcp.Tool{}
	for _, tl := range mcp.BuildTools(OpenAPISpec("https://panel.example/apiseg"), true) {
		byName[tl.Name] = tl
	}

	users, ok := byName["get_users"]
	if !ok {
		t.Fatal("no get_users tool")
	}
	if got := withMCPWindow(users, map[string]any{})["limit"]; got != mcpListDefaultLimit {
		t.Errorf("unwindowed list call got limit %v, want %d", got, mcpListDefaultLimit)
	}
	// An explicit window wins — including "everything", which stays sayable.
	for _, want := range []any{200, 0} {
		if got := withMCPWindow(users, map[string]any{"limit": want})["limit"]; got != want {
			t.Errorf("explicit limit %v was overridden with %v", want, got)
		}
	}
	// A call that takes no window is left exactly as it came.
	if _, ok := withMCPWindow(byName["get_summary"], map[string]any{})["limit"]; ok {
		t.Error("a non-list tool was given a limit it does not accept")
	}

	// The whole backup surface is kept away from assistants: the download because its
	// body is a tarball an assistant cannot read, and the manifest because describing
	// what a dump would contain is only useful to somebody about to take one — which is
	// an operator's job, done from the panel where the file actually goes somewhere.
	for _, name := range []string{"get_backup", "get_backup_info"} {
		if _, ok := byName[name]; ok {
			t.Errorf("%s is offered as a tool", name)
		}
	}
	// The opt-out has to stay per-route: neighbouring Monitoring endpoints are exactly
	// what an assistant is for, and an opt-out that swept the tag would take them too.
	for _, name := range []string{"get_system", "get_metrics", "get_summary"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("%s went missing — the opt-out took a neighbouring route with it", name)
		}
	}
}
