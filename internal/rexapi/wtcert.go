package rexapi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// WebTransport serverCertificateHashes requires ECDSA P-256 certs
	// with validity <= 14 days. Use 10 days and rotate with 3 days left.
	wtCertLifetime    = 10 * 24 * time.Hour
	wtCertRotateEarly = 3 * 24 * time.Hour
)

type wtCertManager struct {
	dir  string
	mu   sync.Mutex
	cert tls.Certificate
	hash string
}

func newWTCertManager(dir string) *wtCertManager {
	return &wtCertManager{dir: dir}
}

func (m *wtCertManager) get() (tls.Certificate, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if wtCertValid(m.cert, time.Now()) {
		return m.cert, m.hash, nil
	}
	cert, hash, err := loadOrCreateWTCert(m.dir, time.Now())
	if err != nil {
		return tls.Certificate{}, "", err
	}
	m.cert = cert
	m.hash = hash
	return cert, hash, nil
}

func (m *wtCertManager) getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	cert, _, err := m.get()
	if err != nil {
		return nil, err
	}
	return &cert, nil
}

func wtCertHash(der []byte) string {
	sum := sha256.Sum256(der)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func wtCertValid(c tls.Certificate, now time.Time) bool {
	if len(c.Certificate) == 0 || c.Leaf == nil {
		return false
	}
	pub, ok := c.Leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return false
	}
	if now.Before(c.Leaf.NotBefore) {
		return false
	}
	if c.Leaf.NotAfter.Sub(c.Leaf.NotBefore) > 14*24*time.Hour {
		return false
	}
	return c.Leaf.NotAfter.Sub(now) > wtCertRotateEarly
}

func loadOrCreateWTCert(dir string, now time.Time) (tls.Certificate, string, error) {
	certPath := filepath.Join(dir, "wt-cert.pem")
	keyPath := filepath.Join(dir, "wt-key.pem")
	if cert, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		if len(cert.Certificate) > 0 {
			if leaf, err := x509.ParseCertificate(cert.Certificate[0]); err == nil {
				cert.Leaf = leaf
				if wtCertValid(cert, now) {
					return cert, wtCertHash(leaf.Raw), nil
				}
			}
		}
	}
	return createWTCert(dir, certPath, keyPath, now)
}

func createWTCert(dir, certPath, keyPath string, now time.Time) (tls.Certificate, string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, "", err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "gorex-webtransport"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(wtCertLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return tls.Certificate{}, "", fmt.Errorf("write %s: %w", certPath, err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, "", fmt.Errorf("write %s: %w", keyPath, err)
	}
	cert := tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
		Leaf:        leaf,
	}
	return cert, wtCertHash(der), nil
}
