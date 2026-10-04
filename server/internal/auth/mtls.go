// Package auth implements mTLS peer identification for the device API and
// cookie sessions for the admin API.
package auth

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
)

type CertKind int

const (
	KindUnknown       CertKind = iota
	KindDevice                 // leaf issued by the online device CA
	KindManufacturing          // leaf issued by the manufacturing CA
)

// PeerInfo identifies a device-API client from its verified TLS leaf cert.
type PeerInfo struct {
	Fingerprint string // sha256 of leaf DER, lowercase hex
	Kind        CertKind
	Leaf        *x509.Certificate
}

// ClassifyPeer inspects the (already TLS-verified) client leaf certificate
// and determines which of our two client CAs issued it. Signature checks are
// performed explicitly so a cert that merely copies an issuer DN is not
// misclassified.
func ClassifyPeer(cs *tls.ConnectionState, deviceCA, mfgCA *x509.Certificate) (PeerInfo, error) {
	if cs == nil || len(cs.PeerCertificates) == 0 {
		return PeerInfo{}, fmt.Errorf("no client certificate")
	}
	leaf := cs.PeerCertificates[0]
	info := PeerInfo{Fingerprint: CertFingerprint(leaf), Kind: KindUnknown, Leaf: leaf}
	if issuedBy(leaf, deviceCA) {
		info.Kind = KindDevice
	} else if issuedBy(leaf, mfgCA) {
		info.Kind = KindManufacturing
	}
	return info, nil
}

func issuedBy(leaf, ca *x509.Certificate) bool {
	if ca == nil || !bytesEqual(leaf.RawIssuer, ca.RawSubject) {
		return false
	}
	return leaf.CheckSignatureFrom(ca) == nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// CertFingerprint is the canonical device identity: SHA-256 over the leaf
// certificate DER, lowercase hex, no separators.
func CertFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// PubkeyFingerprint is stable across certificate reissue: SHA-256 over the
// PKIX-encoded public key.
func PubkeyFingerprint(pub any) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("marshal public key: %w", err)
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:]), nil
}

// LoadCertificate reads a single PEM certificate from disk.
func LoadCertificate(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%s: no PEM certificate found", path)
	}
	return x509.ParseCertificate(block.Bytes)
}
