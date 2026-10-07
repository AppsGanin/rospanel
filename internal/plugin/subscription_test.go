package plugin

import (
	"context"
	"strings"
	"testing"
)

const subManifest = `{
	"id": "subx", "version": "1.0.0", "api": 1, "name": "Sub",
	"provides": {"subscription": true},
	"experimental": ["subscription"]
}`

const subMain = `
let calls = 0;
export function transformSubscription(req) {
	calls++;
	panel.kv.set("calls", calls);
	if (req.user.id === 2) return { links: ["javascript:alert(1)"] };
	if (req.user.id === 3) return null;
	switch (req.format) {
	case "links": return { links: req.links.map(l => l.replace(/#.*/, "#" + req.user.name)).reverse() };
	case "clash": return { config: req.config.replace("proxies:", "# hi\nproxies:") };
	case "singbox": req.config.outbounds.push({ type: "direct", tag: "extra" }); return { config: req.config };
	case "xray": return { config: "not a profile" };
	}
}
`

func TestTransformSubscription(t *testing.T) {
	h := newHarness(t)
	h.install(pkg(t, subManifest, subMain, nil))
	h.enable("subx")
	ctx := context.Background()
	ann := map[string]any{"id": 1, "name": "ann"}
	links := []string{"vless://a@h:443#One", "hy2://b@h:443#Two"}

	got := h.host.TransformLinks(ctx, ann, links)
	if len(got) != 2 || got[0] != "hy2://b@h:443#ann" || got[1] != "vless://a@h:443#ann" {
		t.Fatalf("links: %v", got)
	}
	_ = h.host.TransformLinks(ctx, ann, links) // the same input: from the cache
	if c := gateKVOf(t, h, "subx", "calls"); c != "1" {
		t.Fatalf("plugin called %s times for one input", c)
	}
	if got := h.host.TransformLinks(ctx, map[string]any{"id": 2}, links); len(got) != 2 || got[0] != links[0] {
		t.Fatalf("a bad link was served: %v", got)
	}
	if got := h.host.TransformLinks(ctx, map[string]any{"id": 3}, links); got[0] != links[0] {
		t.Fatalf("null changed the links: %v", got)
	}

	if got := h.host.TransformConfig(ctx, ann, SubClash, "mixed-port: 7890\nproxies:\n  - name: a\n"); !strings.Contains(got, "# hi\nproxies:") {
		t.Fatalf("clash: %q", got)
	}
	sb := `{"outbounds": [{"type": "vless", "tag": "a"}]}`
	if got := h.host.TransformConfig(ctx, ann, SubSingBox, sb); !strings.Contains(got, `"extra"`) {
		t.Fatalf("sing-box: %s", got)
	}
	xr := `[{"outbounds": []}]`
	if got := h.host.TransformConfig(ctx, ann, SubXray, xr); got != xr {
		t.Fatalf("a string served for an Xray profile: %s", got)
	}
}
