package storage

// Integration tests against a real Postgres 15. Skipped unless
// TEST_DATABASE_URL is set — run via scripts/dev/run-storage-tests.sh or
// `make server-test-integration`.

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"atomic-updater/server/internal/model"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; run scripts/dev/run-storage-tests.sh")
	}
	if err := Migrate(dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Order respects FKs.
		for _, table := range []string{"device_events", "admin_sessions",
			"revoked_certs", "rollouts", "devices", "bundles"} {
			_, _ = db.Pool.Exec(context.Background(), "DELETE FROM "+table)
		}
		db.Close()
	})
	return db
}

func mkBundle(t *testing.T, db *DB, version, hw string) *model.Bundle {
	t.Helper()
	b := &model.Bundle{
		ID: uuid.NewString(), Version: version, HardwareType: hw,
		ObjectKey: "signed/test-" + hw + "-" + version + ".raucb",
		SHA256:    "d0b425e00e15a0d36ffc2b0c1a1f3b3c4d5e6f708192a3b4c5d6e7f8091a2b3c",
		SizeBytes: 1024,
	}
	if err := db.InsertBundle(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	return b
}

func mkDevice(t *testing.T, db *DB, channel string) *model.Device {
	t.Helper()
	id := uuid.NewString()
	d := &model.Device{
		ID: id, CertFingerprint: "fp-" + id, PubkeyFingerprint: "pub-" + id,
		HardwareType: "rpi-cm4", Serial: "SN-" + id[:8], Channel: channel,
	}
	if err := db.CreateDevice(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestIntegrationDeviceLifecycle(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	dev := mkDevice(t, db, "dev")
	got, err := db.GetDeviceByFingerprint(ctx, dev.CertFingerprint)
	if err != nil || got == nil || got.ID != dev.ID {
		t.Fatalf("fingerprint lookup: %+v %v", got, err)
	}

	// Checkin with an unknown bundle id degrades to NULL instead of an FK error.
	unknown := uuid.NewString()
	if err := db.UpdateDeviceCheckin(ctx, dev.ID, "A", "1.0.0", &unknown, 3, true, "none"); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetDevice(ctx, dev.ID)
	if got.CurrentBundleID != nil {
		t.Error("unknown bundle id should store NULL")
	}
	if got.CurrentSlot != "A" || got.CurrentVersion != "1.0.0" || got.BootCount != 3 {
		t.Errorf("checkin fields not persisted: %+v", got)
	}
	if got.LastSeenAt == nil {
		t.Error("last_seen_at not set")
	}

	// Known bundle id is stored.
	b := mkBundle(t, db, "1.0.0", "rpi-cm4")
	if err := db.UpdateDeviceCheckin(ctx, dev.ID, "A", "1.0.0", &b.ID, 4, true, "ok"); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetDevice(ctx, dev.ID)
	if got.CurrentBundleID == nil || *got.CurrentBundleID != b.ID {
		t.Error("known bundle id should persist")
	}

	// Admin updates + listing filters.
	beta := "beta"
	if _, err := db.UpdateDeviceAdmin(ctx, dev.ID, &beta, nil); err != nil {
		t.Fatal(err)
	}
	list, total, err := db.ListDevices(ctx, DeviceFilter{Channel: "beta"})
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("filtered list: n=%d total=%d err=%v", len(list), total, err)
	}
	list, _, _ = db.ListDevices(ctx, DeviceFilter{Query: dev.Serial[:6]})
	if len(list) != 1 {
		t.Error("serial search failed")
	}

	// Revocation.
	if err := db.RevokeCert(ctx, dev.CertFingerprint, "01", "test"); err != nil {
		t.Fatal(err)
	}
	revoked, _ := db.IsRevoked(ctx, dev.CertFingerprint)
	if !revoked {
		t.Error("cert should be revoked")
	}
	if err := db.RevokeCert(ctx, dev.CertFingerprint, "01", "twice"); err != nil {
		t.Error("revoke must be idempotent:", err)
	}
}

func TestIntegrationBundleConstraints(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	b := mkBundle(t, db, "2.0.0", "rpi-cm4")

	dup := *b
	dup.ID = uuid.NewString()
	dup.ObjectKey = "signed/other-key.raucb"
	if err := db.InsertBundle(ctx, &dup); err != ErrDuplicateBundle {
		t.Fatalf("duplicate version+hw: want ErrDuplicateBundle, got %v", err)
	}
}

func TestIntegrationRolloutFlowAndAutoPause(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	b := mkBundle(t, db, "3.0.0", "rpi-cm4")
	created, err := db.CreateRollout(ctx, &model.Rollout{
		ID: uuid.NewString(), BundleID: b.ID, Channel: "dev", Percentage: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != "draft" || created.BundleVersion != "3.0.0" {
		t.Fatalf("created: %+v", created)
	}

	// draft → active
	active, err := db.TransitionRollout(ctx, created.ID, "active", []string{"draft", "paused"})
	if err != nil || active == nil || active.ActivatedAt == nil {
		t.Fatalf("activate: %+v %v", active, err)
	}
	// invalid: draft ← active is not allowed by any caller list we pass
	if r, _ := db.TransitionRollout(ctx, created.ID, "active", []string{"draft"}); r != nil {
		t.Error("transition with wrong from-set must return nil")
	}

	found, err := db.NewestActiveRollout(ctx, "dev", "rpi-cm4")
	if err != nil || found == nil || found.ID != created.ID {
		t.Fatalf("newest active: %+v %v", found, err)
	}
	if found2, _ := db.NewestActiveRollout(ctx, "stable", "rpi-cm4"); found2 != nil {
		t.Error("channel filter leaked")
	}

	// Auto-pause: 30 offered devices, 1 failure = 3.33% > 2% with cohort ≥ 25.
	for i := 0; i < 30; i++ {
		d := mkDevice(t, db, "dev")
		if err := db.LogOffer(ctx, d.ID, created.ID, b.ID); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			payload, _ := json.Marshal(map[string]string{
				"rollout_id": created.ID, "bundle_id": b.ID,
			})
			if err := db.InsertEvent(ctx, d.ID, "install-fail", payload); err != nil {
				t.Fatal(err)
			}
			// The §10.4 guard: this device must never be re-offered b.
			failed, _ := db.LastFailedBundleID(ctx, d.ID)
			if failed != b.ID {
				t.Errorf("LastFailedBundleID = %q, want %q", failed, b.ID)
			}
		}
	}

	// Below-cohort threshold must NOT pause.
	paused, err := db.EvaluateAutoPause(ctx, created.ID, 50, 2)
	if err != nil || paused {
		t.Fatalf("min-cohort 50 should not pause (30 offered): paused=%v err=%v", paused, err)
	}
	// Real thresholds pause.
	paused, err = db.EvaluateAutoPause(ctx, created.ID, 25, 2)
	if err != nil || !paused {
		t.Fatalf("expected auto-pause: paused=%v err=%v", paused, err)
	}
	r, _ := db.GetRollout(ctx, created.ID)
	if r.Status != "paused" || r.PausedReason == nil {
		t.Fatalf("rollout not paused: %+v", r)
	}
	// Sticky: a second evaluation must not error and must not "re-pause".
	paused, _ = db.EvaluateAutoPause(ctx, created.ID, 25, 2)
	if paused {
		t.Error("already-paused rollout reported as newly paused")
	}
	// Operator resume clears the reason.
	resumed, err := db.TransitionRollout(ctx, created.ID, "active", []string{"draft", "paused"})
	if err != nil || resumed == nil || resumed.PausedReason != nil {
		t.Fatalf("resume: %+v %v", resumed, err)
	}

	stats, err := db.RolloutStats(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Offered != 30 || stats.Failed != 1 {
		t.Errorf("stats: %+v", stats)
	}

	// Offer dedup window.
	devs, _, _ := db.ListDevices(ctx, DeviceFilter{Limit: 1})
	within, err := db.OfferLoggedWithin(ctx, devs[0].ID, created.ID, time.Hour)
	if err != nil || !within {
		t.Errorf("offer should be within window: %v %v", within, err)
	}
	within, _ = db.OfferLoggedWithin(ctx, devs[0].ID, uuid.NewString(), time.Hour)
	if within {
		t.Error("different rollout must not match")
	}
}

func TestIntegrationSummaryBuckets(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	d := mkDevice(t, db, "stable")
	_ = db.UpdateDeviceCheckin(ctx, d.ID, "B", "9.9.9", nil, 1, true, "none")
	payload, _ := json.Marshal(map[string]string{"reason": "test"})
	_ = db.InsertEvent(ctx, d.ID, "rollback", payload)

	sum, err := db.Summary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sum.DevicesTotal != 1 || sum.ByChannel["stable"] != 1 || sum.ByVersion["9.9.9"] != 1 {
		t.Errorf("summary: %+v", sum)
	}
	if len(sum.MarkBad1h) != 24 {
		t.Fatalf("expected 24 hourly buckets, got %d", len(sum.MarkBad1h))
	}
	var totalBad int64
	for _, bkt := range sum.MarkBad1h {
		totalBad += bkt.Count
	}
	if totalBad != 1 {
		t.Errorf("rollback event not counted in buckets (total=%d)", totalBad)
	}
}

func TestIntegrationSessions(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := db.CreateSession(ctx, "hash1", "admin", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	user, _, err := db.GetSession(ctx, "hash1")
	if err != nil || user != "admin" {
		t.Fatalf("get session: %q %v", user, err)
	}
	if err := db.DeleteSession(ctx, "hash1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.GetSession(ctx, "hash1"); err == nil {
		t.Error("deleted session still readable")
	}
}
