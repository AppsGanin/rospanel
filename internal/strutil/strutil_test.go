package strutil

import (
	"testing"
)

func TestEscHTML(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"hello", "hello"},
		{"<script>alert(1)</script>", "&lt;script&gt;alert(1)&lt;/script&gt;"},
		{"a & b", "a &amp; b"},
		{"\"quote\"", "&#34;quote&#34;"},
	}
	for _, tc := range cases {
		if got := EscHTML(tc.in); got != tc.want {
			t.Errorf("EscHTML(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTruncateName(t *testing.T) {
	cases := []struct {
		in       string
		maxRunes int
		want     string
	}{
		{"  Alice  ", 10, "Alice"},
		{"Пользователь", 6, "Пользо"},
		{"Short", 10, "Short"},
		{"Exact", 5, "Exact"},
		{"", 5, ""},
	}
	for _, tc := range cases {
		if got := TruncateName(tc.in, tc.maxRunes); got != tc.want {
			t.Errorf("TruncateName(%q, %d) = %q, want %q", tc.in, tc.maxRunes, got, tc.want)
		}
	}
}
