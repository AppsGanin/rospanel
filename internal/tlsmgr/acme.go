package tlsmgr

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/AppsGanin/rospanel/internal/tlsutil"
	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/http01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
)

// shortLivedProfile is Let's Encrypt's profile for IP-address certificates
// (issued as ~6-day certs, not 90-day).
const shortLivedProfile = "shortlived"

// zeroDirURL is the ZeroSSL ACME directory.
const zeroDirURL = "https://acme.zerossl.com/v2/DV90"

// FetchZeroSSLEAB calls the ZeroSSL public API to obtain External Account
// Binding credentials for the given email address. No authentication required —
// ZeroSSL issues EAB credentials per email on demand.
func FetchZeroSSLEAB(email string) (kid, hmacKey string, err error) {
	resp, err := http.PostForm(
		"https://api.zerossl.com/acme/eab-credentials-email",
		url.Values{"email": {email}},
	)
	if err != nil {
		return "", "", fmt.Errorf("zerossl eab api: %w", err)
	}
	defer resp.Body.Close()
	var body struct {
		Success    bool   `json:"success"`
		EABKeyID   string `json:"eab_kid"`
		EABHMACKey string `json:"eab_hmac_key"`
		Error      *struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", fmt.Errorf("zerossl eab api: decode: %w", err)
	}
	if !body.Success {
		errType := ""
		if body.Error != nil {
			errType = body.Error.Type
		}
		return "", "", fmt.Errorf("zerossl eab api: %s", strings.TrimSpace(errType))
	}
	return body.EABKeyID, body.EABHMACKey, nil
}

// acmeUser implements lego's registration.User.
type acmeUser struct {
	email string
	reg   *registration.Resource
	key   crypto.PrivateKey
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.reg }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

// ObtainACME runs the ACME HTTP-01 flow on port 80 and writes the resulting
// fullchain + key to certPath/keyPath. target is a domain OR an IP address.
// provider is "letsencrypt" (default) or "zerossl"; for ZeroSSL, eabKID and
// eabHMAC are the External Account Binding credentials from zerossl.com.
//
// Requires inbound :80 reachable for target (the panel binds it for
// /.well-known/acme-challenge/).
func ObtainACME(target, email, certPath, keyPath, acmeDir, provider, eabKID, eabHMAC string) error {
	target = strings.TrimSpace(target)
	isIP := net.ParseIP(target) != nil
	if !isIP {
		target = strings.ToLower(target)
	}

	accountKey, err := loadOrCreateAccountKey(accountKeyPath(acmeDir, provider))
	if err != nil {
		return err
	}
	user := &acmeUser{email: email, key: accountKey}

	cfg := lego.NewConfig(user)
	if provider == "zerossl" {
		cfg.CADirURL = zeroDirURL
	} else {
		cfg.CADirURL = lego.LEDirectoryProduction
	}
	cfg.Certificate.KeyType = certcrypto.EC256
	if isIP {
		cfg.Certificate.DisableCommonName = true // CN cannot be an IP address
	}

	client, err := lego.NewClient(cfg)
	if err != nil {
		return err
	}
	if err := client.Challenge.SetHTTP01Provider(http01.NewProviderServer("", "80")); err != nil {
		return err
	}

	var reg *registration.Resource
	if provider == "zerossl" && eabKID != "" && eabHMAC != "" {
		reg, err = client.Registration.RegisterWithExternalAccountBinding(
			registration.RegisterEABOptions{
				TermsOfServiceAgreed: true,
				Kid:                  eabKID,
				HmacEncoded:          eabHMAC,
			})
	} else {
		reg, err = client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	}
	if err != nil {
		return fmt.Errorf("acme register: %w", err)
	}
	user.reg = reg

	req := certificate.ObtainRequest{Domains: []string{target}, Bundle: true}
	// shortLivedProfile is LE-only: IP certs are 6 days; ZeroSSL issues 90-day certs.
	if isIP && provider != "zerossl" {
		req.Profile = shortLivedProfile
	}
	res, err := client.Certificate.Obtain(req)
	if err != nil {
		return fmt.Errorf("acme obtain for %s: %w", target, err)
	}
	return tlsutil.WriteKeyPair(certPath, keyPath, res.Certificate, res.PrivateKey)
}

// accountKeyPath returns the on-disk path for the ACME account private key.
// Each CA gets its own file so switching providers doesn't invalidate the other.
func accountKeyPath(acmeDir, provider string) string {
	if provider == "zerossl" {
		return filepath.Join(acmeDir, "zerossl_account.key")
	}
	return filepath.Join(acmeDir, "account.key") // legacy path — backward compatible
}

// loadOrCreateAccountKey persists the ACME account key at path so re-issuance
// reuses the same account (avoids new-account rate limits).
func loadOrCreateAccountKey(path string) (crypto.PrivateKey, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if raw, err := os.ReadFile(path); err == nil {
		if block, _ := pem.Decode(raw); block != nil {
			if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
				return key, nil
			}
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}
