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

// A template is stored once and rendered on every subscription fetch by every client,
// so its cost is paid by the panel over and over. Two ways it grows are invisible in
// the document: repeating the list placeholder, and nesting (the output is indented
// two spaces per level, so depth alone turns kilobytes into hundreds of megabytes).
func TestTemplateExpansionIsBounded(t *testing.T) {
	// Deep nesting, no proxies involved at all.
	deep := strings.Repeat("[", 200) + `"{{proxies}}"` + strings.Repeat("]", 200)
	if err := ValidateSingBoxTemplate(deep); !errors.Is(err, ErrTemplateTooBig) {
		t.Errorf("a 200-deep template gave %v, want ErrTemplateTooBig", err)
	}

	// Many list placeholders: each one is replaced by the whole proxy list.
	slots := make([]string, 200)
	for i := range slots {
		slots[i] = `"{{proxies}}"`
	}
	many := `{"outbounds": [` + strings.Join(slots, ",") + `]}`
	if err := ValidateSingBoxTemplate(many); !errors.Is(err, ErrTemplateTooBig) {
		t.Errorf("a template with 200 slots gave %v, want ErrTemplateTooBig", err)
	}

	// The shapes an operator actually writes stay accepted.
	ok := `{"outbounds": [{"type":"selector","outbounds":["{{tags}}"]}, "{{proxies}}"], "route": {"final": "{{group}}"}}`
	if err := ValidateSingBoxTemplate(ok); err != nil {
		t.Errorf("an ordinary template was refused: %v", err)
	}

	// And a template that slips past validation still cannot produce a giant profile:
	// the render measures the finished bytes and falls back.
	huge := map[string]any{"pad": strings.Repeat("x", maxRenderedBytes), "outbounds": []any{TplProxies}}
	b, err := json.Marshal(huge)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := SingBoxWithTemplate(tplUser(), tplServers(), string(b))
	if !errors.Is(err, ErrTemplateTooBig) {
		t.Errorf("an oversized render gave %v, want ErrTemplateTooBig", err)
	}
	if out != SingBoxJSONMulti(tplUser(), tplServers()) {
		t.Error("an oversized render did not fall back to the generated profile")
	}
}

// Two lanes whose names carry different variables can still render to the same
// string — "{left}" and "{total}" are both ∞ for an account with no quota. A duplicate
// Clash node name or sing-box tag makes a client reject the WHOLE profile, so the user
// would lose every server rather than one. Neither validator can see this: they compare
// the stored text, and the collision only exists once the values are resolved.
func TestRenderedNameCollisionsAreDeduplicated(t *testing.T) {
	set := &model.Settings{
		Host: "vpn.example.com", SNI: "vpn.example.com", NodeLabel: "NL",
		VLESSEnabled: true, VLESSPort: 443, VLESSName: "Fast {left}",
		HysteriaEnabled: true, HysteriaPort: 8443, HysteriaName: "Fast {total}",
		SubTitle: "MyVPN",
	}
	// No quota: {left} and {total} both render ∞.
	u := model.User{ID: 1, Name: "u", UUID: "11111111-1111-1111-1111-111111111111", Password: "pw"}
	servers := One(set)

	yaml := ClashYAMLMulti(u, servers)
	names := map[string]int{}
	for _, line := range strings.Split(yaml, "\n") {
		if i := strings.Index(line, `- {name: "`); i >= 0 {
			rest := line[i+len(`- {name: "`):]
			names[rest[:strings.Index(rest, `"`)]]++
		}
	}
	if len(names) < 2 {
		t.Fatalf("expected two proxies, got %v", names)
	}
	for n, c := range names {
		if c > 1 {
			t.Errorf("Clash proxy name %q appears %d times — mihomo rejects the profile", n, c)
		}
	}

	// sing-box: the selector lists every tag, and a repeated one is refused outright.
	var cfg map[string]any
	if err := json.Unmarshal([]byte(SingBoxJSONMulti(u, servers)), &cfg); err != nil {
		t.Fatalf("sing-box profile is not JSON: %v", err)
	}
	tags := map[string]int{}
	for _, o := range cfg["outbounds"].([]any) {
		m := o.(map[string]any)
		if m["type"] == "selector" || m["type"] == "urltest" {
			continue
		}
		tags[m["tag"].(string)]++
	}
	for tag, c := range tags {
		if c > 1 {
			t.Errorf("sing-box tag %q appears %d times — the profile is refused", tag, c)
		}
	}
}

// A placeholder only substitutes as an ARRAY ELEMENT. The natural mistake — writing it
// as a map value — used to validate, render to itself, and hand every client a profile
// with the literal text {{proxies}} in it. Nothing failed, so nothing fell back and
// nothing was logged: the operator's only signal was users saying it does not work.
func TestTemplatePlaceholderMustBeSubstitutable(t *testing.T) {
	scalar := `{"log":{"level":"warn"},"outbounds":"{{proxies}}","route":{"final":"{{group}}"}}`
	if err := ValidateSingBoxTemplate(scalar); !errors.Is(err, ErrTemplateEmpty) {
		t.Errorf("a scalar {{proxies}} validated with %v, want ErrTemplateEmpty", err)
	}
	// And the render refuses it too, so a template stored before this rule cannot leak
	// its own placeholder text to a client.
	out, err := SingBoxWithTemplate(tplUser(), tplServers(), scalar)
	if err == nil {
		t.Error("a scalar {{proxies}} rendered without error")
	}
	if strings.Contains(out, TplProxies) {
		t.Error("the placeholder text reached the served profile")
	}
	if out != SingBoxJSONMulti(tplUser(), tplServers()) {
		t.Error("the render did not fall back to the generated profile")
	}

	// Same rule for the Xray template.
	if err := ValidateXrayTemplate(`{"outbounds":"{{outbounds}}","remarks":"{{remarks}}"}`); !errors.Is(err, ErrTemplateEmpty) {
		t.Errorf("a scalar {{outbounds}} validated with %v", err)
	}
}

// One servers slot, not many: each expansion re-emits the same outbound objects, tags
// included, and a duplicated tag makes sing-box and Xray refuse the whole profile.
// {{tags}} is the exception — a selector and a urltest both list them.
func TestTemplateRefusesASecondServersSlot(t *testing.T) {
	twice := `{"outbounds":[{"type":"selector","tag":"{{group}}","outbounds":["{{tags}}"]},"{{proxies}}","{{proxies}}"]}`
	if err := ValidateSingBoxTemplate(twice); !errors.Is(err, ErrTemplateTooBig) {
		t.Errorf("two {{proxies}} slots validated with %v, want a refusal", err)
	}
	if err := ValidateXrayTemplate(`{"outbounds":["{{outbounds}}","{{outbounds}}"]}`); !errors.Is(err, ErrTemplateTooBig) {
		t.Errorf("two {{outbounds}} slots validated with %v", err)
	}
	// Two {{tags}} is the shape an ordinary profile has, and stays valid.
	both := `{"outbounds":[{"type":"selector","tag":"{{group}}","outbounds":["{{tags}}"]},` +
		`{"type":"urltest","tag":"auto","outbounds":["{{tags}}"]},"{{proxies}}"]}`
	if err := ValidateSingBoxTemplate(both); err != nil {
		t.Errorf("selector + urltest was refused: %v", err)
	}
	// And it renders with every tag naming a real outbound.
	out, err := SingBoxWithTemplate(tplUser(), tplServers(), both)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	seen := map[string]int{}
	for _, o := range doc["outbounds"].([]any) {
		seen[o.(map[string]any)["tag"].(string)]++
	}
	for tag, n := range seen {
		if n > 1 {
			t.Errorf("tag %q emitted %d times", tag, n)
		}
	}
}
