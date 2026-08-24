package updater

import (
	"reflect"
	"testing"
)

func TestRepoConstant(t *testing.T) {
	const want = "Shu1t3/rospanel-shu1t3"
	if Repo != want {
		t.Errorf("Repo = %q, want %q", Repo, want)
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		latest  string
		current string
		want    bool
	}{
		{latest: "2.10.1", current: "2.10.0", want: true},
		{latest: "2.11.0", current: "2.10.9", want: true},
		{latest: "3.0.0", current: "2.10.0", want: true},
		{latest: "v2.10.1", current: "2.10.0", want: true},
		{latest: "2.10.1", current: "v2.10.0", want: true},
		{latest: "v2.10.1", current: "v2.10.0", want: true},
		{latest: "2.10.0", current: "2.10.0", want: false},
		{latest: "v2.10.0", current: "v2.10.0", want: false},
		{latest: "2.9.1", current: "2.10.0", want: false},
		{latest: "1.0.0", current: "2.0.0", want: false},
		{latest: "2.10.0-beta.1", current: "2.10.0", want: false},
		{latest: "2.10.1-rc1", current: "2.10.0", want: true},
		{latest: "2.10.0.1", current: "2.10.0", want: true},
		{latest: "2.10", current: "2.10.0", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.latest+"_vs_"+tt.current, func(t *testing.T) {
			got := IsNewer(tt.latest, tt.current)
			if got != tt.want {
				t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
			}
		})
	}
}

func TestSplitVer(t *testing.T) {
	tests := []struct {
		input string
		want  []int
	}{
		{input: "2.10.1", want: []int{2, 10, 1}},
		{input: "v2.10.1", want: []int{2, 10, 1}},
		{input: "  v1.2.3-beta.1  ", want: []int{1, 2, 3}},
		{input: "v2.0.0+build123", want: []int{2, 0, 0}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitVer(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitVer(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
