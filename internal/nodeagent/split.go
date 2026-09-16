package nodeagent

import (
	"bytes"
	"log/slog"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/nodestate"
)

// The node's side of a state sent in parts (see nodeapi/split.go and nodestate): the
// parts are kept as they arrived, the Xray config and meta the rest of the agent works
// from are put together from them, and a change is applied to the parts before
// anything is.

// applySplit applies a whole state sent in parts.
func (a *Agent) applySplit(s *nodeapi.SplitState) error {
	held, err := nodestate.FromSplit(s)
	if err != nil {
		return err
	}
	p, err := nodestate.Decode(held)
	if err != nil {
		return err
	}
	st, err := nodestate.Assemble(p)
	if err != nil {
		return err
	}
	if err := a.applyState(st); err != nil {
		return err
	}
	a.parts = p
	a.setSplit(held, st)
	slog.Info("node: applied new config", "hash", short(held.Hash), "users", len(p.Rows))
	return nil
}

// applyDelta applies a change to the split state the node holds. A change it cannot take
// — it names another state, or does not decode — is not an error to back off over: the
// node forgets which state it holds, and the panel, finding no tag, compares the
// content and sends it whole if it differs.
func (a *Agent) applyDelta(d *nodeapi.StateDelta) error {
	if a.parts == nil {
		slog.Warn("node: a change came for a state held in parts, and none is", "from", short(d.From))
		a.forgetSplitTag()
		return nil
	}
	next, err := nodestate.Apply(a.parts, d)
	if err != nil {
		slog.Warn("node: cannot take the change, asking for the whole state", "err", err)
		a.forgetSplitTag()
		return nil
	}
	if len(d.Upsert) == 0 && len(d.Remove) == 0 && bytes.Equal(next.Held.Blocked, a.parts.Held.Blocked) {
		// Only the name changed: nothing to apply.
		a.parts = next
		a.renameSplit(next.Held)
		return nil
	}
	st, err := nodestate.Assemble(next)
	if err != nil {
		slog.Warn("node: cannot put the change together, asking for the whole state", "err", err)
		a.forgetSplitTag()
		return nil
	}
	if err := a.applyUsers(st); err != nil {
		return err
	}
	a.parts = next
	a.setSplit(next.Held, st)
	slog.Info("node: applied a change of users", "hash", short(next.Held.Hash),
		"changed", len(d.Upsert), "removed", len(d.Remove))
	return nil
}
