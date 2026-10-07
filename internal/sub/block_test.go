package sub

import (
	"strings"
	"testing"
)

// A plugin's block reaches the page as the panel renders it: Markdown without raw
// HTML, text escaped, and buttons to https only.
func TestNewBlock(t *testing.T) {
	md, ok := NewBlock("markdown", "**Hi** <script>x()</script>", "", "")
	if !ok || !strings.Contains(string(md.HTML), "<strong>Hi</strong>") || strings.Contains(string(md.HTML), "<script>") {
		t.Fatalf("markdown: %q", md.HTML)
	}
	txt, ok := NewBlock("notice", "<b>5 days</b> left", "", "")
	if !ok || string(txt.HTML) != "&lt;b&gt;5 days&lt;/b&gt; left" {
		t.Fatalf("notice: %q", txt.HTML)
	}
	if _, ok := NewBlock("button", "", "Pay", "javascript:alert(1)"); ok {
		t.Fatal("a javascript: button was accepted")
	}
	if _, ok := NewBlock("button", "", "Pay", "http://pay.example"); ok {
		t.Fatal("a plain-http button was accepted")
	}
	if b, ok := NewBlock("button", "", "Pay", "https://pay.example"); !ok || b.URL != "https://pay.example" {
		t.Fatal("an https button was refused")
	}
	if _, ok := NewBlock("iframe", "x", "", ""); ok {
		t.Fatal("an unknown type was accepted")
	}
}
