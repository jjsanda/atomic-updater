package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeCA(t *testing.T, keyType string) (certPath, keyPath string, caCert *x509.Certificate) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ = x509.ParseCertificate(der)

	dir := t.TempDir()
	certPath = filepath.Join(dir, "ca.cert.pem")
	keyPath = filepath.Join(dir, "ca.key.pem")
	_ = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)

	var keyPEM []byte
	switch keyType {
	case "sec1": // what openssl ecparam emits — used by scripts/pki
		keyDER, _ := x509.MarshalECPrivateKey(key)
		keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	case "pkcs8":
		keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
		keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	}
	_ = os.WriteFile(keyPath, keyPEM, 0o600)
	return certPath, keyPath, caCert
}

func makeCSRPEM(t *testing.T) []byte {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "device"},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

func TestIssueFromBothKeyEncodings(t *testing.T) {
	for _, keyType := range []string{"sec1", "pkcs8"} {
		t.Run(keyType, func(t *testing.T) {
			certPath, keyPath, caCert := writeCA(t, keyType)
			issuer, err := LoadIssuer(certPath, keyPath)
			if err != nil {
				t.Fatal(err)
			}
			csr, err := ParseCSR(makeCSRPEM(t))
			if err != nil {
				t.Fatal(err)
			}
			certPEM, cert, err := issuer.Issue(csr, "11111111-2222-4333-8444-555555555555")
			if err != nil {
				t.Fatal(err)
			}
			if cert.Subject.CommonName != "11111111-2222-4333-8444-555555555555" {
				t.Errorf("CN = %q", cert.Subject.CommonName)
			}
			if err := cert.CheckSignatureFrom(caCert); err != nil {
				t.Errorf("not signed by CA: %v", err)
			}
			if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
				t.Error("device certs must be clientAuth-only")
			}
			block, _ := pem.Decode(certPEM)
			if block == nil || block.Type != "CERTIFICATE" {
				t.Error("Issue must return PEM")
			}
		})
	}
}

func TestParseCSRRejectsGarbage(t *testing.T) {
	if _, err := ParseCSR([]byte("not a csr")); err == nil {
		t.Error("garbage must not parse")
	}
	// A syntactically valid PEM block of the wrong type.
	wrong := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1, 2, 3}})
	if _, err := ParseCSR(wrong); err == nil {
		t.Error("wrong PEM type must not parse")
	}
}
