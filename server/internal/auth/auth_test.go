package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// --- ClassifyPeer ---

func makeCA(t *testing.T, cn string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
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
	cert, _ := x509.ParseCertificate(der)
	return cert, key
}

func makeLeaf(t *testing.T, cn string, ca *x509.Certificate, caKey *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return cert
}

func TestClassifyPeer(t *testing.T) {
	deviceCA, deviceKey := makeCA(t, "Device CA")
	mfgCA, mfgKey := makeCA(t, "Mfg CA")

	devLeaf := makeLeaf(t, "some-device", deviceCA, deviceKey)
	mfgLeaf := makeLeaf(t, "mfg-fleet", mfgCA, mfgKey)

	cs := &tls.ConnectionState{PeerCertificates: []*x509.Certificate{devLeaf}}
	info, err := ClassifyPeer(cs, deviceCA, mfgCA)
	if err != nil || info.Kind != KindDevice {
		t.Fatalf("device leaf: kind=%v err=%v", info.Kind, err)
	}
	if info.Fingerprint != CertFingerprint(devLeaf) {
		t.Error("fingerprint mismatch")
	}

	cs = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{mfgLeaf}}
	info, _ = ClassifyPeer(cs, deviceCA, mfgCA)
	if info.Kind != KindManufacturing {
		t.Fatalf("mfg leaf: kind=%v", info.Kind)
	}

	if _, err := ClassifyPeer(&tls.ConnectionState{}, deviceCA, mfgCA); err == nil {
		t.Error("no peer cert must error")
	}
}

// A forged certificate that copies the device CA's subject as its issuer but
// is NOT signed by it must classify as unknown.
func TestClassifyPeerRejectsForgedIssuer(t *testing.T) {
	deviceCA, _ := makeCA(t, "Device CA")
	mfgCA, _ := makeCA(t, "Mfg CA")

	rogueKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rogueCATmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               deviceCA.Subject, // same DN as the real device CA
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	rogueDER, _ := x509.CreateCertificate(rand.Reader, rogueCATmpl, rogueCATmpl, &rogueKey.PublicKey, rogueKey)
	rogueCA, _ := x509.ParseCertificate(rogueDER)
	forgedLeaf := makeLeaf(t, "forged-device", rogueCA, rogueKey)

	cs := &tls.ConnectionState{PeerCertificates: []*x509.Certificate{forgedLeaf}}
	info, _ := ClassifyPeer(cs, deviceCA, mfgCA)
	if info.Kind != KindUnknown {
		t.Fatalf("forged issuer DN must classify unknown, got %v", info.Kind)
	}
}

// --- sessions ---

type memSessions struct {
	rows map[string]struct {
		user string
		exp  time.Time
	}
}

func (m *memSessions) CreateSession(ctx context.Context, hash, user string, exp time.Time) error {
	m.rows[hash] = struct {
		user string
		exp  time.Time
	}{user, exp}
	return nil
}

func (m *memSessions) GetSession(ctx context.Context, hash string) (string, time.Time, error) {
	r, ok := m.rows[hash]
	if !ok {
		return "", time.Time{}, ErrNoSession
	}
	return r.user, r.exp, nil
}

func (m *memSessions) DeleteSession(ctx context.Context, hash string) error {
	delete(m.rows, hash)
	return nil
}

func newSessions(t *testing.T, ttl time.Duration) (*Sessions, *memSessions) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-horse"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	store := &memSessions{rows: map[string]struct {
		user string
		exp  time.Time
	}{}}
	return &Sessions{Store: store, User: "admin", PasswordHash: hash, TTL: ttl}, store
}

func TestSessionLifecycle(t *testing.T) {
	s, _ := newSessions(t, time.Hour)
	ctx := context.Background()

	if _, err := s.Login(ctx, "admin", "wrong"); err != ErrBadCredentials {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := s.Login(ctx, "eve", "correct-horse"); err != ErrBadCredentials {
		t.Fatalf("wrong user: %v", err)
	}

	token, err := s.Login(ctx, "admin", "correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Validate(ctx, token); !ok {
		t.Fatal("fresh session must validate")
	}
	if ok, _ := s.Validate(ctx, "forged-token"); ok {
		t.Fatal("unknown token must not validate")
	}
	if err := s.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Validate(ctx, token); ok {
		t.Fatal("logged-out session must not validate")
	}
}

func TestSessionExpiry(t *testing.T) {
	s, store := newSessions(t, -time.Minute) // already expired on creation
	ctx := context.Background()
	token, err := s.Login(ctx, "admin", "correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Validate(ctx, token); ok {
		t.Fatal("expired session must not validate")
	}
	if len(store.rows) != 0 {
		t.Error("expired session should be deleted on validation")
	}
}
