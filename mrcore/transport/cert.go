// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// certLifetime is deliberately long: this identity is meant to live for as
// long as the machine or key holder it names does, and there is no
// revocation or rotation story yet.
const certLifetime = 30 * 365 * 24 * time.Hour

// LoadOrCreateCert loads a persistent TLS identity from path, a single PEM
// file holding both a private key and a self-signed certificate. If path
// does not exist, it generates a new ECDSA P-256 key (chosen for
// consistency with the rest of this project's P-256 use in mrcore; nothing
// about the Syncthing Device ID derivation cares which curve or algorithm
// is used here) and a self-signed certificate, writes both PEM-encoded to
// path (creating parent directories as needed), and returns the result.
//
// The certificate's subject is never checked by anything: only its DER
// bytes matter, since protocol.NewDeviceID(cert.Certificate[0]) hashes them
// into this identity's Device ID. That Device ID must stay STABLE across
// restarts, or every Recipient.Transport / AuthorizedPeers comparison
// recorded against it silently stops matching. That is why the actual
// generated certificate bytes are what get persisted and reloaded here,
// never regenerated from a persisted key on each call: a freshly generated
// certificate gets a fresh serial number and NotBefore timestamp, and
// would hash to a different Device ID every time.
func LoadOrCreateCert(path string) (tls.Certificate, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		cert, err := tls.X509KeyPair(data, data)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("transport: loading identity from %s: %w", path, err)
		}
		return cert, nil
	}
	if !os.IsNotExist(err) {
		return tls.Certificate{}, fmt.Errorf("transport: reading %s: %w", path, err)
	}

	cert, pemData, err := generateCert()
	if err != nil {
		return tls.Certificate{}, err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return tls.Certificate{}, fmt.Errorf("transport: creating directory for %s: %w", path, err)
	}
	if err := os.WriteFile(path, pemData, 0o600); err != nil {
		return tls.Certificate{}, fmt.Errorf("transport: writing %s: %w", path, err)
	}

	return cert, nil
}

// generateCert creates a fresh ECDSA P-256 key and self-signed certificate,
// returning both the usable tls.Certificate and its PEM encoding (a
// CERTIFICATE block followed by an EC PRIVATE KEY block) ready to write to
// disk as one file.
func generateCert() (tls.Certificate, []byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("transport: generating key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("transport: generating serial number: %w", err)
	}

	now := time.Now()
	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "synctang"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(certLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("transport: creating certificate: %w", err)
	}

	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("transport: marshalling private key: %w", err)
	}

	var buf bytes.Buffer
	if err := pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("transport: encoding certificate: %w", err)
	}
	if err := pem.Encode(&buf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}); err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("transport: encoding private key: %w", err)
	}

	cert := tls.Certificate{
		Certificate: [][]byte{derBytes},
		PrivateKey:  priv,
	}
	return cert, buf.Bytes(), nil
}
