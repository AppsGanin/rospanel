package model

import (
	"regexp"
	"strings"
)

// Subscription response rules: operator-defined overrides evaluated BEFORE the
// automatic User-Agent format detection. Each request is matched against the rules in
// order; the first match decides the response — force a specific format, or block the
// client (served the decoy, so a scraper learns nothing). No match falls through to
// the normal auto-detection. This is the "serve a different config to a specific
// app/OS/version" mechanism Remnawave (SRR) and Marzneshin expose.

// Sub-rule match fields — the request attributes a rule can test. Kept to a known set
// (the User-Agent plus the HWID headers the subscription convention already defines)
// rather than an arbitrary header name, so a rule can't be written against a header
// that never arrives and silently never fire.
const (
	SubMatchUserAgent   = "user_agent"
	SubMatchDeviceOS    = "device_os"    // x-device-os
	SubMatchVerOS       = "ver_os"       // x-ver-os
	SubMatchDeviceModel = "device_model" // x-device-model
)

// Sub-rule operators.
const (
	SubOpContains   = "contains"
	SubOpEquals     = "equals"
	SubOpPrefix     = "prefix"
	SubOpRegex      = "regex"
	SubOpNotContain = "not_contains"
)

// Sub-rule actions: a format to force, or "block".
const (
	SubActionV2ray    = "v2ray"
	SubActionClash    = "clash"
	SubActionSingbox  = "singbox"
	SubActionXrayJSON = "xray-json" // full Xray configs for Xray-core clients (fragment/noise ride here)
	SubActionBlock    = "block"
)

// SubRule is one response rule. Enabled rules are evaluated top to bottom.
type SubRule struct {
	Field   string `json:"field"`
	Op      string `json:"op"`
	Value   string `json:"value"`
	Action  string `json:"action"`
	Enabled bool   `json:"enabled"`
}

var (
	subMatchFields = map[string]bool{
		SubMatchUserAgent: true, SubMatchDeviceOS: true,
		SubMatchVerOS: true, SubMatchDeviceModel: true,
	}
	subOps = map[string]bool{
		SubOpContains: true, SubOpEquals: true, SubOpPrefix: true,
		SubOpRegex: true, SubOpNotContain: true,
	}
	subActions = map[string]bool{
		SubActionV2ray: true, SubActionClash: true,
		SubActionSingbox: true, SubActionXrayJSON: true, SubActionBlock: true,
	}
)

// Valid reports whether a rule is well-formed (known field/op/action, a value, and a
// compilable pattern for the regex op). Invalid rules are rejected on save so a typo
// can't sit silently never matching.
func (r SubRule) Valid() error {
	if !subMatchFields[r.Field] {
		return fieldErr("err.subRuleField", "неизвестное поле правила {{value}}", map[string]any{"value": r.Field})
	}
	if !subOps[r.Op] {
		return fieldErr("err.subRuleOp", "неизвестный оператор правила {{value}}", map[string]any{"value": r.Op})
	}
	if !subActions[r.Action] {
		return fieldErr("err.subRuleAction", "неизвестное действие правила {{value}}", map[string]any{"value": r.Action})
	}
	if strings.TrimSpace(r.Value) == "" {
		return fieldErr("err.subRuleValue", "укажите значение для правила")
	}
	if r.Op == SubOpRegex {
		if _, err := regexp.Compile(r.Value); err != nil {
			return fieldErr("err.subRuleRegex", "неверное регулярное выражение: {{value}}", map[string]any{"value": err.Error()})
		}
	}
	return nil
}

// SubRuleInput are the per-request header values a rule set is evaluated against.
type SubRuleInput struct {
	UserAgent   string
	DeviceOS    string
	VerOS       string
	DeviceModel string
}

func (in SubRuleInput) field(name string) string {
	switch name {
	case SubMatchUserAgent:
		return in.UserAgent
	case SubMatchDeviceOS:
		return in.DeviceOS
	case SubMatchVerOS:
		return in.VerOS
	case SubMatchDeviceModel:
		return in.DeviceModel
	}
	return ""
}

// matches reports whether one rule fires for the given input. Comparisons are
// case-insensitive except the regex op, which uses the pattern as written (a caller
// wanting case-insensitivity writes `(?i)`).
func (r SubRule) matches(in SubRuleInput) bool {
	subject := in.field(r.Field)
	switch r.Op {
	case SubOpContains:
		return strings.Contains(strings.ToLower(subject), strings.ToLower(r.Value))
	case SubOpNotContain:
		return !strings.Contains(strings.ToLower(subject), strings.ToLower(r.Value))
	case SubOpEquals:
		return strings.EqualFold(strings.TrimSpace(subject), strings.TrimSpace(r.Value))
	case SubOpPrefix:
		return strings.HasPrefix(strings.ToLower(subject), strings.ToLower(r.Value))
	case SubOpRegex:
		re, err := regexp.Compile(r.Value)
		return err == nil && re.MatchString(subject)
	}
	return false
}

// EvalSubRules returns the action of the first enabled rule that fires, or "" when
// none do — the signal to fall through to automatic format detection.
func EvalSubRules(rules []SubRule, in SubRuleInput) string {
	for _, r := range rules {
		if r.Enabled && r.matches(in) {
			return r.Action
		}
	}
	return ""
}
