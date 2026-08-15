package model

import "testing"

func TestEvalSubRules(t *testing.T) {
	rules := []SubRule{
		{Field: SubMatchUserAgent, Op: SubOpContains, Value: "happ", Action: SubActionSingbox, Enabled: true},
		{Field: SubMatchDeviceOS, Op: SubOpEquals, Value: "ios", Action: SubActionClash, Enabled: true},
		{Field: SubMatchUserAgent, Op: SubOpRegex, Value: `(?i)v2rayng/1\.[0-7]`, Action: SubActionV2ray, Enabled: true},
		{Field: SubMatchUserAgent, Op: SubOpContains, Value: "curl", Action: SubActionBlock, Enabled: true},
		{Field: SubMatchUserAgent, Op: SubOpContains, Value: "disabled", Action: SubActionClash, Enabled: false},
	}
	cases := []struct {
		name string
		in   SubRuleInput
		want string
	}{
		{"first match wins (contains)", SubRuleInput{UserAgent: "Happ/2.0"}, SubActionSingbox},
		{"case-insensitive equals on device os", SubRuleInput{DeviceOS: "iOS"}, SubActionClash},
		{"regex on old v2rayng", SubRuleInput{UserAgent: "v2rayNG/1.6.5"}, SubActionV2ray},
		{"newer v2rayng doesn't match the regex", SubRuleInput{UserAgent: "v2rayNG/1.9.0"}, ""},
		{"block a scraper", SubRuleInput{UserAgent: "curl/8.4"}, SubActionBlock},
		{"disabled rule never fires", SubRuleInput{UserAgent: "disabled-client"}, ""},
		{"no match falls through", SubRuleInput{UserAgent: "Mozilla/5.0"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EvalSubRules(rules, c.in); got != c.want {
				t.Errorf("EvalSubRules = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSubRuleValid(t *testing.T) {
	good := SubRule{Field: SubMatchUserAgent, Op: SubOpContains, Value: "x", Action: SubActionClash}
	if err := good.Valid(); err != nil {
		t.Errorf("valid rule rejected: %v", err)
	}
	bad := []SubRule{
		{Field: "nope", Op: SubOpContains, Value: "x", Action: SubActionClash},
		{Field: SubMatchUserAgent, Op: "nope", Value: "x", Action: SubActionClash},
		{Field: SubMatchUserAgent, Op: SubOpContains, Value: "x", Action: "nope"},
		{Field: SubMatchUserAgent, Op: SubOpContains, Value: " ", Action: SubActionClash},
		{Field: SubMatchUserAgent, Op: SubOpRegex, Value: "(", Action: SubActionClash},
	}
	for i, r := range bad {
		if err := r.Valid(); err == nil {
			t.Errorf("bad rule %d accepted", i)
		}
	}
}
