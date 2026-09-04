package sub

import (
	"encoding/json"
	"errors"
	"strings"
)

// Operator-editable subscription templates.
//
// The panel's generated profiles are deliberately minimal — enough routing to work
// anywhere, nothing opinionated. An operator who wants their own DNS, their own rule
// set or their own group layout had one option before this: the mihomo template URL,
// and nothing at all for sing-box or Xray JSON.
//
// A template is the operator's own document with placeholders where the panel's
// per-user parts go. That shape rather than a template language on purpose: the
// values being injected are objects the panel already builds and already knows are
// valid, so nothing an operator writes can produce a proxy entry that a client
// rejects — the worst they can do is write invalid JSON, which is caught when they
// save it and again before it is served.
//
// The failure rule is absolute: a client that cannot parse a profile drops ALL of it,
// so a broken template must never reach one. Every path here falls back to the
// generated profile and says so in the log rather than serving something malformed.
const (
	// TplProxies is an ARRAY ELEMENT replaced by the generated proxy outbounds.
	// ["{{proxies}}"] becomes the list itself, not a list containing a list.
	TplProxies = "{{proxies}}"
	// TplTags is an array element replaced by the generated proxies' tags, in the
	// same order — what a selector or urltest group lists.
	TplTags = "{{tags}}"
	// TplGroup is a scalar replaced by the profile's group name (the subscription
	// title), which is what route.final and a selector's tag want to be.
	TplGroup = "{{group}}"
	// TplOutbounds is an array element replaced by one lane's whole outbound chain —
	// the proxy, its optional fragment/noise dialer, direct and block. Xray JSON only,
	// where the profile is one config PER LANE rather than one config for all of them.
	TplOutbounds = "{{outbounds}}"
	// TplRemarks is a scalar replaced by one lane's display name. Xray JSON only.
	TplRemarks = "{{remarks}}"
)

// ErrTemplateEmpty is returned when a template has none of the placeholders it needs,
// which would produce a profile with no servers in it — valid, parseable, and useless.
var ErrTemplateEmpty = errors.New("template has no placeholder for the servers")

// spliceJSON renders a JSON template: it walks the decoded document replacing scalar
// placeholders with their values, and array elements that are list placeholders with
// the elements themselves.
//
// Walking the decoded document rather than substituting in the text is what keeps an
// operator's own strings out of the substitution: a rule that happens to contain
// "{{group}}" as part of a domain is a string value like any other and is replaced by
// the group name, which is what they asked for — but a value being injected can never
// be re-scanned for placeholders, so nothing recurses and nothing escapes.
func spliceJSON(doc any, scalars map[string]any, lists map[string][]any) any {
	switch v := doc.(type) {
	case string:
		if val, ok := scalars[v]; ok {
			return val
		}
		return v
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				if elems, isList := lists[s]; isList {
					out = append(out, elems...)
					continue
				}
			}
			out = append(out, spliceJSON(item, scalars, lists))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = spliceJSON(item, scalars, lists)
		}
		return out
	}
	return doc
}

// renderJSONTemplate parses a template, splices the values in and returns the result
// as indented JSON. An unparseable template is an error, not a half-rendered profile.
func renderJSONTemplate(tpl string, scalars map[string]any, lists map[string][]any) (string, error) {
	var doc any
	if err := json.Unmarshal([]byte(tpl), &doc); err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(spliceJSON(doc, scalars, lists), "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// ValidateSingBoxTemplate checks a sing-box template the way the renderer will use it:
// it must be JSON, and it must place the proxies somewhere. Run when the operator
// saves, so a broken template is refused at the keyboard rather than discovered by a
// user whose client silently has no servers.
func ValidateSingBoxTemplate(tpl string) error {
	return validateJSONTemplate(tpl, TplProxies)
}

// ValidateXrayTemplate is the same check for the Xray JSON template, whose per-lane
// placeholder is the outbound chain.
func ValidateXrayTemplate(tpl string) error {
	return validateJSONTemplate(tpl, TplOutbounds)
}

func validateJSONTemplate(tpl, required string) error {
	if strings.TrimSpace(tpl) == "" {
		return nil // empty means "use the generated profile", which is always valid
	}
	var doc any
	if err := json.Unmarshal([]byte(tpl), &doc); err != nil {
		return err
	}
	if !hasPlaceholder(doc, required) {
		return ErrTemplateEmpty
	}
	return nil
}

// hasPlaceholder reports whether the decoded document contains the placeholder as a
// string value anywhere. Checked on the decoded form, not the text, so a placeholder
// that only appears inside a comment-like key the renderer never visits does not
// count as present.
func hasPlaceholder(doc any, want string) bool {
	switch v := doc.(type) {
	case string:
		return v == want
	case []any:
		for _, item := range v {
			if hasPlaceholder(item, want) {
				return true
			}
		}
	case map[string]any:
		for _, item := range v {
			if hasPlaceholder(item, want) {
				return true
			}
		}
	}
	return false
}

// ValidateClashTemplate checks a mihomo template. It is YAML, which the panel does not
// parse — the injection is textual, keyed on the markers the RoscomVPN-style templates
// already carry — so the only thing that can be checked is that the marker is there.
func ValidateClashTemplate(tpl string) error {
	if strings.TrimSpace(tpl) == "" {
		return nil
	}
	if !strings.Contains(tpl, clashProxiesMarker) {
		return ErrTemplateEmpty
	}
	return nil
}
