package hop

import (
	"strings"
	"testing"
)

func TestRangeNormalize(t *testing.T) {
	// Range completely above target
	r1 := Range{Start: 20000, End: 30000, Target: 443}
	norm1, ok1 := r1.normalize()
	if !ok1 || norm1.Start != 20000 || norm1.End != 30000 || norm1.Target != 443 {
		t.Errorf("r1.normalize() = (%+v, %v); want (20000, 30000, 443, true)", norm1, ok1)
	}

	// Range covering target (target=443, start=400, end=500)
	r2 := Range{Start: 400, End: 500, Target: 443}
	norm2, ok2 := r2.normalize()
	if !ok2 || norm2.Start != 444 || norm2.End != 500 || norm2.Target != 443 {
		t.Errorf("r2.normalize() = (%+v, %v); want (444, 500, 443, true)", norm2, ok2)
	}

	// Range completely below target (start=100, end=200, target=443) -> should normalize Start to 444 -> Start > End -> ok=false
	r3 := Range{Start: 100, End: 200, Target: 443}
	norm3, ok3 := r3.normalize()
	if ok3 {
		t.Errorf("r3.normalize() = (%+v, %v); want ok=false", norm3, ok3)
	}
}

func TestRulesetAll(t *testing.T) {
	ranges := []Range{
		{Start: 20000, End: 30000, Target: 8443},
		{Start: 40000, End: 50000, Target: 9443},
	}
	ruleset := RulesetAll(ranges)

	if !strings.Contains(ruleset, "table inet rospanel_hop") {
		t.Errorf("ruleset missing table declaration: %s", ruleset)
	}
	if !strings.Contains(ruleset, "udp dport 20000-30000 redirect to :8443") {
		t.Errorf("ruleset missing first range: %s", ruleset)
	}
	if !strings.Contains(ruleset, "udp dport 40000-50000 redirect to :9443") {
		t.Errorf("ruleset missing second range: %s", ruleset)
	}
}
