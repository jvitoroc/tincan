// Package transport owns the server's QUIC listener and its TLS certificate.
package transport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/jvitoroc/tincan/internal/proto"
	"github.com/quic-go/quic-go"
)

const (
	certFile = "cert.pem"
	keyFile  = "key.pem"
)

// LoadOrCreateCert loads the server's self-signed certificate from dir, creating
// one on first run. Clients pin its fingerprint, so it must survive restarts:
// deleting it makes every client see a "certificate changed" warning.
func LoadOrCreateCert(dir string) (tls.Certificate, error) {
	certPath, keyPath := filepath.Join(dir, certFile), filepath.Join(dir, keyFile)
	certExists, keyExists := exists(certPath), exists(keyPath)
	switch {
	case certExists && keyExists:
		return tls.LoadX509KeyPair(certPath, keyPath)
	case certExists || keyExists:
		return tls.Certificate{}, fmt.Errorf("found only one of %s and %s in %s; restore the other, or remove both to generate a new certificate (clients will then see a fingerprint warning)", certFile, keyFile, dir)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, err
	}
	certPEM, keyPEM, err := generate()
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, os.ErrNotExist)
}

func generate() (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "tincan-server"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(20, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// Fingerprint returns the fingerprint clients will pin for cert.
func Fingerprint(cert tls.Certificate) string {
	return proto.Fingerprint(cert.Certificate[0])
}

// QUICConfig is shared by the server and client so both sides agree on datagram
// support and keep idle connections alive through NAT.
func QUICConfig() *quic.Config {
	return &quic.Config{
		EnableDatagrams: true,
		KeepAlivePeriod: 15 * time.Second,
		MaxIdleTimeout:  45 * time.Second,
	}
}

// Listen starts accepting QUIC connections on addr (UDP).
func Listen(addr string, cert tls.Certificate) (*quic.Listener, error) {
	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{proto.ALPN},
		MinVersion:   tls.VersionTLS13,
	}
	return quic.ListenAddr(addr, tlsConf, QUICConfig())
}
