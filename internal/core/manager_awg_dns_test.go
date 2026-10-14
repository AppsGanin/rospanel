package core

import (
	"errors"
	"testing"
)

func TestValidateAWGUpdateRefusesPrivateDNS(t *testing.T) {
	for _, dns := range []string{"1.1.1.1", "1.1.1.1, 2606:4700:4700::1111", "10.66.0.1", ""} {
		if _, _, err := validateAWGUpdate(51820, dns); err != nil {
			t.Errorf("%q refused: %v", dns, err)
		}
	}
	for _, dns := range []string{"10.0.0.2", "1.1.1.1, 192.168.1.1", "fd00::1", "169.254.169.253"} {
		_, _, err := validateAWGUpdate(51820, dns)
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Code != "err.awgDNSPrivate" {
			t.Errorf("%q: err = %v, want err.awgDNSPrivate", dns, err)
		}
	}
}
