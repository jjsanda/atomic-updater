package api

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"atomic-updater/server/internal/auth"
	"atomic-updater/server/internal/model"
)

// Golden pair from rollout/assign_test.go: bucket(includedDev, rolloutID)=31.
const (
	rolloutID   = "9e8d7c6b-5a49-4838-a7b6-c5d4e3f2a1b0"
	bundleID    = "0d2f4a9e-1111-4222-8333-444455556666"
	includedDev = "ffffffff-ffff-ffff-ffff-ffffffffffff" // bucket 31
	excludedDev = "123e4567-e89b-12d3-a456-426614174000" // bucket 81
)

func seedDevice(env *testEnv, id string) *model.Device {
	dev := &model.Device{
		ID: id, CertFingerprint: "fp-" + id, PubkeyFingerprint: "pub-" + id,
		HardwareType: "rpi-cm4", Channel: "dev",
	}
	_ = env.store.CreateDevice(context.Background(), dev)
	d := env.store.devices[id]
	d.CurrentVersion = "1.0.0"
	return d
}

func seedActiveRollout(env *testEnv, pct int) {
	env.store.bundles[bundleID] = &model.Bundle{
		ID: bundleID, Version: "1.1.0", HardwareType: "rpi-cm4",
		ObjectKey: "signed/test-1.1.0.raucb",
		SHA256:    "d0b425e00e15a0d36ffc2b0c1a1f3b3c4d5e6f708192a3b4c5d6e7f8091a2b3c",
		SizeBytes: 42, Status: "active",
	}
	now := time.Now()
	env.store.rollouts[rolloutID] = &model.Rollout{
		ID: rolloutID, BundleID: bundleID, Channel: "dev", HardwareType: "rpi-cm4",
		BundleVersion: "1.1.0", Percentage: pct, Status: "active", ActivatedAt: &now,
	}
}

func deviceJSONRequest(t *testing.T, dev *model.Device, target string, body any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(raw))
	ctx := ContextWithPeer(r.Context(), auth.PeerInfo{
		Fingerprint: dev.CertFingerprint, Kind: auth.KindDevice,
	})
	return r.WithContext(ContextWithDevice(ctx, dev))
}

func checkinBody(version string) CheckinRequest {
	return CheckinRequest{
		Slot: "A", Version: version, BootCount: 1, UptimeS: 10, Healthy: true,
		LastUpdate: LastUpdate{Outcome: "none"},
	}
}

func TestCheckinNoRolloutReturnsNull(t *testing.T) {
	env := newTestEnv(t)
	dev := seedDevice(env, includedDev)
	w := httptest.NewRecorder()
	env.srv.handleCheckin(w, deviceJSONRequest(t, dev, "/api/v1/checkin", checkinBody("1.0.0")))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var resp CheckinResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Update != nil {
		t.Fatalf("expected no update, got %+v", resp.Update)
	}
}

func TestCheckinOfferForIncludedDevice(t *testing.T) {
	env := newTestEnv(t)
	dev := seedDevice(env, includedDev)
	seedActiveRollout(env, 50)
	w := httptest.NewRecorder()
	env.srv.handleCheckin(w, deviceJSONRequest(t, dev, "/api/v1/checkin", checkinBody("1.0.0")))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var resp CheckinResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Update == nil {
		t.Fatal("expected an update offer")
	}
	if resp.Update.Version != "1.1.0" || resp.Update.RolloutID != rolloutID {
		t.Errorf("unexpected offer %+v", resp.Update)
	}
	if resp.Update.URL == "" || resp.Update.SHA256 == "" {
		t.Error("offer must carry url and sha256")
	}
	if got := env.store.eventsOfType(dev.ID, "offer"); len(got) != 1 {
		t.Errorf("expected 1 offer event, got %d", len(got))
	}
}

func TestCheckinExcludedDeviceGetsNothing(t *testing.T) {
	env := newTestEnv(t)
	dev := seedDevice(env, excludedDev)
	seedActiveRollout(env, 50)
	w := httptest.NewRecorder()
	env.srv.handleCheckin(w, deviceJSONRequest(t, dev, "/api/v1/checkin", checkinBody("1.0.0")))
	var resp CheckinResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Update != nil {
		t.Fatalf("excluded device must not get an offer (bucket 81 >= 50)")
	}
}

func TestCheckinValidation(t *testing.T) {
	env := newTestEnv(t)
	dev := seedDevice(env, includedDev)
	cases := []struct {
		name string
		mut  func(*CheckinRequest)
	}{
		{"bad slot", func(c *CheckinRequest) { c.Slot = "C" }},
		{"bad version", func(c *CheckinRequest) { c.Version = "not-semver" }},
		{"bad outcome", func(c *CheckinRequest) { c.LastUpdate.Outcome = "exploded" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := checkinBody("1.0.0")
			tc.mut(&body)
			w := httptest.NewRecorder()
			env.srv.handleCheckin(w, deviceJSONRequest(t, dev, "/api/v1/checkin", body))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body)
			}
		})
	}
}

func TestEventsIngestOutcomeAndAutoPause(t *testing.T) {
	env := newTestEnv(t)
	dev := seedDevice(env, includedDev)
	seedActiveRollout(env, 100)
	env.store.autoPauseResult = true

	payload, _ := json.Marshal(map[string]string{"rollout_id": rolloutID, "bundle_id": bundleID})
	body := EventsRequest{Events: []DeviceEvent{{EventType: "install-fail", Payload: payload}}}
	w := httptest.NewRecorder()
	env.srv.handleEvents(w, deviceJSONRequest(t, dev, "/api/v1/events", body))
	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if env.store.devices[dev.ID].LastUpdateOutcome != "fail" {
		t.Error("install-fail must set device outcome to fail")
	}
	if len(env.store.autoPauseCalls) != 1 || env.store.autoPauseCalls[0] != rolloutID {
		t.Errorf("auto-pause not evaluated: %v", env.store.autoPauseCalls)
	}
	if env.store.rollouts[rolloutID].Status != "paused" {
		t.Error("rollout should be paused")
	}
}

func TestEventsValidation(t *testing.T) {
	env := newTestEnv(t)
	dev := seedDevice(env, includedDev)

	w := httptest.NewRecorder()
	env.srv.handleEvents(w, deviceJSONRequest(t, dev, "/api/v1/events",
		EventsRequest{Events: []DeviceEvent{{EventType: "self-destruct"}}}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown event type: expected 400, got %d", w.Code)
	}

	tooMany := EventsRequest{}
	for i := 0; i < 65; i++ {
		tooMany.Events = append(tooMany.Events, DeviceEvent{EventType: "download"})
	}
	w = httptest.NewRecorder()
	env.srv.handleEvents(w, deviceJSONRequest(t, dev, "/api/v1/events", tooMany))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversize batch: expected 400, got %d", w.Code)
	}
}

// --- enrollment ---

func enroll(t *testing.T, env *testEnv, csrPEM []byte) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(EnrollRequest{
		CSRPEM: string(csrPEM), HardwareType: "rpi-cm4", Serial: "SN-1",
	})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/enroll", bytes.NewReader(body))
	r = r.WithContext(ContextWithPeer(r.Context(), auth.PeerInfo{
		Fingerprint: "mfg-fp", Kind: auth.KindManufacturing,
	}))
	w := httptest.NewRecorder()
	env.srv.handleEnroll(w, r)
	return w
}

func TestEnrollIssuesDeviceCert(t *testing.T) {
	env := newTestEnv(t)
	csrPEM, _ := makeTestCSR(t)
	w := enroll(t, env, csrPEM)
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var resp EnrollResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(resp.CertificatePEM))
	if block == nil {
		t.Fatal("no certificate in response")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := cert.CheckSignatureFrom(env.deviceCA); err != nil {
		t.Errorf("issued cert not signed by device CA: %v", err)
	}
	if cert.Subject.CommonName != resp.DeviceID {
		t.Errorf("CN %q != device id %q", cert.Subject.CommonName, resp.DeviceID)
	}
	if env.store.devices[resp.DeviceID] == nil {
		t.Error("device row not created")
	}
}

func TestEnrollIdempotentForSameKey(t *testing.T) {
	env := newTestEnv(t)
	csrPEM, _ := makeTestCSR(t)
	first := enroll(t, env, csrPEM)
	if first.Code != http.StatusCreated {
		t.Fatalf("first enroll: %d", first.Code)
	}
	var r1 EnrollResponse
	_ = json.Unmarshal(first.Body.Bytes(), &r1)

	second := enroll(t, env, csrPEM)
	if second.Code != http.StatusOK {
		t.Fatalf("re-enroll should be 200, got %d: %s", second.Code, second.Body)
	}
	var r2 EnrollResponse
	_ = json.Unmarshal(second.Body.Bytes(), &r2)
	if r1.DeviceID != r2.DeviceID {
		t.Errorf("re-enroll created a new device: %s vs %s", r1.DeviceID, r2.DeviceID)
	}
	if len(env.store.devices) != 1 {
		t.Errorf("expected 1 device, got %d", len(env.store.devices))
	}
}

func TestEnrollRevokedKeyRejected(t *testing.T) {
	env := newTestEnv(t)
	csrPEM, _ := makeTestCSR(t)
	first := enroll(t, env, csrPEM)
	var r1 EnrollResponse
	_ = json.Unmarshal(first.Body.Bytes(), &r1)
	_ = env.store.RevokeCert(context.Background(), env.store.devices[r1.DeviceID].CertFingerprint, "", "lost")

	second := enroll(t, env, csrPEM)
	if second.Code != http.StatusForbidden {
		t.Fatalf("revoked key re-enroll should be 403, got %d", second.Code)
	}
}

// --- middleware behavior via DeviceHandler ---

func throughDeviceHandler(env *testEnv, peer auth.PeerInfo, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/checkin", bytes.NewReader(raw))
	r = r.WithContext(ContextWithPeer(r.Context(), peer))
	w := httptest.NewRecorder()
	env.srv.DeviceHandler().ServeHTTP(w, r)
	return w
}

func TestMiddlewareRejectsRevokedCert(t *testing.T) {
	env := newTestEnv(t)
	dev := seedDevice(env, includedDev)
	env.store.revoked[dev.CertFingerprint] = true
	w := throughDeviceHandler(env,
		auth.PeerInfo{Fingerprint: dev.CertFingerprint, Kind: auth.KindDevice},
		checkinBody("1.0.0"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestMiddlewareRejectsRetiredDevice(t *testing.T) {
	env := newTestEnv(t)
	dev := seedDevice(env, includedDev)
	env.store.devices[dev.ID].Status = "retired"
	w := throughDeviceHandler(env,
		auth.PeerInfo{Fingerprint: dev.CertFingerprint, Kind: auth.KindDevice},
		checkinBody("1.0.0"))
	if w.Code != http.StatusGone {
		t.Fatalf("expected 410, got %d", w.Code)
	}
}

func TestMiddlewareRejectsUnknownDevice(t *testing.T) {
	env := newTestEnv(t)
	w := throughDeviceHandler(env,
		auth.PeerInfo{Fingerprint: "never-enrolled", Kind: auth.KindDevice},
		checkinBody("1.0.0"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestMiddlewareRejectsWrongCertKind(t *testing.T) {
	env := newTestEnv(t)
	w := throughDeviceHandler(env,
		auth.PeerInfo{Fingerprint: "mfg-fp", Kind: auth.KindManufacturing},
		checkinBody("1.0.0"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("manufacturing cert on checkin: expected 403, got %d", w.Code)
	}
}

// --- admin API ---

type adminClient struct {
	t       *testing.T
	handler http.Handler
	cookie  *http.Cookie
}

func newAdminClient(t *testing.T, env *testEnv) *adminClient {
	return &adminClient{t: t, handler: env.srv.AdminHandler(nil)}
}

func (c *adminClient) do(method, target string, body any) *httptest.ResponseRecorder {
	c.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, target, reader)
	if c.cookie != nil {
		r.AddCookie(c.cookie)
	}
	w := httptest.NewRecorder()
	c.handler.ServeHTTP(w, r)
	return w
}

func (c *adminClient) login(password string) *httptest.ResponseRecorder {
	w := c.do(http.MethodPost, "/admin/v1/login", LoginRequest{Username: "admin", Password: password})
	for _, ck := range w.Result().Cookies() {
		if ck.Name == sessionCookie {
			c.cookie = ck
		}
	}
	return w
}

func TestAdminAuthFlow(t *testing.T) {
	env := newTestEnv(t)
	c := newAdminClient(t, env)

	if w := c.do(http.MethodGet, "/admin/v1/summary", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated summary: expected 401, got %d", w.Code)
	}
	if w := c.login("wrong-password"); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad login: expected 401, got %d", w.Code)
	}
	if w := c.login(testAdminPassword); w.Code != http.StatusNoContent {
		t.Fatalf("login: expected 204, got %d: %s", w.Code, w.Body)
	}
	if c.cookie == nil {
		t.Fatal("no session cookie set")
	}
	if w := c.do(http.MethodGet, "/admin/v1/summary", nil); w.Code != http.StatusOK {
		t.Fatalf("summary with session: expected 200, got %d", w.Code)
	}
	if w := c.do(http.MethodPost, "/admin/v1/logout", nil); w.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", w.Code)
	}
	if w := c.do(http.MethodGet, "/admin/v1/summary", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("after logout: expected 401, got %d", w.Code)
	}
}

func TestAdminDeviceManagement(t *testing.T) {
	env := newTestEnv(t)
	dev := seedDevice(env, includedDev)
	c := newAdminClient(t, env)
	c.login(testAdminPassword)

	if w := c.do(http.MethodGet, "/admin/v1/devices", nil); w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	beta := "beta"
	if w := c.do(http.MethodPatch, "/admin/v1/devices/"+dev.ID,
		PatchDeviceRequest{Channel: &beta}); w.Code != http.StatusOK {
		t.Fatalf("patch channel: %d: %s", w.Code, w.Body)
	}
	if env.store.devices[dev.ID].Channel != "beta" {
		t.Error("channel not updated")
	}
	junk := "junk-channel"
	if w := c.do(http.MethodPatch, "/admin/v1/devices/"+dev.ID,
		PatchDeviceRequest{Channel: &junk}); w.Code != http.StatusBadRequest {
		t.Fatalf("bad channel: expected 400, got %d", w.Code)
	}
	if w := c.do(http.MethodPost, "/admin/v1/devices/"+dev.ID+"/revoke",
		RevokeRequest{Reason: "stolen"}); w.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d", w.Code)
	}
	if !env.store.revoked[dev.CertFingerprint] {
		t.Error("fingerprint not revoked")
	}
	if w := c.do(http.MethodGet, "/admin/v1/devices/00000000-0000-0000-0000-00000000dead", nil); w.Code != http.StatusNotFound {
		t.Fatalf("missing device: expected 404, got %d", w.Code)
	}
}

func TestAdminBundleRegistration(t *testing.T) {
	env := newTestEnv(t)
	c := newAdminClient(t, env)
	c.login(testAdminPassword)

	payload := []byte("raucb-bytes-here")
	meta := fmt.Sprintf(`{"version":"1.2.0","hardware_type":"rpi-cm4",
		"sha256":"d0b425e00e15a0d36ffc2b0c1a1f3b3c4d5e6f708192a3b4c5d6e7f8091a2b3c",
		"size_bytes":%d,"signed_by":"cafe"}`, len(payload))
	env.objects.objects["signed/x-1.2.0.raucb"] = payload
	env.objects.objects["signed/x-1.2.0.raucb.meta.json"] = []byte(meta)

	if w := c.do(http.MethodPost, "/admin/v1/bundles",
		RegisterBundleRequest{ObjectKey: "signed/x-1.2.0.raucb"}); w.Code != http.StatusCreated {
		t.Fatalf("register: %d: %s", w.Code, w.Body)
	}
	if w := c.do(http.MethodPost, "/admin/v1/bundles",
		RegisterBundleRequest{ObjectKey: "signed/x-1.2.0.raucb"}); w.Code != http.StatusConflict {
		t.Fatalf("duplicate register: expected 409, got %d", w.Code)
	}
	if w := c.do(http.MethodPost, "/admin/v1/bundles",
		RegisterBundleRequest{ObjectKey: "signed/missing.raucb"}); w.Code != http.StatusBadRequest {
		t.Fatalf("missing object: expected 400, got %d", w.Code)
	}
}

func TestAdminRolloutLifecycle(t *testing.T) {
	env := newTestEnv(t)
	env.store.bundles[bundleID] = &model.Bundle{
		ID: bundleID, Version: "1.1.0", HardwareType: "rpi-cm4",
		ObjectKey: "signed/x.raucb", SHA256: "aa", SizeBytes: 1, Status: "active",
	}
	c := newAdminClient(t, env)
	c.login(testAdminPassword)

	w := c.do(http.MethodPost, "/admin/v1/rollouts", CreateRolloutRequest{
		BundleID: bundleID, Channel: "dev", Percentage: 5,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", w.Code, w.Body)
	}
	var ro RolloutJSON
	_ = json.Unmarshal(w.Body.Bytes(), &ro)
	if ro.Status != "draft" {
		t.Fatalf("new rollout must be draft, got %s", ro.Status)
	}

	patch := func(body PatchRolloutRequest) *httptest.ResponseRecorder {
		return c.do(http.MethodPatch, "/admin/v1/rollouts/"+ro.ID, body)
	}
	statusOf := func(s string) *string { return &s }

	if w := patch(PatchRolloutRequest{Status: statusOf("active")}); w.Code != http.StatusOK {
		t.Fatalf("activate: %d: %s", w.Code, w.Body)
	}
	pct := 50
	if w := patch(PatchRolloutRequest{Percentage: &pct}); w.Code != http.StatusOK {
		t.Fatalf("ramp: %d", w.Code)
	}
	if w := patch(PatchRolloutRequest{Status: statusOf("paused")}); w.Code != http.StatusOK {
		t.Fatalf("pause: %d", w.Code)
	}
	if w := patch(PatchRolloutRequest{Status: statusOf("active")}); w.Code != http.StatusOK {
		t.Fatalf("resume: %d", w.Code)
	}
	if w := patch(PatchRolloutRequest{Status: statusOf("completed")}); w.Code != http.StatusOK {
		t.Fatalf("complete: %d", w.Code)
	}
	if w := patch(PatchRolloutRequest{Status: statusOf("active")}); w.Code != http.StatusConflict {
		t.Fatalf("completed→active must 409, got %d", w.Code)
	}
	if w := patch(PatchRolloutRequest{Percentage: &pct}); w.Code != http.StatusConflict {
		t.Fatalf("percentage change after completion must 409, got %d", w.Code)
	}
}
