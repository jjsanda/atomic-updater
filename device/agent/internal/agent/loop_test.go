package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"atomic-updater/agent/internal/client"
	"atomic-updater/agent/internal/config"
	"atomic-updater/agent/internal/state"
	"atomic-updater/agent/internal/window"
)

// --- fakes ---

type fakeAPI struct {
	checkins   []client.CheckinRequest
	responses  []*client.CheckinResponse // popped per checkin; empty → update:null
	events     []client.DeviceEvent
	eventsErr  error
	checkinErr error
}

func (f *fakeAPI) Checkin(req client.CheckinRequest) (*client.CheckinResponse, error) {
	if f.checkinErr != nil {
		return nil, f.checkinErr
	}
	f.checkins = append(f.checkins, req)
	if len(f.responses) == 0 {
		return &client.CheckinResponse{}, nil
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

func (f *fakeAPI) Events(events []client.DeviceEvent) error {
	if f.eventsErr != nil {
		return f.eventsErr
	}
	f.events = append(f.events, events...)
	return nil
}

func (f *fakeAPI) eventTypes() []string {
	var out []string
	for _, e := range f.events {
		out = append(out, e.EventType)
	}
	return out
}

type fakeInstaller struct {
	calls     []string
	err       error
	failFirst int // fail this many calls, then succeed (transient failure)
}

func (f *fakeInstaller) Install(ctx context.Context, path string) (string, error) {
	f.calls = append(f.calls, path)
	if f.failFirst > 0 {
		f.failFirst--
		return "simulated rauc output: Device or resource busy", context.DeadlineExceeded
	}
	if f.err != nil {
		return "simulated rauc output: signature rejected", f.err
	}
	return "installed OK", nil
}

type env struct {
	loop      *Loop
	api       *fakeAPI
	installer *fakeInstaller
	bundleSrv *httptest.Server
	payload   []byte
	rebooted  int
	slot      string
	version   string
	bootID    string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{
		api:       &fakeAPI{},
		installer: &fakeInstaller{},
		payload:   []byte("fake-bundle-payload"),
		slot:      "A",
		version:   "1.0.0",
		bootID:    "boot-1",
	}
	e.bundleSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(e.payload)
	}))
	t.Cleanup(e.bundleSrv.Close)

	cfg := config.Defaults()
	cfg.ServerURL = "https://unused"
	cfg.Keyring = "unused"
	cfg.HardwareType = "rpi-cm4"
	cfg.DataDir = t.TempDir()
	cfg.InstallRetryDelay = time.Millisecond

	st, err := state.Load(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	win, _ := window.Parse("")
	e.loop = &Loop{
		Cfg:       cfg,
		API:       e.api,
		Installer: e.installer,
		Download:  e.bundleSrv.Client(),
		Window:    win,
		St:        st,
		Log:       slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		Now:       time.Now,
		BootID:    func() (string, error) { return e.bootID, nil },
		Slot:      func() (string, error) { return e.slot, nil },
		Version:   func() (string, error) { return e.version, nil },
		Uptime:    func() int64 { return 42 },
		Reboot:    func() error { e.rebooted++; return nil },
	}
	return e
}

func (e *env) sha() string {
	sum := sha256.Sum256(e.payload)
	return hex.EncodeToString(sum[:])
}

func (e *env) offer(version string, allowDowngrade bool) *client.CheckinResponse {
	return &client.CheckinResponse{Update: &client.UpdateAssignment{
		RolloutID: "r1", BundleID: "b1", Version: version,
		URL: e.bundleSrv.URL, SHA256: e.sha(),
		SizeBytes: int64(len(e.payload)), AllowDowngrade: allowDowngrade,
	}}
}

// --- tests ---

func TestHappyPathInstallThenConfirm(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	if err := e.loop.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	if e.loop.St.BootCount != 1 {
		t.Errorf("boot count = %d", e.loop.St.BootCount)
	}

	// Tick with an offer: download → install → pending → reboot (window open).
	e.api.responses = []*client.CheckinResponse{e.offer("1.1.0", false)}
	if err := e.loop.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.installer.calls) != 1 {
		t.Fatalf("installer calls: %v", e.installer.calls)
	}
	if e.loop.St.Pending == nil || e.loop.St.Pending.ExpectedSlot != "B" {
		t.Fatalf("pending wrong: %+v", e.loop.St.Pending)
	}
	if e.rebooted != 1 {
		t.Fatalf("expected reboot, got %d", e.rebooted)
	}
	types := e.api.eventTypes()
	if len(types) < 2 || types[0] != "download" || types[1] != "install-started" {
		t.Fatalf("events before reboot: %v", types)
	}

	// "Reboot" into the new slot: new boot id, expected slot + version.
	e.bootID = "boot-2"
	e.slot = "B"
	e.version = "1.1.0"
	if err := e.loop.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	if e.loop.St.Pending != nil {
		t.Error("pending must clear after boot")
	}
	if e.loop.St.LastUpdate.Outcome != "ok" {
		t.Errorf("outcome = %q", e.loop.St.LastUpdate.Outcome)
	}
	// The install-ok event is queued; a tick delivers it and reports state.
	if err := e.loop.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	types = e.api.eventTypes()
	if types[len(types)-1] != "install-ok" {
		t.Fatalf("expected trailing install-ok, got %v", types)
	}
	last := e.api.checkins[len(e.api.checkins)-1]
	if last.Slot != "B" || last.Version != "1.1.0" || last.LastUpdate.Outcome != "ok" {
		t.Errorf("post-boot checkin wrong: %+v", last)
	}
}

func TestRollbackDetectedAfterFailedBoot(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_ = e.loop.Startup(ctx)

	e.api.responses = []*client.CheckinResponse{e.offer("1.1.0", false)}
	if err := e.loop.Tick(ctx); err != nil {
		t.Fatal(err)
	}

	// Bootloader fell back: we boot the OLD slot again with a new boot id.
	e.bootID = "boot-2" // slot stays A, version stays 1.0.0
	if err := e.loop.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	if e.loop.St.LastUpdate.Outcome != "rollback" {
		t.Fatalf("outcome = %q", e.loop.St.LastUpdate.Outcome)
	}
	_ = e.loop.Tick(ctx)
	types := e.api.eventTypes()
	found := false
	for _, ty := range types {
		if ty == "rollback" {
			found = true
		}
	}
	if !found {
		t.Fatalf("rollback event missing: %v", types)
	}
	last := e.api.checkins[len(e.api.checkins)-1]
	if last.LastUpdate.Outcome != "rollback" || last.LastUpdate.Reason == "" {
		t.Errorf("rollback checkin wrong: %+v", last.LastUpdate)
	}
}

func TestInstallFailureReported(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_ = e.loop.Startup(ctx)

	e.installer.err = context.DeadlineExceeded
	e.api.responses = []*client.CheckinResponse{e.offer("1.1.0", false)}
	if err := e.loop.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if e.loop.St.Pending != nil {
		t.Error("failed install must not set pending")
	}
	if e.rebooted != 0 {
		t.Error("failed install must not reboot")
	}
	if e.loop.St.LastUpdate.Outcome != "fail" {
		t.Errorf("outcome = %q", e.loop.St.LastUpdate.Outcome)
	}
	types := e.api.eventTypes()
	if types[len(types)-1] != "install-fail" {
		t.Fatalf("expected install-fail event, got %v", types)
	}
	if _, err := os.Stat(filepath.Join(e.loop.Cfg.DataDir, "pending.raucb")); !os.IsNotExist(err) {
		t.Error("failed bundle should be deleted")
	}
}

// A transient install failure (busy device) must be retried and absorbed —
// no install-fail event, because that permanently blocks the bundle (§10.4).
func TestInstallRetriesTransientFailure(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_ = e.loop.Startup(ctx)

	e.installer.failFirst = 2 // fails twice, third attempt succeeds
	e.api.responses = []*client.CheckinResponse{e.offer("1.1.0", false)}
	if err := e.loop.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.installer.calls) != 3 {
		t.Fatalf("expected 3 install attempts, got %d", len(e.installer.calls))
	}
	if e.loop.St.Pending == nil {
		t.Fatal("install should have eventually succeeded")
	}
	for _, ty := range e.api.eventTypes() {
		if ty == "install-fail" {
			t.Fatal("transient failure must not emit install-fail")
		}
	}
	if e.rebooted != 1 {
		t.Errorf("expected reboot after eventual success, got %d", e.rebooted)
	}
}

// A bundle whose published object never matches its advertised sha256 must
// be reported as install-fail after a few strikes — not re-downloaded
// forever.
func TestCorruptBundleObjectReportedAfterStrikes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_ = e.loop.Startup(ctx)

	bad := e.offer("1.1.0", false)
	bad.Update.SHA256 = strings.Repeat("0", 64) // never matches the payload
	for i := 0; i < 3; i++ {
		e.api.responses = []*client.CheckinResponse{bad}
		err := e.loop.Tick(ctx)
		if i < 2 && err == nil {
			t.Fatalf("strike %d: expected a download error", i+1)
		}
		if i == 2 && err != nil {
			t.Fatalf("strike 3 should resolve to install-fail, got %v", err)
		}
	}
	if len(e.installer.calls) != 0 {
		t.Error("corrupt bundle must never reach the installer")
	}
	if e.loop.St.LastUpdate.Outcome != "fail" {
		t.Errorf("outcome = %q", e.loop.St.LastUpdate.Outcome)
	}
	types := e.api.eventTypes()
	if len(types) == 0 || types[len(types)-1] != "install-fail" {
		t.Fatalf("expected trailing install-fail, got %v", types)
	}
}

func TestAgentRefusesDowngradeOffer(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_ = e.loop.Startup(ctx)

	e.api.responses = []*client.CheckinResponse{e.offer("0.9.0", false)}
	if err := e.loop.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.installer.calls) != 0 {
		t.Error("downgrade must not install")
	}

	// With allow_downgrade the same offer proceeds.
	e.api.responses = []*client.CheckinResponse{e.offer("0.9.0", true)}
	if err := e.loop.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.installer.calls) != 1 {
		t.Error("allow_downgrade offer must install")
	}
}

func TestMaintenanceWindowDefersReboot(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_ = e.loop.Startup(ctx)

	// A window that is never "now": pick 2h from now, 1 minute long.
	future := time.Now().Add(2 * time.Hour)
	spec := future.Format("15:04") + "-" + future.Add(time.Minute).Format("15:04")
	win, err := window.Parse(spec)
	if err != nil {
		t.Fatal(err)
	}
	e.loop.Window = win

	e.api.responses = []*client.CheckinResponse{e.offer("1.1.0", false)}
	if err := e.loop.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if e.loop.St.Pending == nil {
		t.Fatal("install should have happened")
	}
	if e.rebooted != 0 {
		t.Fatal("reboot must wait for the window")
	}

	// Later ticks while pending: no reinstall, still no reboot.
	e.api.responses = []*client.CheckinResponse{e.offer("1.2.0", false)}
	_ = e.loop.Tick(ctx)
	if len(e.installer.calls) != 1 {
		t.Error("pending state must block further installs")
	}
	if e.rebooted != 0 {
		t.Error("still outside window")
	}

	// Window opens.
	e.loop.Window, _ = window.Parse("")
	_ = e.loop.Tick(ctx)
	if e.rebooted != 1 {
		t.Error("reboot must fire once the window opens")
	}
}

func TestEventQueueSurvivesDeliveryFailure(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_ = e.loop.Startup(ctx)

	e.api.eventsErr = context.DeadlineExceeded
	e.api.responses = []*client.CheckinResponse{e.offer("1.1.0", false)}
	if err := e.loop.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.loop.St.Queue) == 0 {
		t.Fatal("events must stay queued while the server is unreachable")
	}
	queued := len(e.loop.St.Queue)

	e.api.eventsErr = nil
	_ = e.loop.Tick(ctx)
	if len(e.loop.St.Queue) != 0 {
		t.Error("queue must drain once delivery succeeds")
	}
	if len(e.api.events) < queued {
		t.Errorf("expected at least %d delivered events, got %d", queued, len(e.api.events))
	}
}

func TestRetiredDeviceStopsPolling(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_ = e.loop.Startup(ctx)
	e.api.checkinErr = &client.APIError{Status: http.StatusGone, Message: "device retired"}
	if err := e.loop.Tick(ctx); err != ErrRetired {
		t.Fatalf("expected ErrRetired, got %v", err)
	}
}
