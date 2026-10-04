package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadFreshState(t *testing.T) {
	s, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if s.LastUpdate.Outcome != "none" || s.BootCount != 0 || s.Pending != nil {
		t.Errorf("fresh state wrong: %+v", s)
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s := &State{
		DeviceID:   "dev-1",
		LastBootID: "boot-1",
		BootCount:  3,
		LastUpdate: LastUpdate{Outcome: "ok", BundleID: "b1"},
		Pending: &Pending{
			RolloutID: "r1", BundleID: "b2", Version: "1.1.0",
			SHA256: "aa", ExpectedSlot: "B", InstalledAt: time.Now(),
		},
		Queue: []QueuedEvent{{EventType: "download", Payload: json.RawMessage(`{"bytes":5}`)}},
	}
	s.Installed.BundleID = "b1"
	s.Installed.Version = "1.0.0"
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.DeviceID != "dev-1" || got.BootCount != 3 ||
		got.Pending == nil || got.Pending.ExpectedSlot != "B" ||
		len(got.Queue) != 1 || got.Installed.Version != "1.0.0" {
		t.Errorf("roundtrip lost data: %+v", got)
	}
	// No stray temp file left behind.
	if _, err := os.Stat(filepath.Join(dir, "state.json.tmp")); !os.IsNotExist(err) {
		t.Error("temp file leaked")
	}
}

func TestCorruptStateStartsFresh(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.BootCount != 0 || s.LastUpdate.Outcome != "none" {
		t.Errorf("corrupt state should reset, got %+v", s)
	}
}
