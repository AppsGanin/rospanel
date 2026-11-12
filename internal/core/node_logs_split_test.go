package core

import (
	"slices"
	"testing"

	"github.com/AppsGanin/rospanel/internal/logbuf"
	"github.com/AppsGanin/rospanel/internal/model"
)

func TestWithPanelLines(t *testing.T) {
	node := []string{"a", "b"}
	if got := withPanelLines(node, nil); !slices.Equal(got, node) {
		t.Fatalf("no panel lines: got %v", got)
	}
	got := withPanelLines(node, []string{"p"})
	if want := []string{"a", "b", "--- panel ---", "p"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if !slices.Equal(node, []string{"a", "b"}) {
		t.Fatalf("the node's stored tail was changed: %v", node)
	}
}

// The master's own entry is its server log, not the panel's.
func TestRequestNodeLogsMasterIsServerLog(t *testing.T) {
	_, _ = logbuf.Server.Write([]byte("xray: started (server-log test)\n"))
	_, _ = logbuf.Default.Write([]byte("billing: panel only (server-log test)\n"))
	lines, _ := (&Manager{}).RequestNodeLogs(model.LocalNodeID)
	if !slices.Contains(lines, "xray: started (server-log test)") {
		t.Fatalf("server line missing from the master's log: %v", lines)
	}
	if slices.Contains(lines, "billing: panel only (server-log test)") {
		t.Fatalf("panel line leaked into the master's server log")
	}
}
