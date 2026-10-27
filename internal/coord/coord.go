// Package coord is GoRex's network coordinator: the mini control plane
// that turns pairwise remote access into a network the way Tailscale
// does. A machine joins once with a network key; every member then
// trusts and finds every other member, without pairing windows or
// addresses copied by hand. See docs/coord.md.
//
// The coordinator holds only the member directory. Sessions and all
// terminal traffic stay on the tailcat data plane, end to end; this
// package never sees them. The wire side lives in internal/coordproto,
// shared with the machines that follow a network.
package coord

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorex/internal/coordproto"
)

// DefaultPort is the port the coordinator listens on: HTTPS's own, so
// a join URL reads without a port.
const DefaultPort = 443

// MaxName is the longest member name; a longer one is cut.
const MaxName = coordproto.MaxName

// state is the coordinator's whole memory, kept in network.json.
type state struct {
	// KeyMAC is SHA-256 of the network key under a fixed label: the
	// key is 32 random bytes, far past brute force, so a slow KDF
	// would only tax every poll; the label keeps the digest from
	// being reused elsewhere.
	KeyMAC   string              `json:"keyMAC"`
	Revision int                 `json:"revision"`
	Members  []coordproto.Member `json:"members"`
}

// keyMAC is the digest of the network key that state keeps.
func keyMAC(networkKey string) []byte {
	h := sha256.Sum256(append([]byte("gorex-coord-v1\x00"), networkKey...))
	return h[:]
}

// checkKey tells whether s is the network key, in constant time.
func checkKey(s *state, networkKey string) bool {
	mac := keyMAC(networkKey)
	want, err := hex.DecodeString(s.KeyMAC)
	return len(networkKey) > 0 && err == nil && subtle.ConstantTimeCompare(mac, want) == 1
}

// loadState reads the state file; a missing one is an error, for the
// coordinator only serves after init.
func loadState(dir string) (*state, error) {
	b, err := os.ReadFile(filepath.Join(dir, "network.json"))
	if err != nil {
		return nil, fmt.Errorf("coord: reading the network state: %w", err)
	}
	var s state
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("coord: reading the network state: %w", err)
	}
	if len(s.KeyMAC) != sha256.Size*2 {
		return nil, errors.New("coord: the network state holds no network key")
	}
	return &s, nil
}

// save writes the state to dir, readable by the user only, atomically.
func (s *state) save(dir string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "network.json.tmp")
	os.Remove(tmp)
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, "network.json")); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// CleanName cleans the name a machine joins with: printable characters
// only, at most MaxName of them.
func CleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r >= 0x20 && r < 0x7f {
			return r
		}
		return -1
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > MaxName {
		s = strings.TrimSpace(string(r[:MaxName]))
	}
	return s
}

// Init makes a network in dir: a network key, shown once to the caller
// and never written in the clear, a certificate to pin, and the state.
// An existing state is left alone; re-running init on a live directory
// would fork the network.
func Init(dir string) (networkKey, fingerprint string, err error) {
	if _, err := os.Stat(filepath.Join(dir, "network.json")); err == nil {
		return "", "", errors.New("coord: the directory already holds a network")
	}
	networkKey, err = NewNetworkKey()
	if err != nil {
		return "", "", err
	}
	der, keyDER, err := NewCertificate()
	if err != nil {
		return "", "", err
	}
	if err = (&state{KeyMAC: hex.EncodeToString(keyMAC(networkKey))}).save(dir); err != nil {
		return "", "", err
	}
	if err = WriteCert(dir, der, keyDER); err != nil {
		return "", "", err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return "", "", err
	}
	return networkKey, coordproto.CertFingerprint(cert), nil
}

// Members reads the directory without serving, for the coordinator's
// own command line.
func Members(dir string) ([]coordproto.Member, error) {
	st, err := loadState(dir)
	if err != nil {
		return nil, err
	}
	return st.Members, nil
}

// NewNetworkKey returns a network key, 32 random bytes as base64: the
// secret every member holds. Whoever holds it may run commands on every
// machine of the network, so it is never logged.
func NewNetworkKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewCertificate makes the coordinator's self-signed certificate and
// its private key, valid ten years; clients pin the fingerprint
// instead of trusting an authority.
func NewCertificate() (der, keyDER []byte, err error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "gorex-coord"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err = x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &k.PublicKey, k)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err = x509.MarshalECPrivateKey(k)
	return der, keyDER, err
}

// WriteCert writes a certificate and its key to dir, the key readable
// by the user only.
func WriteCert(dir string, der, keyDER []byte) error {
	crt := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	key := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "tls.crt"), crt, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "tls.key"), key, 0o600)
}
