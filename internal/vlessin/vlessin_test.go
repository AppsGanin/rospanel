package vlessin

import (
	"bytes"
	"net"
	"testing"
)

func TestAppendRequest(t *testing.T) {
	id, ok := ParseUUID("11111111-2222-3333-4444-555555555555")
	if !ok {
		t.Fatal("uuid")
	}
	head := append([]byte{0}, id[:]...)
	head = append(head, 0, 1, 0x01, 0xbb)
	for host, tail := range map[string][]byte{
		"1.2.3.4":        {1, 1, 2, 3, 4},
		"::ffff:1.2.3.4": {1, 1, 2, 3, 4},
		"2001:db8::1":    append([]byte{3}, net.ParseIP("2001:db8::1").To16()...),
		"example.com":    append([]byte{2, 11}, "example.com"...),
	} {
		got := AppendRequest(nil, id, CmdTCP, host, 443)
		if want := append(append([]byte(nil), head...), tail...); !bytes.Equal(got, want) {
			t.Errorf("%s: % x, want % x", host, got, want)
		}
	}
	if _, ok := ParseUUID("not-a-uuid"); ok {
		t.Error("parsed a malformed uuid")
	}
}

func TestProxyHeader(t *testing.T) {
	for remote, want := range map[string]string{
		"203.0.113.9:5555":        "PROXY TCP4 203.0.113.9 127.0.0.1 5555 443\r\n",
		"[::ffff:203.0.113.9]:80": "PROXY TCP4 203.0.113.9 127.0.0.1 80 443\r\n",
		"[2001:db8::1]:7":         "PROXY TCP6 2001:db8::1 ::1 7 443\r\n",
		"[fe80::1%eth0]:7":        "PROXY TCP6 fe80::1 ::1 7 443\r\n",
		"@":                       "PROXY UNKNOWN\r\n",
	} {
		if got := string(ProxyHeader(remote)); got != want {
			t.Errorf("%s: %q, want %q", remote, got, want)
		}
	}
}

func TestReadResponse(t *testing.T) {
	if !ReadResponse(bytes.NewReader([]byte{0, 2, 9, 9, 'x'})) {
		t.Error("a response with addons was refused")
	}
	if ReadResponse(bytes.NewReader([]byte{1, 0})) || ReadResponse(bytes.NewReader([]byte{0})) {
		t.Error("a bad or short response was taken")
	}
}
