// Package ca issues device client certificates from the online device CA
// during first-boot enrollment (design doc §11.3).
package ca

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"time"
)

const DeviceCertTTL = 2 * 365 * 24 * time.Hour

type Issuer struct {
	Cert *x509.Certificate
	key  crypto.Signer
}

func LoadIssuer(certPath, keyPath string) (*Issuer, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%s: no PEM certificate", certPath)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", certPath, err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	key, err := parsePrivateKey(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", keyPath, err)
	}
	return &Issuer{Cert: cert, key: key}, nil
}

func parsePrivateKey(pemData []byte) (crypto.Signer, error) {
	for {
		var block *pem.Block
		block, pemData = pem.Decode(pemData)
		if block == nil {
			return nil, fmt.Errorf("no private key found in PEM")
		}
		switch block.Type {
		case "PRIVATE KEY":
			k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, err
			}
			switch key := k.(type) {
			case *ecdsa.PrivateKey:
				return key, nil
			case *rsa.PrivateKey:
				return key, nil
			case ed25519.PrivateKey:
				return key, nil
			default:
				return nil, fmt.Errorf("unsupported PKCS8 key type %T", k)
			}
		case "EC PRIVATE KEY":
			return x509.ParseECPrivateKey(block.Bytes)
		case "RSA PRIVATE KEY":
			return x509.ParsePKCS1PrivateKey(block.Bytes)
		}
		// Skip unrelated blocks (e.g. EC PARAMETERS emitted by openssl ecparam).
	}
}

// ParseCSR decodes and signature-checks a PEM certificate request.
func ParseCSR(csrPEM []byte) (*x509.CertificateRequest, error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, fmt.Errorf("no PEM certificate request")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, err
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("CSR signature invalid: %w", err)
	}
	return csr, nil
}

// Issue signs a client certificate for the CSR's public key with CN=cn.
// Returns the PEM plus the parsed certificate (for fingerprinting).
func (i *Issuer) Issue(csr *x509.CertificateRequest, cn string) ([]byte, *x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   cn,
			Organization: []string{"Acme Devices"},
		},
		NotBefore:             time.Now().Add(-5 * time.Minute),
		NotAfter:              time.Now().Add(DeviceCertTTL),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, i.Cert, csr.PublicKey, i.key)
	if err != nil {
		return nil, nil, fmt.Errorf("sign device cert: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), cert, nil
}
