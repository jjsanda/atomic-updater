package api

// In-memory fake store + PKI helpers shared by the handler and mTLS tests.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"atomic-updater/server/internal/auth"
	"atomic-updater/server/internal/ca"
	"atomic-updater/server/internal/model"
	"atomic-updater/server/internal/rollout"
	"atomic-updater/server/internal/storage"
)

// --- fake store ---

type storedEvent struct {
	id        int64
	deviceID  string
	eventType string
	payload   json.RawMessage
	createdAt time.Time
}

type fakeStore struct {
	mu       sync.Mutex
	devices  map[string]*model.Device
	bundles  map[string]*model.Bundle
	rollouts map[string]*model.Rollout
	events   []storedEvent
	revoked  map[string]bool
	sessions map[string]struct {
		user    string
		expires time.Time
	}
	nextEventID     int64
	autoPauseCalls  []string
	autoPauseResult bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		devices:  map[string]*model.Device{},
		bundles:  map[string]*model.Bundle{},
		rollouts: map[string]*model.Rollout{},
		revoked:  map[string]bool{},
		sessions: map[string]struct {
			user    string
			expires time.Time
		}{},
	}
}

// rollout.Store

func (f *fakeStore) NewestActiveRollout(ctx context.Context, ch, hw string) (*model.Rollout, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var newest *model.Rollout
	for _, r := range f.rollouts {
		if r.Status != "active" || r.Channel != ch || r.HardwareType != hw {
			continue
		}
		if newest == nil || (r.ActivatedAt != nil && newest.ActivatedAt != nil &&
			r.ActivatedAt.After(*newest.ActivatedAt)) {
			newest = r
		}
	}
	return newest, nil
}

func (f *fakeStore) GetBundle(ctx context.Context, id string) (*model.Bundle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bundles[id], nil
}

func (f *fakeStore) LastFailedBundleID(ctx context.Context, deviceID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.events) - 1; i >= 0; i-- {
		e := f.events[i]
		if e.deviceID == deviceID && (e.eventType == "install-fail" || e.eventType == "rollback") {
			var p struct {
				BundleID string `json:"bundle_id"`
			}
			_ = json.Unmarshal(e.payload, &p)
			return p.BundleID, nil
		}
	}
	return "", nil
}

func (f *fakeStore) OfferLoggedWithin(ctx context.Context, deviceID, rolloutID string, w time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.events {
		if e.deviceID == deviceID && e.eventType == "offer" &&
			strings.Contains(string(e.payload), rolloutID) &&
			time.Since(e.createdAt) < w {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) LogOffer(ctx context.Context, deviceID, rolloutID, bundleID string) error {
	payload, _ := json.Marshal(map[string]string{"rollout_id": rolloutID, "bundle_id": bundleID})
	return f.InsertEvent(ctx, deviceID, "offer", payload)
}

// auth.SessionStore

func (f *fakeStore) CreateSession(ctx context.Context, hash, user string, exp time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[hash] = struct {
		user    string
		expires time.Time
	}{user, exp}
	return nil
}

func (f *fakeStore) GetSession(ctx context.Context, hash string) (string, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[hash]
	if !ok {
		return "", time.Time{}, auth.ErrNoSession
	}
	return s.user, s.expires, nil
}

func (f *fakeStore) DeleteSession(ctx context.Context, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, hash)
	return nil
}

// devices

func (f *fakeStore) GetDevice(ctx context.Context, id string) (*model.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.devices[id], nil
}

func (f *fakeStore) GetDeviceByFingerprint(ctx context.Context, fp string) (*model.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.devices {
		if d.CertFingerprint == fp {
			return d, nil
		}
	}
	return nil, nil
}

func (f *fakeStore) GetDeviceByPubkey(ctx context.Context, fp string) (*model.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.devices {
		if d.PubkeyFingerprint == fp {
			return d, nil
		}
	}
	return nil, nil
}

func (f *fakeStore) CreateDevice(ctx context.Context, d *model.Device) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *d
	cp.Status = "active"
	cp.LastUpdateOutcome = "none"
	cp.Healthy = true
	cp.CreatedAt = time.Now()
	f.devices[d.ID] = &cp
	return nil
}

func (f *fakeStore) UpdateDeviceCert(ctx context.Context, id, fp string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.devices[id]; ok {
		d.CertFingerprint = fp
	}
	return nil
}

func (f *fakeStore) UpdateDeviceCheckin(ctx context.Context, id, slot, version string,
	bundleID *string, bootCount int64, healthy bool, outcome string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.devices[id]
	if !ok {
		return nil
	}
	d.CurrentSlot = slot
	d.CurrentVersion = version
	d.CurrentBundleID = bundleID
	d.BootCount = bootCount
	d.Healthy = healthy
	d.LastUpdateOutcome = outcome
	now := time.Now()
	d.LastSeenAt = &now
	return nil
}

func (f *fakeStore) SetDeviceOutcome(ctx context.Context, id, outcome string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.devices[id]; ok {
		d.LastUpdateOutcome = outcome
	}
	return nil
}

func (f *fakeStore) ListDevices(ctx context.Context, fl storage.DeviceFilter) ([]model.Device, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Device
	for _, d := range f.devices {
		if fl.Channel != "" && d.Channel != fl.Channel {
			continue
		}
		if fl.HardwareType != "" && d.HardwareType != fl.HardwareType {
			continue
		}
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, int64(len(out)), nil
}

func (f *fakeStore) UpdateDeviceAdmin(ctx context.Context, id string, channel, status *string) (*model.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.devices[id]
	if !ok {
		return nil, nil
	}
	if channel != nil {
		d.Channel = *channel
	}
	if status != nil {
		d.Status = *status
	}
	cp := *d
	return &cp, nil
}

func (f *fakeStore) Summary(ctx context.Context) (*model.Summary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &model.Summary{ByChannel: map[string]int64{}, ByVersion: map[string]int64{}}
	for _, d := range f.devices {
		s.DevicesTotal++
		s.ByChannel[d.Channel]++
		if d.CurrentVersion != "" {
			s.ByVersion[d.CurrentVersion]++
		}
	}
	return s, nil
}

// bundles

func (f *fakeStore) ListBundles(ctx context.Context) ([]model.Bundle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Bundle
	for _, b := range f.bundles {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeStore) GetBundleByKey(ctx context.Context, key string) (*model.Bundle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.bundles {
		if b.ObjectKey == key {
			return b, nil
		}
	}
	return nil, nil
}

func (f *fakeStore) InsertBundle(ctx context.Context, b *model.Bundle) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ex := range f.bundles {
		if ex.ObjectKey == b.ObjectKey ||
			(ex.Version == b.Version && ex.HardwareType == b.HardwareType) {
			return storage.ErrDuplicateBundle
		}
	}
	cp := *b
	cp.Status = "active"
	cp.UploadedAt = time.Now()
	f.bundles[b.ID] = &cp
	return nil
}

func (f *fakeStore) UpdateBundleStatus(ctx context.Context, id, status string) (*model.Bundle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.bundles[id]
	if !ok {
		return nil, nil
	}
	b.Status = status
	cp := *b
	return &cp, nil
}

func (f *fakeStore) BundleExistsByKey(ctx context.Context, key string) (bool, error) {
	b, _ := f.GetBundleByKey(ctx, key)
	return b != nil, nil
}

// rollouts

func (f *fakeStore) ListRollouts(ctx context.Context) ([]model.Rollout, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Rollout
	for _, r := range f.rollouts {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeStore) GetRollout(ctx context.Context, id string) (*model.Rollout, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.rollouts[id]; ok {
		cp := *r
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeStore) CreateRollout(ctx context.Context, r *model.Rollout) (*model.Rollout, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *r
	cp.Status = "draft"
	cp.CreatedAt = time.Now()
	if b, ok := f.bundles[r.BundleID]; ok {
		cp.BundleVersion = b.Version
		cp.HardwareType = b.HardwareType
	}
	f.rollouts[r.ID] = &cp
	out := cp
	return &out, nil
}

func (f *fakeStore) UpdateRolloutPercentage(ctx context.Context, id string, pct int) (*model.Rollout, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rollouts[id]
	if !ok || (r.Status != "draft" && r.Status != "active" && r.Status != "paused") {
		return nil, nil
	}
	r.Percentage = pct
	cp := *r
	return &cp, nil
}

func (f *fakeStore) TransitionRollout(ctx context.Context, id, to string, allowedFrom []string) (*model.Rollout, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rollouts[id]
	if !ok {
		return nil, nil
	}
	allowed := false
	for _, from := range allowedFrom {
		if r.Status == from {
			allowed = true
		}
	}
	if !allowed {
		return nil, nil
	}
	r.Status = to
	if to == "active" {
		if r.ActivatedAt == nil {
			now := time.Now()
			r.ActivatedAt = &now
		}
		r.PausedReason = nil
	}
	cp := *r
	return &cp, nil
}

func (f *fakeStore) EvaluateAutoPause(ctx context.Context, rolloutID string, minCohort, failPct int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.autoPauseCalls = append(f.autoPauseCalls, rolloutID)
	if f.autoPauseResult {
		if r, ok := f.rollouts[rolloutID]; ok && r.Status == "active" {
			r.Status = "paused"
			reason := "auto: test"
			r.PausedReason = &reason
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) RolloutStats(ctx context.Context, id string) (*model.RolloutStats, error) {
	return &model.RolloutStats{}, nil
}

// events

func (f *fakeStore) InsertEvent(ctx context.Context, deviceID, eventType string, payload json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextEventID++
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	f.events = append(f.events, storedEvent{
		id: f.nextEventID, deviceID: deviceID, eventType: eventType,
		payload: payload, createdAt: time.Now(),
	})
	return nil
}

func (f *fakeStore) ListDeviceEvents(ctx context.Context, deviceID string, limit int) ([]model.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Event
	for i := len(f.events) - 1; i >= 0 && len(out) < max(limit, 100); i-- {
		e := f.events[i]
		if e.deviceID == deviceID {
			out = append(out, model.Event{
				ID: e.id, DeviceID: e.deviceID, EventType: e.eventType,
				Payload: e.payload, CreatedAt: e.createdAt,
			})
		}
	}
	return out, nil
}

func (f *fakeStore) eventsOfType(deviceID, eventType string) []storedEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []storedEvent
	for _, e := range f.events {
		if e.deviceID == deviceID && e.eventType == eventType {
			out = append(out, e)
		}
	}
	return out
}

// revocation

func (f *fakeStore) IsRevoked(ctx context.Context, fp string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.revoked[fp], nil
}

func (f *fakeStore) RevokeCert(ctx context.Context, fp, serial, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked[fp] = true
	return nil
}

func (f *fakeStore) Ping(ctx context.Context) error { return nil }

// --- fake object store ---

type fakeObjects struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newFakeObjects() *fakeObjects {
	return &fakeObjects{objects: map[string][]byte{}}
}

func (f *fakeObjects) StatObject(ctx context.Context, key string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.objects[key]
	if !ok {
		return 0, storage.ErrNoObject
	}
	return int64(len(data)), nil
}

func (f *fakeObjects) GetObjectBytes(ctx context.Context, key string, maxSize int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.objects[key]
	if !ok {
		return nil, storage.ErrNoObject
	}
	return data, nil
}

func (f *fakeObjects) Ping(ctx context.Context) error { return nil }

type fakePresigner struct{}

func (fakePresigner) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return "https://store.example/" + key + "?sig=test", nil
}

// --- test PKI helpers ---

func makeTestCA(t *testing.T, cn string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn, Organization: []string{"Test"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func makeTestLeaf(t *testing.T, cn string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn, Organization: []string{"Test"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, parsed
}

func makeTestCSR(t *testing.T) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "enrolling-device"},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), key
}

// writeTestIssuer persists a generated CA to disk and loads it through the
// production LoadIssuer path (also covers SEC1 key parsing).
func writeTestIssuer(t *testing.T, cert *x509.Certificate, key *ecdsa.PrivateKey) *ca.Issuer {
	t.Helper()
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.cert.pem")
	keyPath := filepath.Join(dir, "ca.key.pem")
	if err := os.WriteFile(certPath,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath,
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	issuer, err := ca.LoadIssuer(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	return issuer
}

const testAdminPassword = "hunter2-test-password"

type testEnv struct {
	srv       *Server
	store     *fakeStore
	objects   *fakeObjects
	deviceCA  *x509.Certificate
	deviceKey *ecdsa.PrivateKey
	mfgCA     *x509.Certificate
	mfgKey    *ecdsa.PrivateKey
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	deviceCA, deviceKey := makeTestCA(t, "Test Device CA")
	mfgCA, mfgKey := makeTestCA(t, "Test Manufacturing CA")
	store := newFakeStore()
	objects := newFakeObjects()
	hash, err := bcrypt.GenerateFromPassword([]byte(testAdminPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		Store:   store,
		Objects: objects,
		Controller: &rollout.Controller{
			Store:     store,
			Presigner: fakePresigner{},
			TTL:       15 * time.Minute,
		},
		Issuer: writeTestIssuer(t, deviceCA, deviceKey),
		Sessions: &auth.Sessions{
			Store: store, User: "admin", PasswordHash: hash, TTL: time.Hour,
		},
		DeviceCA:       deviceCA,
		MfgCA:          mfgCA,
		CAChainPEM:     pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: deviceCA.Raw}),
		PauseMinCohort: 25,
		PauseFailPct:   2,
		Log:            slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	return &testEnv{
		srv: srv, store: store, objects: objects,
		deviceCA: deviceCA, deviceKey: deviceKey,
		mfgCA: mfgCA, mfgKey: mfgKey,
	}
}
