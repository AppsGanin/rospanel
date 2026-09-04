package sub

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func tplUser() model.User {
	return model.User{ID: 1, Name: "u", UUID: "11111111-1111-1111-1111-111111111111", Password: "pw"}
}

func tplServers() []Server {
	return One(&model.Settings{
		Host: "vpn.example.com", SNI: "vpn.example.com",
		VLESSEnabled: true, VLESSPort: 443,
		HysteriaEnabled: true, HysteriaPort: 8443,
		SubTitle: "MyVPN",
	})
}

// The whole point of a template: the operator's document comes out, with the panel's
// servers spliced in where they asked for them.
func TestSingBoxTemplateSplicesProxies(t *testing.T) {
	tpl := `{
	  "log": {"level": "debug"},
	  "outbounds": [
	    {"type": "selector", "tag": "{{group}}", "outbounds": ["{{tags}}"]},
	    "{{proxies}}",
	    {"type": "direct", "tag": "direct"}
	  ],
	  "route": {"final": "{{group}}"}
	}`
	out, err := SingBoxWithTemplate(tplUser(), tplServers(), tpl)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("rendered profile is not JSON: %v\n%s", err, out)
	}
	// The operator's own keys survive untouched.
	if lvl := doc["log"].(map[string]any)["level"]; lvl != "debug" {
		t.Errorf("the operator's log level was rewritten to %v", lvl)
	}
	outbounds := doc["outbounds"].([]any)
	sel := outbounds[0].(map[string]any)
	if sel["tag"] != "MyVPN" {
		t.Errorf("{{group}} = %v, want the profile title", sel["tag"])
	}
	tags := sel["outbounds"].([]any)
	if len(tags) < 2 {
		t.Fatalf("{{tags}} spliced %d entries, want one per lane", len(tags))
	}
	// The tags list must be strings, spliced in place — not a list inside a list.
	if _, ok := tags[0].(string); !ok {
		t.Errorf("{{tags}} produced %T, want a flat list of tag strings", tags[0])
	}
	// The proxies are objects, spliced in place, and the direct outbound after them
	// is still last — order is the operator's, not the renderer's.
	if _, ok := outbounds[1].(map[string]any); !ok {
		t.Errorf("{{proxies}} produced %T, want the outbound objects", outbounds[1])
	}
	last := outbounds[len(outbounds)-1].(map[string]any)
	if last["tag"] != "direct" {
		t.Errorf("the operator's trailing outbound moved: %v", last["tag"])
	}
	if doc["route"].(map[string]any)["final"] != "MyVPN" {
		t.Error("{{group}} was not replaced in route.final")
	}
	// Every proxy tag the selector names must exist as an outbound, or sing-box
	// refuses the profile — the exact failure a template is most likely to cause.
	present := map[string]bool{}
	for _, o := range outbounds {
		if m, ok := o.(map[string]any); ok {
			present[m["tag"].(string)] = true
		}
	}
	for _, tag := range tags {
		if !present[tag.(string)] {
			t.Errorf("selector names %q but no outbound has that tag", tag)
		}
	}
}

// A template that cannot work must never reach a client: a profile it cannot parse
// costs the user every server, not just the broken one.
func TestSingBoxTemplateFallsBack(t *testing.T) {
	generated := SingBoxJSONMulti(tplUser(), tplServers())

	out, err := SingBoxWithTemplate(tplUser(), tplServers(), `{"outbounds": [`)
	if err == nil {
		t.Error("unparseable template reported success")
	}
	if out != generated {
		t.Error("unparseable template did not fall back to the generated profile")
	}

	// A user with no servers: the generated profile has a valid direct-only answer,
	// while a template would leave a selector pointing at nothing.
	empty := []Server{}
	if out, _ := SingBoxWithTemplate(tplUser(), empty, `{"outbounds":["{{proxies}}"]}`); out != SingBoxJSONMulti(tplUser(), empty) {
		t.Error("a user with no servers did not get the generated profile")
	}

	// No template at all is the default path, byte for byte.
	if out, err := SingBoxWithTemplate(tplUser(), tplServers(), "   "); err != nil || out != generated {
		t.Errorf("a blank template changed the output (err=%v)", err)
	}
}

// Xray JSON is an array of one config per lane, so the template is one config and the
// renderer repeats it — each with its own outbound chain and its own name.
func TestXrayTemplateRepeatsPerLane(t *testing.T) {
	tpl := `{"remarks": "{{remarks}}", "inbounds": [], "outbounds": ["{{outbounds}}"], "mine": true}`
	out, err := XrayJSONWithTemplate(tplUser(), tplServers(), model.SubDPI{}, tpl)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var configs []map[string]any
	if err := json.Unmarshal([]byte(out), &configs); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out)
	}
	if len(configs) < 2 {
		t.Fatalf("rendered %d configs, want one per lane", len(configs))
	}
	names := map[string]bool{}
	for _, c := range configs {
		if c["mine"] != true {
			t.Error("the operator's own key did not survive")
		}
		obs, ok := c["outbounds"].([]any)
		if !ok || len(obs) == 0 {
			t.Fatalf("outbounds = %v, want the lane's chain", c["outbounds"])
		}
		if _, ok := obs[0].(map[string]any); !ok {
			t.Errorf("{{outbounds}} produced %T, want the outbound objects", obs[0])
		}
		r, _ := c["remarks"].(string)
		if r == "" || r == TplRemarks {
			t.Errorf("remarks = %q, want the lane name", r)
		}
		names[r] = true
	}
	if len(names) != len(configs) {
		t.Error("two lanes rendered under the same name")
	}
}

func TestXrayTemplateFallsBack(t *testing.T) {
	generated := XrayJSONMulti(tplUser(), tplServers(), model.SubDPI{})
	out, err := XrayJSONWithTemplate(tplUser(), tplServers(), model.SubDPI{}, `not json`)
	if err == nil {
		t.Error("unparseable template reported success")
	}
	if out != generated {
		t.Error("unparseable template did not fall back")
	}
}

// Validation runs where the operator can see it. A template with nowhere to put the
// servers parses fine and produces a profile with no servers in it — valid, and
// useless — so it is refused rather than stored.
func TestTemplateValidation(t *testing.T) {
	if err := ValidateSingBoxTemplate(""); err != nil {
		t.Errorf("an empty template is the default and must be valid: %v", err)
	}
	if err := ValidateSingBoxTemplate(`{"outbounds": []}`); !errors.Is(err, ErrTemplateEmpty) {
		t.Errorf("a template with no {{proxies}} gave %v, want ErrTemplateEmpty", err)
	}
	if err := ValidateSingBoxTemplate(`{"outbounds": ["{{proxies}}"]}`); err != nil {
		t.Errorf("a valid template was refused: %v", err)
	}
	if err := ValidateSingBoxTemplate(`{`); err == nil {
		t.Error("invalid JSON was accepted")
	}
	if err := ValidateXrayTemplate(`{"outbounds": ["{{proxies}}"]}`); !errors.Is(err, ErrTemplateEmpty) {
		t.Error("the xray template needs {{outbounds}}, not {{proxies}}")
	}
	if err := ValidateClashTemplate("proxies:\n  - {}"); !errors.Is(err, ErrTemplateEmpty) {
		t.Error("a mihomo template without the marker was accepted")
	}
	if err := ValidateClashTemplate("proxies: # LEAVE THIS LINE!"); err != nil {
		t.Errorf("a marked mihomo template was refused: %v", err)
	}
}

// The mihomo path is textual, so the check that matters is that the operator's
// document survives around the injection.
func TestClashTemplateKeepsTheDocument(t *testing.T) {
	tpl := "mixed-port: 7890\nproxies: # LEAVE THIS LINE!\nproxy-groups:\n  - name: main\n    proxies:\n    # LEAVE THIS LINE!\nrules:\n  - MATCH,main\n"
	out := ClashWithTemplateMulti(tplUser(), tplServers(), tpl)
	if !strings.Contains(out, "mixed-port: 7890") {
		t.Error("the operator's own keys were lost")
	}
	if strings.Contains(out, "LEAVE THIS LINE") {
		t.Error("a marker survived into the served profile")
	}
	if !strings.Contains(out, "type: hysteria2") {
		t.Error("the proxies were not injected")
	}
}
