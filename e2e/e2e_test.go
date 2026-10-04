// End-to-end OTA scenarios (design doc §13.2) against the real stack:
// atomic-server + Postgres + MinIO + containerized devices running real
// RAUC installs of signed bundles. Run with `make e2e`.
package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("ATOMIC_E2E") != "1" {
		fmt.Println("skipping e2e suite (set ATOMIC_E2E=1 or use `make e2e`)")
		os.Exit(0)
	}
	var err error
	h, err = setupHarness()
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e setup failed:", err)
		if h != nil {
			h.teardown()
		}
		os.Exit(1)
	}
	code := m.Run()
	h.teardown()
	os.Exit(code)
}

// beginScenario gives each test a clean rollout slate and aborts its
// rollouts afterwards. Sims are per-test (fresh containers, fresh /data).
func beginScenario(t *testing.T) {
	t.Helper()
	if err := h.Admin.AbortActiveRollouts(); err != nil {
		t.Fatalf("aborting stale rollouts: %v", err)
	}
	t.Cleanup(func() {
		_ = h.Admin.AbortActiveRollouts()
	})
}

func startRolloutFor(t *testing.T, version string, pct int, allowDowngrade bool) *Rollout {
	t.Helper()
	bundle, err := h.Admin.BundleByVersion(version)
	if err != nil {
		t.Fatalf("bundle %s: %v", version, err)
	}
	ro, err := h.Admin.StartRollout(bundle.ID, "dev", pct, allowDowngrade)
	if err != nil {
		t.Fatalf("rollout for %s: %v", version, err)
	}
	t.Logf("rollout %s active: version %s at %d%%", ro.ID, version, pct)
	return ro
}

func waitEnrolled(t *testing.T, serial string) *Device {
	t.Helper()
	return h.waitDevice(t, serial, "enrolled and checked in", 2*time.Minute,
		func(d *Device) bool { return d.CurrentVersion != "" })
}

// --- §10.1 happy path ---

func TestHappyPath(t *testing.T) {
	beginScenario(t)
	sim := h.StartSim(t, "happy-1", nil)
	dev := waitEnrolled(t, "happy-1")
	if dev.CurrentVersion != "1.0.0" || dev.CurrentSlot != "A" {
		t.Fatalf("factory state wrong: %+v", dev)
	}

	startRolloutFor(t, "1.1.0", 100, false)

	dev = h.waitDevice(t, "happy-1", "updated to 1.1.0 and confirmed", 4*time.Minute,
		func(d *Device) bool {
			return d.CurrentVersion == "1.1.0" && d.LastUpdateOutcome == "ok"
		})
	if dev.CurrentSlot != "B" {
		t.Errorf("expected slot flip A→B, got %s", dev.CurrentSlot)
	}

	events, err := h.Admin.DeviceEvents(dev.ID, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"offer", "download", "install-started", "install-ok"} {
		if !hasEvent(events, want, "") {
			t.Errorf("missing %s event; got %v", want, eventTypes(events))
		}
	}

	env := sim.BootEnv()
	if !strings.HasPrefix(env["BOOT_ORDER"], "B") {
		t.Errorf("BOOT_ORDER should prefer B, env: %v", env)
	}
	if env["BOOT_B_LEFT"] != "3" {
		t.Errorf("confirmed slot must have 3 attempts, env: %v", env)
	}
	if sim.RescueHalted() {
		t.Fatal("device fell into rescue halt")
	}
}

// --- §10.3 failed health check → mark-bad → old slot ---

func TestRollbackOnHealthFail(t *testing.T) {
	beginScenario(t)
	sim := h.StartSim(t, "healthfail-1", nil)
	waitEnrolled(t, "healthfail-1")

	startRolloutFor(t, "1.3.0", 100, false) // ships an always-failing probe

	dev := h.waitDevice(t, "healthfail-1", "rolled back to 1.0.0", 4*time.Minute,
		func(d *Device) bool {
			return d.LastUpdateOutcome == "rollback" && d.CurrentVersion == "1.0.0"
		})

	events, err := h.Admin.DeviceEvents(dev.ID, 200)
	if err != nil {
		t.Fatal(err)
	}
	if !hasEvent(events, "rollback", "expected slot") {
		t.Errorf("rollback event with reason missing; got %v", eventTypes(events))
	}

	logs := sim.Logs()
	if !strings.Contains(logs, "as bad") && !strings.Contains(logs, "result: FAIL") {
		t.Error("simulator log carries no health failure evidence (mark-bad / FAIL)")
	}
	env := sim.BootEnv()
	if env["BOOT_B_LEFT"] != "0" {
		t.Errorf("bad slot must have 0 attempts left, env: %v", env)
	}
	if sim.RescueHalted() {
		t.Fatal("device fell into rescue halt")
	}
}

// --- §10.2 failed boot (kernel panic) → counter exhaustion → old slot ---

func TestRollbackOnBootLoop(t *testing.T) {
	beginScenario(t)
	sim := h.StartSim(t, "bootloop-1", nil)
	waitEnrolled(t, "bootloop-1")

	startRolloutFor(t, "1.2.0", 100, false) // rootfs carries /etc/BOOT_PANIC

	dev := h.waitDevice(t, "bootloop-1", "rolled back after boot loop", 6*time.Minute,
		func(d *Device) bool {
			return d.LastUpdateOutcome == "rollback" && d.CurrentVersion == "1.0.0"
		})

	panics := strings.Count(sim.Logs(), "KERNEL PANIC (simulated)")
	if panics != 3 {
		t.Errorf("expected exactly 3 panic boot attempts (BOOT_x_LEFT=3), saw %d", panics)
	}
	env := sim.BootEnv()
	if env["BOOT_B_LEFT"] != "0" {
		t.Errorf("exhausted slot should be at 0, env: %v", env)
	}
	if sim.RescueHalted() {
		t.Fatal("device fell into rescue halt — the OLD slot must still boot")
	}

	events, _ := h.Admin.DeviceEvents(dev.ID, 200)
	if !hasEvent(events, "rollback", "") {
		t.Errorf("rollback not reported; got %v", eventTypes(events))
	}
}

// --- §11.1: tampered/untrusted bundle rejected before any slot write ---

func TestRejectUntrustedBundle(t *testing.T) {
	beginScenario(t)
	sim := h.StartSim(t, "badkey-1", nil)
	waitEnrolled(t, "badkey-1")

	before, _ := sim.Exec("sha256sum", "/data/slots/rootfs-B.img")

	startRolloutFor(t, "1.4.0", 100, false) // signed with the out-of-PKI key

	dev := h.waitDevice(t, "badkey-1", "install failure reported", 4*time.Minute,
		func(d *Device) bool { return d.LastUpdateOutcome == "fail" })
	if dev.CurrentVersion != "1.0.0" {
		t.Fatalf("device must stay on 1.0.0, is on %s", dev.CurrentVersion)
	}

	events, _ := h.Admin.DeviceEvents(dev.ID, 200)
	if !hasEvent(events, "install-fail", "signature") {
		t.Errorf("expected install-fail event mentioning signature; got %v", eventTypes(events))
	}

	after, _ := sim.Exec("sha256sum", "/data/slots/rootfs-B.img")
	if before != after {
		t.Error("inactive slot was modified by an untrusted bundle")
	}
	env := sim.BootEnv()
	if !strings.HasPrefix(env["BOOT_ORDER"], "A") {
		t.Errorf("boot order must not flip for a rejected bundle, env: %v", env)
	}

	// §10.4: the device is never re-offered the failed bundle — after more
	// check-ins there is still exactly one offer for this rollout.
	time.Sleep(10 * time.Second)
	events, _ = h.Admin.DeviceEvents(dev.ID, 200)
	offers := 0
	for _, e := range events {
		if e.EventType == "offer" {
			offers++
		}
	}
	if offers != 1 {
		t.Errorf("expected exactly 1 offer (no retry of a failed bundle), got %d", offers)
	}
}

// --- §11.1 rollback attack: downgrades rejected unless explicitly allowed ---

func TestRejectDowngrade(t *testing.T) {
	beginScenario(t)
	h.StartSim(t, "downgrade-1", nil)
	dev := waitEnrolled(t, "downgrade-1")

	watermark := int64(0)
	if events, err := h.Admin.DeviceEvents(dev.ID, 1); err == nil && len(events) > 0 {
		watermark = events[0].ID
	}

	startRolloutFor(t, "0.9.0", 100, false)

	// Let several check-ins happen, then verify no offer was made.
	waitFor(t, "3 further checkins", 2*time.Minute, func() (bool, string) {
		events, err := h.Admin.DeviceEvents(dev.ID, 200)
		if err != nil {
			return false, err.Error()
		}
		checkins := 0
		for _, e := range events {
			if e.ID > watermark && e.EventType == "checkin" {
				checkins++
			}
		}
		return checkins >= 3, fmt.Sprintf("%d checkins", checkins)
	})
	events, _ := h.Admin.DeviceEvents(dev.ID, 200)
	for _, e := range events {
		if e.ID > watermark && e.EventType == "offer" {
			t.Fatalf("downgrade was offered: %s", string(e.Payload))
		}
	}
	cur, _ := h.Admin.DeviceBySerial("downgrade-1")
	if cur.CurrentVersion != "1.0.0" {
		t.Fatalf("device downgraded to %s without allow_downgrade", cur.CurrentVersion)
	}

	// Explicit operator downgrade (allow_downgrade=true) must proceed.
	if err := h.Admin.AbortActiveRollouts(); err != nil {
		t.Fatal(err)
	}
	startRolloutFor(t, "0.9.0", 100, true)
	h.waitDevice(t, "downgrade-1", "downgraded to 0.9.0", 4*time.Minute,
		func(d *Device) bool {
			return d.CurrentVersion == "0.9.0" && d.LastUpdateOutcome == "ok"
		})
}

// --- §13.2 resume after network drop mid-download ---

func TestResumeAfterNetworkDrop(t *testing.T) {
	beginScenario(t)
	if h.Tier == "fallback" {
		t.Skip("throttling needs NET_ADMIN (privileged tiers only)")
	}
	sim := h.StartSim(t, "netdrop-1", nil)
	dev := waitEnrolled(t, "netdrop-1")

	// ~42 MB bundle + ingress policing: even with TCP overshooting the
	// policed rate, the download window stays tens of seconds wide.
	if err := sim.ThrottleIngress("2mbit"); err != nil {
		t.Fatalf("tc ingress throttle: %v", err)
	}

	startRolloutFor(t, "1.5.0", 100, false)

	// Detect the partial file from INSIDE the container (100 ms resolution —
	// docker-exec polling from the host is too coarse).
	if out, err := sim.Exec("sh", "-c",
		`for i in $(seq 1 1200); do
		   [ -s /data/atomic-updater/pending.raucb.partial ] && exit 0
		   sleep 0.1
		 done; exit 1`); err != nil {
		t.Fatalf("download never started: %v %s", err, out)
	}
	t.Logf("%s cutting network mid-download", time.Now().Format("15:04:05.000"))
	if err := sim.NetworkDisconnect(); err != nil {
		t.Fatal(err)
	}

	// The cut must actually interrupt the transfer before we reconnect —
	// otherwise this test silently degrades into a plain happy path.
	waitFor(t, "download interruption evidence", 2*time.Minute, func() (bool, string) {
		logs := sim.Logs()
		if strings.Contains(logs, "download interrupted") ||
			strings.Contains(logs, "tick failed") {
			return true, ""
		}
		return false, "no interruption in agent log yet"
	})
	t.Logf("%s reconnecting", time.Now().Format("15:04:05.000"))
	if err := sim.NetworkConnect(); err != nil {
		t.Fatal(err)
	}

	h.waitDevice(t, "netdrop-1", "updated despite network drop", 6*time.Minute,
		func(d *Device) bool {
			return d.CurrentVersion == "1.5.0" && d.LastUpdateOutcome == "ok"
		})

	// The download event must prove a Range resume happened.
	events, _ := h.Admin.DeviceEvents(dev.ID, 200)
	resumed := false
	for _, e := range events {
		if e.EventType == "download" && strings.Contains(string(e.Payload), `"resumed_from"`) &&
			!strings.Contains(string(e.Payload), `"resumed_from":0`) {
			resumed = true
		}
	}
	if !resumed {
		t.Error("no download event with resumed_from > 0 — resume did not happen")
	}
}

// --- §8.3/§10.4 auto-pause on failure spike (synthetic 30-device cohort) ---

func TestAutoPause(t *testing.T) {
	beginScenario(t)

	bundle, err := h.Admin.BundleByVersion("1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	ro, err := h.Admin.StartRollout(bundle.ID, "dev", 100, false)
	if err != nil {
		t.Fatal(err)
	}

	t.Log("enrolling 30 synthetic devices over real mTLS")
	var offered []*SyntheticDevice
	for i := 0; i < 30; i++ {
		dev, err := h.EnrollSynthetic(fmt.Sprintf("synthetic-%02d", i))
		if err != nil {
			t.Fatalf("enroll synthetic-%02d: %v", i, err)
		}
		rolloutID, err := dev.Checkin("1.0.0")
		if err != nil {
			t.Fatalf("checkin synthetic-%02d: %v", i, err)
		}
		if rolloutID != ro.ID {
			t.Fatalf("synthetic-%02d expected offer of rollout %s, got %q", i, ro.ID, rolloutID)
		}
		offered = append(offered, dev)
	}

	// 2 failures / 30 offered = 6.7%/h > 2%/h with cohort ≥ 25 → sticky pause.
	if err := offered[0].ReportInstallFail(ro.ID, bundle.ID); err != nil {
		t.Fatal(err)
	}
	if err := offered[1].ReportInstallFail(ro.ID, bundle.ID); err != nil {
		t.Fatal(err)
	}

	got, err := h.Admin.GetRollout(ro.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "paused" {
		t.Fatalf("rollout should be auto-paused, is %s", got.Status)
	}
	if got.PausedReason == nil || !strings.Contains(*got.PausedReason, "auto") {
		t.Fatalf("paused_reason should record the auto-pause, got %v", got.PausedReason)
	}
	t.Logf("auto-paused: %s", *got.PausedReason)

	// Sticky: further offers stop while paused.
	if rolloutID, _ := offered[2].Checkin("1.0.0"); rolloutID != "" {
		t.Error("paused rollout must not be offered")
	}

	// Operator resume clears the reason.
	if err := h.Admin.PatchRollout(ro.ID, map[string]any{"status": "active"}); err != nil {
		t.Fatal(err)
	}
	got, _ = h.Admin.GetRollout(ro.ID)
	if got.Status != "active" || got.PausedReason != nil {
		t.Fatalf("resume failed: %+v", got)
	}
}

// --- §13.3 miniature soak: 5 sims × 10 sequential updates, zero bricks ---

func TestSoak(t *testing.T) {
	if os.Getenv("ATOMIC_E2E_SOAK") != "1" {
		t.Skip("soak disabled (set ATOMIC_E2E_SOAK=1 or use `make soak`)")
	}
	beginScenario(t)

	const simCount = 5
	const updates = 10

	t.Log("building soak bundles 2.0.0 … 2.0.9")
	for i := 0; i < updates; i++ {
		if err := h.BuildAndUploadBundle(fmt.Sprintf("2.0.%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.Admin.Scan(); err != nil {
		t.Fatal(err)
	}

	sims := make([]*Sim, simCount)
	serials := make([]string, simCount)
	for i := range sims {
		serials[i] = fmt.Sprintf("soak-%d", i)
		sims[i] = h.StartSim(t, serials[i], nil)
	}
	for _, serial := range serials {
		waitEnrolled(t, serial)
	}

	for i := 0; i < updates; i++ {
		version := fmt.Sprintf("2.0.%d", i)
		startRolloutFor(t, version, 100, false)
		for _, serial := range serials {
			h.waitDevice(t, serial, "at "+version, 6*time.Minute, func(d *Device) bool {
				return d.CurrentVersion == version && d.LastUpdateOutcome == "ok"
			})
		}
		for s, sim := range sims {
			if sim.RescueHalted() {
				t.Fatalf("BRICK: %s rescue-halted during update %d", serials[s], i)
			}
		}
		if err := h.Admin.AbortActiveRollouts(); err != nil {
			t.Fatal(err)
		}
		t.Logf("soak wave %d/%d complete on all %d devices", i+1, updates, simCount)
	}
}
