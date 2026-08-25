package netguard

import (
	"net"
	"testing"
)

func TestValidateFetchURLShape(t *testing.T) {
	cases := []struct {
		url     string
		wantErr bool
	}{
		{"", true},
		{"   ", true},
		{"http://example.com/file", true},            // http forbidden
		{"ftp://example.com/file", true},             // ftp forbidden
		{"https://user:pass@example.com/file", true}, // credentials forbidden
		{"https://", true},                           // no host
		{"https://example.com/file.txt", false},
		{"https://api.github.com/repos", false},
	}

	for _, tc := range cases {
		err := validateFetchURLShape(tc.url)
		if (err != nil) != tc.wantErr {
			t.Errorf("validateFetchURLShape(%q) err = %v; wantErr = %v", tc.url, err, tc.wantErr)
		}
	}
}

func TestRejectPrivateIP(t *testing.T) {
	cases := []struct {
		ip      string
		wantErr bool
	}{
		{"127.0.0.1", true},             // loopback
		{"127.0.1.5", true},             // loopback
		{"::1", true},                   // ipv6 loopback
		{"10.0.0.1", true},              // private class A
		{"172.16.5.4", true},            // private class B
		{"192.168.1.1", true},           // private class C
		{"169.254.169.254", true},       // metadata
		{"169.254.1.1", true},           // link-local unicast
		{"224.0.0.1", true},             // multicast
		{"0.0.0.0", true},               // unspecified
		{"8.8.8.8", false},              // public IPv4
		{"1.1.1.1", false},              // public IPv4
		{"2606:4700:4700::1111", false}, // public IPv6
	}

	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("failed to parse IP %q", tc.ip)
		}
		err := rejectPrivateIP(ip)
		if (err != nil) != tc.wantErr {
			t.Errorf("rejectPrivateIP(%q) err = %v; wantErr = %v", tc.ip, err, tc.wantErr)
		}
	}
}

func TestValidateFetchURL(t *testing.T) {
	// Rejects private IP in URL directly
	if err := ValidateFetchURL("https://127.0.0.1/secrets"); err == nil {
		t.Error("ValidateFetchURL(127.0.0.1) expected error; got nil")
	}
	if err := ValidateFetchURL("https://10.0.0.1/internal"); err == nil {
		t.Error("ValidateFetchURL(10.0.0.1) expected error; got nil")
	}
	if err := ValidateFetchURL("https://169.254.169.254/latest/meta-data/"); err == nil {
		t.Error("ValidateFetchURL(169.254.169.254) expected error; got nil")
	}
}
