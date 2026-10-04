// Package agent is the check-in agent's state machine (design doc §7.5,
// §10): poll → download → verify → install → reboot-in-window, plus
// boot-outcome reporting (install-ok / rollback) after the reboot.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"atomic-updater/agent/internal/client"
	"atomic-updater/agent/internal/config"
	"atomic-updater/agent/internal/download"
	"atomic-updater/agent/internal/installer"
	"atomic-updater/agent/internal/state"
	"atomic-updater/agent/internal/window"
)

// ErrRetired signals the server permanently rejected this device (410).
var ErrRetired = errors.New("device retired by server")

// API is the server surface the loop needs (client.Client implements it).
type API interface {
	Checkin(client.CheckinRequest) (*client.CheckinResponse, error)
	Events([]client.DeviceEvent) error
}

type Loop struct {
	Cfg       *config.Config
	API       API
	Installer installer.Installer
	Download  *http.Client // plain client for presigned bundle URLs
	Window    window.Window
	St        *state.State
	Log       *slog.Logger

	// Consecutive complete-download checksum mismatches per bundle id. A
	// transfer corruption heals on retry; a corrupt PUBLISHED object never
	// does — after a few strikes it must surface as install-fail instead of
	// re-downloading forever.
	checksumStrikes map[string]int

	// Environment probes — overridable in tests and the simulator.
	Now     func() time.Time
	BootID  func() (string, error)
	Slot    func() (string, error)
	Version func() (string, error)
	Uptime  func() int64
	Reboot  func() error
}

func (l *Loop) bundlePath() string {
	return filepath.Join(l.Cfg.DataDir, "pending.raucb")
}

// Startup performs boot accounting and, when a pending update exists,
// decides install-ok vs rollback by comparing the booted slot+version with
// the expectation recorded before the reboot (§10.1 step 9, §10.2/10.3
// step 10). The agent only runs after atomic-health has confirmed the slot
// (systemd ordering), so a boot that reaches us is a confirmed boot.
func (l *Loop) Startup(ctx context.Context) error {
	bootID, err := l.BootID()
	if err != nil {
		return fmt.Errorf("boot id: %w", err)
	}
	newBoot := bootID != l.St.LastBootID
	if newBoot {
		l.St.LastBootID = bootID
		l.St.BootCount++
	}

	if newBoot && l.St.Pending != nil {
		p := l.St.Pending
		slot, err := l.Slot()
		if err != nil {
			return err
		}
		version, err := l.Version()
		if err != nil {
			return err
		}
		if slot == p.ExpectedSlot && version == p.Version {
			l.St.Installed.BundleID = p.BundleID
			l.St.Installed.Version = p.Version
			l.St.LastUpdate = state.LastUpdate{Outcome: "ok", BundleID: p.BundleID}
			l.queueEvent("install-ok", map[string]string{
				"rollout_id": p.RolloutID, "bundle_id": p.BundleID, "version": p.Version,
			})
			l.Log.Info("update confirmed", "version", p.Version, "slot", slot)
		} else {
			reason := fmt.Sprintf("booted slot %s version %s, expected slot %s version %s",
				slot, version, p.ExpectedSlot, p.Version)
			l.St.LastUpdate = state.LastUpdate{Outcome: "rollback", BundleID: p.BundleID, Reason: reason}
			l.queueEvent("rollback", map[string]string{
				"rollout_id": p.RolloutID, "bundle_id": p.BundleID, "reason": reason,
			})
			l.Log.Warn("rollback detected", "reason", reason)
		}
		l.St.Pending = nil
		_ = os.Remove(l.bundlePath())
		_ = os.Remove(l.bundlePath() + ".partial")
	}
	return l.St.Save(l.Cfg.DataDir)
}

// Tick is one poll iteration.
func (l *Loop) Tick(ctx context.Context) error {
	l.flushQueue()

	slot, err := l.Slot()
	if err != nil {
		return err
	}
	version, err := l.Version()
	if err != nil {
		return err
	}
	var bundleID *string
	if l.St.Installed.BundleID != "" && l.St.Installed.Version == version {
		id := l.St.Installed.BundleID
		bundleID = &id
	}
	var lastBundle *string
	if l.St.LastUpdate.BundleID != "" {
		b := l.St.LastUpdate.BundleID
		lastBundle = &b
	}
	resp, err := l.API.Checkin(client.CheckinRequest{
		Slot:      slot,
		Version:   version,
		BundleID:  bundleID,
		BootCount: l.St.BootCount,
		UptimeS:   l.Uptime(),
		Healthy:   true, // the agent only runs after atomic-health passed
		LastUpdate: client.LastUpdate{
			Outcome:  l.St.LastUpdate.Outcome,
			BundleID: lastBundle,
			Reason:   l.St.LastUpdate.Reason,
		},
	})
	if err != nil {
		var apiErr *client.APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusGone {
			return ErrRetired
		}
		return fmt.Errorf("checkin: %w", err)
	}

	if l.St.Pending != nil {
		// Installed, waiting for the maintenance window. Ignore new offers
		// until the pending update has been booted.
		l.maybeReboot()
		return nil
	}
	if resp.Update == nil {
		return nil
	}
	return l.applyUpdate(ctx, resp.Update, slot, version)
}

func (l *Loop) applyUpdate(ctx context.Context, u *client.UpdateAssignment, slot, version string) error {
	// Agent-side downgrade guard (§11.1) — defense in depth behind the
	// server-side check.
	if !u.AllowDowngrade && compareVersions(u.Version, version) <= 0 {
		l.Log.Warn("refusing downgrade offer", "offered", u.Version, "running", version)
		return nil
	}

	dest := l.bundlePath()
	if sum, err := download.FileSHA256(dest); err != nil || sum != u.SHA256 {
		res, err := download.Fetch(ctx, l.Download, u.URL, dest, u.SHA256,
			l.Cfg.DownloadStallTimeout)
		if err != nil {
			if strings.Contains(err.Error(), "sha256 mismatch") {
				if l.checksumStrikes == nil {
					l.checksumStrikes = map[string]int{}
				}
				l.checksumStrikes[u.BundleID]++
				if l.checksumStrikes[u.BundleID] >= 3 {
					reason := fmt.Sprintf("bundle object corrupt: %d consecutive checksum mismatches: %v",
						l.checksumStrikes[u.BundleID], err)
					l.St.LastUpdate = state.LastUpdate{Outcome: "fail", BundleID: u.BundleID, Reason: reason}
					l.queueEvent("install-fail", map[string]string{
						"rollout_id": u.RolloutID, "bundle_id": u.BundleID, "reason": reason,
					})
					_ = l.St.Save(l.Cfg.DataDir)
					l.flushQueue()
					l.Log.Error("giving up on corrupt bundle object", "bundle", u.BundleID)
					delete(l.checksumStrikes, u.BundleID)
					return nil
				}
			}
			// Partial file is kept; the next tick gets a fresh presigned URL
			// and resumes.
			return fmt.Errorf("download %s: %w", u.Version, err)
		}
		delete(l.checksumStrikes, u.BundleID)
		l.queueEvent("download", map[string]any{
			"rollout_id": u.RolloutID, "bundle_id": u.BundleID,
			"bytes": res.Bytes, "resumed_from": res.ResumedFrom,
		})
		l.Log.Info("bundle downloaded", "version", u.Version,
			"bytes", res.Bytes, "resumed_from", res.ResumedFrom)
	} else {
		l.Log.Info("bundle already downloaded and verified", "version", u.Version)
	}

	l.queueEvent("install-started", map[string]string{
		"rollout_id": u.RolloutID, "bundle_id": u.BundleID, "version": u.Version,
	})
	l.flushQueue()

	// Bounded retries: a transiently busy device (EBUSY on dm/loop setup,
	// unsettled filesystem) must not become a terminal install-fail — that
	// permanently blocks this bundle for this device (§10.4).
	var out string
	var err error
	for attempt := 0; ; attempt++ {
		out, err = l.Installer.Install(ctx, dest)
		if err == nil || attempt >= l.Cfg.InstallRetries {
			break
		}
		l.Log.Warn("install attempt failed, retrying",
			"attempt", attempt+1, "of", l.Cfg.InstallRetries+1, "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(jitter(l.Now(), l.Cfg.InstallRetryDelay)):
		}
	}
	if err != nil {
		reason := fmt.Sprintf("%v: %s", err, tail(out, 512))
		l.St.LastUpdate = state.LastUpdate{Outcome: "fail", BundleID: u.BundleID, Reason: reason}
		l.queueEvent("install-fail", map[string]string{
			"rollout_id": u.RolloutID, "bundle_id": u.BundleID, "reason": reason,
		})
		_ = os.Remove(dest)
		_ = l.St.Save(l.Cfg.DataDir)
		l.flushQueue()
		l.Log.Error("install failed", "version", u.Version, "err", err)
		return nil // reported via events; not a loop error
	}

	l.St.Pending = &state.Pending{
		RolloutID:    u.RolloutID,
		BundleID:     u.BundleID,
		Version:      u.Version,
		SHA256:       u.SHA256,
		ExpectedSlot: otherSlot(slot),
		InstalledAt:  l.Now(),
	}
	if err := l.St.Save(l.Cfg.DataDir); err != nil {
		return err
	}
	l.Log.Info("install complete, awaiting reboot",
		"version", u.Version, "expected_slot", otherSlot(slot),
		"window", l.Cfg.MaintenanceWindow)
	l.maybeReboot()
	return nil
}

func (l *Loop) maybeReboot() {
	if l.St.Pending == nil {
		return
	}
	if !l.Window.Contains(l.Now()) {
		l.Log.Info("outside maintenance window, reboot deferred")
		return
	}
	l.flushQueue()
	l.Log.Info("rebooting into new slot", "expected_slot", l.St.Pending.ExpectedSlot)
	if err := l.Reboot(); err != nil {
		l.Log.Error("reboot command failed", "err", err)
	}
}

// Run polls forever with jitter (60s ± 10s per §7.5). The first tick fires
// immediately.
func (l *Loop) Run(ctx context.Context) error {
	for {
		if err := l.Tick(ctx); err != nil {
			if errors.Is(err, ErrRetired) {
				l.Log.Warn("server retired this device; agent stopping")
				return nil
			}
			l.Log.Error("tick failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(l.jitteredInterval()):
		}
	}
}

func (l *Loop) jitteredInterval() time.Duration {
	base := l.Cfg.PollInterval
	j := l.Cfg.PollJitter
	if j <= 0 {
		return base
	}
	// Cheap deterministic-enough jitter without importing math/rand: derive
	// from the monotonic clock.
	n := l.Now().UnixNano()
	offset := time.Duration(n%int64(2*j)) - j
	interval := base + offset
	if interval < time.Second {
		interval = time.Second
	}
	return interval
}

// --- helpers ---

func (l *Loop) queueEvent(eventType string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		raw = []byte(`{}`)
	}
	l.St.Queue = append(l.St.Queue, state.QueuedEvent{EventType: eventType, Payload: raw})
	_ = l.St.Save(l.Cfg.DataDir)
}

// flushQueue delivers queued events; on failure they stay queued for the
// next tick (events survive reboots via state.json).
func (l *Loop) flushQueue() {
	if len(l.St.Queue) == 0 {
		return
	}
	batch := make([]client.DeviceEvent, 0, len(l.St.Queue))
	for _, e := range l.St.Queue {
		batch = append(batch, client.DeviceEvent{EventType: e.EventType, Payload: e.Payload})
	}
	if err := l.API.Events(batch); err != nil {
		l.Log.Warn("event delivery failed, will retry", "queued", len(batch), "err", err)
		return
	}
	l.St.Queue = nil
	_ = l.St.Save(l.Cfg.DataDir)
}

// jitter spreads a delay over [d/2, 3d/2) so devices that fail in lockstep
// (thundering herd against a shared resource) do not also retry in lockstep.
func jitter(now time.Time, d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return d/2 + time.Duration(now.UnixNano()%int64(d))
}

func otherSlot(slot string) string {
	if slot == "A" {
		return "B"
	}
	return "A"
}

func compareVersions(a, b string) int {
	if !strings.HasPrefix(a, "v") {
		a = "v" + a
	}
	if !strings.HasPrefix(b, "v") {
		b = "v" + b
	}
	return semver.Compare(a, b)
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
