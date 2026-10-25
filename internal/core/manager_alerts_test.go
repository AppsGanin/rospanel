package core

import (
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

func cpuRule(t *testing.T, m *Manager, forSecs int) model.AlertRule {
	t.Helper()
	r, err := m.SaveAlertRule(t.Context(), 0, model.AlertRuleInput{Name: "CPU high", Enabled: true,
		Metric: model.AlertMetricCPU, Op: model.AlertOpAbove, Threshold: 90, ForSecs: forSecs,
		NodeID: model.AlertEveryServer})
	if err != nil {
		t.Fatalf("save rule: %v", err)
	}
	return *r
}

func cpuAt(v float64) map[int64]alertSample {
	return map[int64]alertSample{model.LocalNodeID: {name: model.LocalNodeName,
		vals: map[string]float64{model.AlertMetricCPU: v}}}
}

var onlyMaster = map[int64]struct{}{model.LocalNodeID: {}}

func rulesOf(t *testing.T, m *Manager) []model.AlertRule {
	t.Helper()
	rules, err := m.store.ListAlertRules()
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

// A rule with a duration fires only once the condition has held that long, and a
// dip before then starts the count over.
func TestAlertFiresAfterItsDuration(t *testing.T) {
	t.Parallel()
	m, _ := newNotifyManager(t)
	cpuRule(t, m, 300)
	rules := rulesOf(t, m)
	now := time.Now()

	if out := m.evalAlertRules(rules, cpuAt(95), onlyMaster, now); out != nil {
		t.Fatalf("fired at once: %+v", out)
	}
	if out := m.evalAlertRules(rules, cpuAt(40), onlyMaster, now.Add(2*time.Minute)); out != nil {
		t.Fatalf("a dip produced %+v", out)
	}
	// The dip restarted the count: 4 minutes after it is still too soon.
	m.evalAlertRules(rules, cpuAt(95), onlyMaster, now.Add(3*time.Minute))
	if out := m.evalAlertRules(rules, cpuAt(96), onlyMaster, now.Add(7*time.Minute)); out != nil {
		t.Fatalf("fired after only 4 minutes: %+v", out)
	}
	out := m.evalAlertRules(rules, cpuAt(97), onlyMaster, now.Add(8*time.Minute+time.Second))
	if len(out) != 1 || !out[0].fired || out[0].ev.Value != 97 || out[0].ev.NodeName != model.LocalNodeName {
		t.Fatalf("expected one firing at 97%%, got %+v", out)
	}
	if got := rulesOf(t, m)[0].Firing; got != 1 {
		t.Errorf("firing count %d", got)
	}
}

// Once fired it stays quiet while the condition lasts, tracks the worst figure, and
// clears with the duration and the peak.
func TestAlertClearsWithPeakAndDuration(t *testing.T) {
	t.Parallel()
	m, _ := newNotifyManager(t)
	cpuRule(t, m, 0)
	rules := rulesOf(t, m)
	now := time.Now()

	if out := m.evalAlertRules(rules, cpuAt(92), onlyMaster, now); len(out) != 1 || !out[0].fired {
		t.Fatalf("no firing: %+v", out)
	}
	if out := m.evalAlertRules(rules, cpuAt(99), onlyMaster, now.Add(time.Minute)); out != nil {
		t.Fatalf("a second notice while it lasts: %+v", out)
	}
	out := m.evalAlertRules(rules, cpuAt(30), onlyMaster, now.Add(5*time.Minute))
	if len(out) != 1 || out[0].fired {
		t.Fatalf("expected the all-clear, got %+v", out)
	}
	ev := out[0].ev
	if ev.Peak != 99 || ev.ResolvedAt-ev.FiredAt != 300 {
		t.Errorf("peak %v, lasted %ds", ev.Peak, ev.ResolvedAt-ev.FiredAt)
	}
	hist, _ := m.AlertEvents(10, 0)
	if len(hist) != 1 || hist[0].ResolvedAt == 0 || hist[0].Peak != 99 {
		t.Errorf("history: %+v", hist)
	}
}

// A figure hovering at its line does not fire and clear every minute.
func TestAlertRefireCooldown(t *testing.T) {
	t.Parallel()
	m, _ := newNotifyManager(t)
	cpuRule(t, m, 0)
	rules := rulesOf(t, m)
	now := time.Now()

	m.evalAlertRules(rules, cpuAt(95), onlyMaster, now)
	m.evalAlertRules(rules, cpuAt(50), onlyMaster, now.Add(time.Minute))
	if out := m.evalAlertRules(rules, cpuAt(95), onlyMaster, now.Add(2*time.Minute)); out != nil {
		t.Fatalf("fired again inside the cooldown: %+v", out)
	}
	if out := m.evalAlertRules(rules, cpuAt(95), onlyMaster, now.Add(12*time.Minute)); len(out) != 1 {
		t.Fatalf("did not fire after the cooldown: %+v", out)
	}
}

// After a restart a condition that is still true is not announced a second time, and
// its clearing is.
func TestAlertSurvivesRestart(t *testing.T) {
	t.Parallel()
	m, _ := newNotifyManager(t)
	cpuRule(t, m, 0)
	rules := rulesOf(t, m)
	now := time.Now()
	m.evalAlertRules(rules, cpuAt(95), onlyMaster, now)

	m.alerts = alertEngine{} // the process restarted
	if out := m.evalAlertRules(rules, cpuAt(96), onlyMaster, now.Add(time.Minute)); out != nil {
		t.Fatalf("announced again after a restart: %+v", out)
	}
	out := m.evalAlertRules(rules, cpuAt(10), onlyMaster, now.Add(2*time.Minute))
	if len(out) != 1 || out[0].fired || out[0].ev.Peak != 96 {
		t.Fatalf("the all-clear was lost: %+v", out)
	}
}

// A figure a server does not report is unknown, never zero: a "below" rule must not
// fire on an agent too old to send it.
func TestAlertIgnoresUnreportedMetric(t *testing.T) {
	t.Parallel()
	m, _ := newNotifyManager(t)
	r, err := m.SaveAlertRule(t.Context(), 0, model.AlertRuleInput{Name: "idle", Enabled: true,
		Metric: model.AlertMetricCPU, Op: model.AlertOpBelow, Threshold: 5, NodeID: model.AlertEveryServer})
	if err != nil {
		t.Fatal(err)
	}
	samples := map[int64]alertSample{model.LocalNodeID: {name: "x", vals: map[string]float64{}}}
	if out := m.evalAlertRules([]model.AlertRule{*r}, samples, onlyMaster, time.Now()); out != nil {
		t.Fatalf("fired on an unknown figure: %+v", out)
	}
}

// A silent (offline) server keeps its firing open and neither clears nor fires; a
// server that is gone, or a rule switched off, closes it without a word.
func TestAlertSilentServerAndOrphans(t *testing.T) {
	t.Parallel()
	m, _ := newNotifyManager(t)
	cpuRule(t, m, 0)
	rules := rulesOf(t, m)
	now := time.Now()
	m.evalAlertRules(rules, cpuAt(95), onlyMaster, now)

	if out := m.evalAlertRules(rules, map[int64]alertSample{}, onlyMaster, now.Add(time.Minute)); out != nil {
		t.Fatalf("a silent server produced %+v", out)
	}
	if rulesOf(t, m)[0].Firing != 1 {
		t.Fatal("the firing was closed while the server was only silent")
	}
	// The server is removed: closed, no notice.
	if out := m.evalAlertRules(rules, map[int64]alertSample{}, map[int64]struct{}{}, now.Add(2*time.Minute)); out != nil {
		t.Fatalf("closing an orphan produced %+v", out)
	}
	if rulesOf(t, m)[0].Firing != 0 {
		t.Fatal("the firing of a removed server stayed open")
	}
}

// Changing what a rule measures closes its open firing; renaming it does not.
func TestAlertEditClosesStaleFirings(t *testing.T) {
	t.Parallel()
	m, _ := newNotifyManager(t)
	r := cpuRule(t, m, 0)
	m.evalAlertRules(rulesOf(t, m), cpuAt(95), onlyMaster, time.Now())

	in := r.AlertRuleInput
	in.Name = "renamed"
	if _, err := m.SaveAlertRule(t.Context(), r.ID, in); err != nil {
		t.Fatal(err)
	}
	if rulesOf(t, m)[0].Firing != 1 {
		t.Fatal("a rename closed the firing")
	}
	in.Threshold = 99
	if _, err := m.SaveAlertRule(t.Context(), r.ID, in); err != nil {
		t.Fatal(err)
	}
	if rulesOf(t, m)[0].Firing != 0 {
		t.Fatal("a new threshold left the old firing open")
	}
}

func TestAlertValidation(t *testing.T) {
	t.Parallel()
	m, _ := newNotifyManager(t)
	good := model.AlertRuleInput{Name: "x", Enabled: true, Metric: model.AlertMetricMem,
		Op: model.AlertOpAbove, Threshold: 80, NodeID: model.AlertEveryServer}
	cases := map[string]func(*model.AlertRuleInput){
		"err.alertName":      func(r *model.AlertRuleInput) { r.Name = "  " },
		"err.alertMetric":    func(r *model.AlertRuleInput) { r.Metric = "bogus" },
		"err.alertOp":        func(r *model.AlertRuleInput) { r.Op = "eq" },
		"err.alertThreshold": func(r *model.AlertRuleInput) { r.Threshold = 101 },
		"err.alertFor":       func(r *model.AlertRuleInput) { r.ForSecs = 86401 },
		"err.alertServer":    func(r *model.AlertRuleInput) { r.NodeID = 999 },
	}
	for code, mutate := range cases {
		in := good
		mutate(&in)
		_, err := m.SaveAlertRule(t.Context(), 0, in)
		var ve *ValidationError
		if err == nil || !asValidation(err, &ve) || ve.Code != code {
			t.Errorf("%s: got %v", code, err)
		}
	}
	if _, err := m.SaveAlertRule(t.Context(), 0, good); err != nil {
		t.Fatalf("a good rule was refused: %v", err)
	}
}

func asValidation(err error, target **ValidationError) bool {
	ve, ok := err.(*ValidationError)
	if ok {
		*target = ve
	}
	return ok
}

// The admin message names the rule, the server, the figure and the line.
func TestAlertNoticeText(t *testing.T) {
	t.Parallel()
	m, msgs := newNotifyManager(t)
	ev := model.AlertEvent{RuleName: "CPU <high>", NodeName: "NL", Metric: model.AlertMetricCPU,
		Op: model.AlertOpAbove, Threshold: 90, Value: 94, Peak: 97, FiredAt: 1000, ResolvedAt: 1000 + 1500}
	m.sendAlertNotice(alertNotice{fired: true, ev: ev, host: "203.0.113.7"})
	m.sendAlertNotice(alertNotice{ev: ev, host: "203.0.113.7"})
	if len(*msgs) != 2 {
		t.Fatalf("messages: %v", *msgs)
	}
	for _, want := range []string{"CPU &lt;high&gt;", "NL", "203.0.113.7", "94%", "выше 90%"} {
		if !strings.Contains((*msgs)[0], want) {
			t.Errorf("fired message lacks %q: %s", want, (*msgs)[0])
		}
	}
	for _, want := range []string{"97%", "25"} {
		if !strings.Contains((*msgs)[1], want) {
			t.Errorf("cleared message lacks %q: %s", want, (*msgs)[1])
		}
	}
}
