// Package enroll implements first-boot enrollment (design doc §11.3): a
// fresh device keypair, a CSR authenticated by the manufacturing cert, and
// persistence of the issued device certificate on the data partition.
package enroll

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"atomic-updater/agent/internal/client"
)

type Params struct {
	ServerURL    string
	Keyring      string
	MfgCertFile  string
	MfgKeyFile   string
	CertFile     string // destination for the issued device cert
	KeyFile      string // destination for the generated device key
	HardwareType string
	Serial       string
	HTTPTimeout  time.Duration
}

// Enrolled reports whether a device identity already exists on disk.
func Enrolled(certFile, keyFile string) bool {
	if _, err := os.Stat(certFile); err != nil {
		return false
	}
	if _, err := os.Stat(keyFile); err != nil {
		return false
	}
	return true
}

// Run performs one enrollment attempt and returns the assigned device ID.
// The device key never leaves the device: only a CSR travels.
func Run(p Params) (string, error) {
	// Reuse a previously generated key (e.g. enrollment response was lost);
	// the server treats same-key enrollment as idempotent re-enrollment.
	key, err := loadOrCreateKey(p.KeyFile)
	if err != nil {
		return "", err
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   "enrollment:" + p.Serial,
			Organization: []string{"Acme Devices"},
		},
	}, key)
	if err != nil {
		return "", fmt.Errorf("create CSR: %w", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	mfgClient, err := client.New(p.ServerURL, p.Keyring, p.MfgCertFile, p.MfgKeyFile, p.HTTPTimeout)
	if err != nil {
		return "", fmt.Errorf("manufacturing identity: %w", err)
	}
	resp, err := mfgClient.Enroll(client.EnrollRequest{
		CSRPEM:       string(csrPEM),
		HardwareType: p.HardwareType,
		Serial:       p.Serial,
	})
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(p.CertFile), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(p.CertFile, []byte(resp.CertificatePEM), 0o600); err != nil {
		return "", err
	}
	// The chain is informational (server-side provenance); keep it beside
	// the cert for debugging.
	_ = os.WriteFile(p.CertFile+".chain", []byte(resp.CAChainPEM), 0o600)
	return resp.DeviceID, nil
}

func loadOrCreateKey(keyFile string) (*ecdsa.PrivateKey, error) {
	if data, err := os.ReadFile(keyFile); err == nil {
		block, _ := pem.Decode(data)
		if block != nil && block.Type == "EC PRIVATE KEY" {
			if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
				return key, nil
			}
		}
		// Unparseable key on disk: fall through and regenerate.
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyFile,
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}
