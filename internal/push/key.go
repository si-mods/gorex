// Package push is the host's sender of Live Activity updates through the
// push relay of docs/NET-003-push-relay.md: it keeps the host's push key,
// builds the terminal's content state, and posts signed updates to the
// relay, coalesced per device.
package push

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gorex/internal/atomicfile"
)

// Keypair is the host's push key, the P-256 pair of NET-003's parties:
// the relay checks a push's signature against the public part a phone
// bound to it.
type Keypair struct {
	priv *ecdsa.PrivateKey
	pub  string
}

// LoadOrCreate reads the key of dir's push-key.json, or makes one. The
// file is the private key's PKCS8 DER in base64, readable by the user
// only; on Windows its ACL is the file system's, as for the other
// secrets of the data dir.
func LoadOrCreate(dir string) (*Keypair, error) {
	path := filepath.Join(dir, "push-key.json")
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		var stored struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(b, &stored); err != nil {
			return nil, fmt.Errorf("push: reading %s: %w", filepath.Base(path), err)
		}
		der, err := base64.StdEncoding.DecodeString(stored.Key)
		if err != nil {
			return nil, fmt.Errorf("push: reading %s: %w", filepath.Base(path), err)
		}
		k, err := x509.ParsePKCS8PrivateKey(der)
		if err != nil {
			return nil, fmt.Errorf("push: reading %s: %w", filepath.Base(path), err)
		}
		priv, ok := k.(*ecdsa.PrivateKey)
		if !ok || priv.Curve != elliptic.P256() {
			return nil, fmt.Errorf("push: %s holds no P-256 key", filepath.Base(path))
		}
		return keypair(priv)
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	b, err = json.Marshal(map[string]string{"key": base64.StdEncoding.EncodeToString(der)})
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := atomicfile.Write(path, b, 0o600); err != nil {
		return nil, err
	}
	return keypair(priv)
}

// keypair wraps a private key, its public part in the form the phone
// relays to the relay.
func keypair(priv *ecdsa.PrivateKey) (*Keypair, error) {
	// PKIX DER in base64: the relay needs a form it can parse without
	// knowing the curve, and PKIX names it.
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, err
	}
	return &Keypair{priv: priv, pub: base64.StdEncoding.EncodeToString(der)}, nil
}

// Public returns the public key.
func (k *Keypair) Public() *ecdsa.PublicKey { return &k.priv.PublicKey }

// PublicB64 is the public key as gorex.push.register and the relay carry
// it: base64 of its PKIX DER.
func (k *Keypair) PublicB64() string { return k.pub }

// Sign signs the body of a request to the relay, ASN.1 DER over SHA-256.
func (k *Keypair) Sign(body []byte) ([]byte, error) {
	sum := sha256.Sum256(body)
	return ecdsa.SignASN1(rand.Reader, k.priv, sum[:])
}
