package extsub

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"
	"testing"
)

// The public key Happ publishes for panels to build happ://crypt4/ links with, as
// shipped by the library remnawave generates its links from (@kastov/cryptohapp,
// crypt4.constant). A link encrypted to any other key opens nowhere, and the app
// only fails it silently — so the key is pinned to the published one, not to the
// private key it happens to be derived from here.
const happCrypt4PublishedKey = `-----BEGIN PUBLIC KEY-----
MIICIjANBgkqhkiG9w0BAQEFAAOCAg8AMIICCgKCAgEA3UZ0M3L4K+WjM3vkbQnz
ozHg/cRbEXvQ6i4A8RVN4OM3rK9kU01FdjyoIgywve8OEKsFnVwERZAQZ1Trv60B
hmaM76QQEE+EUlIOL9EpwKWGtTL5lYC1sT9XJMNP3/CI0gP5wwQI88cY/xedpOEB
W72EmOOShHUm/b/3m+HPmqwc4ugKj5zWV5SyiT829aFA5DxSjmIIFBAms7DafmSq
LFTYIQL5cShDY2u+/sqyAw9yZIOoqW2TFIgIHhLPWek/ocDU7zyOrlu1E0SmcQQb
LFqHq02fsnH6IcqTv3N5Adb/CkZDDQ6HvQVBmqbKZKf7ZdXkqsc/Zw27xhG7OfXC
tUmWsiL7zA+KoTd3avyOh93Q9ju4UQsHthL3Gs4vECYOCS9dsXXSHEY/1ngU/hjO
WFF8QEE/rYV6nA4PTyUvo5RsctSQL/9DJX7XNh3zngvif8LsCN2MPvx6X+zLouBX
zgBkQ9DFfZAGLWf9TR7KVjZC/3NsuUCDoAOcpmN8pENBbeB0puiKMMWSvll36+2M
YR1Xs0MgT8Y9TwhE2+TnnTJOhzmHi/BxiUlY/w2E0s4ax9GHAmX0wyF4zeV7kDkc
vHuEdc0d7vDmdw0oqCqWj0Xwq86HfORu6tm1A8uRATjb4SzjTKclKuoElVAVa5Jo
oh/uZMozC65SmDw+N5p6Su8CAwEAAQ==
-----END PUBLIC KEY-----`

func TestHappCrypt4EncryptsToThePublishedKey(t *testing.T) {
	block, _ := pem.Decode([]byte(happCrypt4PublishedKey))
	if block == nil {
		t.Fatal("the pinned key does not parse as PEM")
	}
	published, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key, err := happRSAKey(3)
	if err != nil {
		t.Fatal(err)
	}
	if !key.PublicKey.Equal(published) {
		t.Fatal("crypt4 links are encrypted to a key that is not the one Happ publishes")
	}
}

func TestHappCrypt4RoundTrip(t *testing.T) {
	const url = "https://vpn.example.com/sub/Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MA"
	link, err := EncryptHapp(url)
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := strings.CutPrefix(link, "happ://crypt4/")
	if !ok {
		t.Fatalf("link %q is not a crypt4 link", link)
	}
	// Standard base64, as the app and remnawave's generator write it.
	if _, err := base64.StdEncoding.DecodeString(payload); err != nil {
		t.Errorf("payload is not standard base64: %v", err)
	}
	plain, err := DecryptHapp(link)
	if err != nil {
		t.Fatalf("our own link does not decrypt: %v", err)
	}
	if string(plain) != url {
		t.Errorf("decrypted %q, want %q", plain, url)
	}
	// Random padding: the same address never makes the same link twice, so a link
	// in one place says nothing about a link in another.
	if again, _ := EncryptHapp(url); again == link {
		t.Error("two encryptions of one address produced the same link")
	}
}

func TestHappCrypt4RefusesWhatDoesNotFitOneBlock(t *testing.T) {
	if _, err := EncryptHapp(strings.Repeat("a", happCryptMax)); err != nil {
		t.Errorf("%d bytes should fit: %v", happCryptMax, err)
	}
	if _, err := EncryptHapp(strings.Repeat("a", happCryptMax+1)); err == nil {
		t.Error("a plaintext past one RSA block was accepted")
	}
}
