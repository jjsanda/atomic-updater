// Package state persists the agent's durable state on the data partition
// (survives updates — the rootfs does not).
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Pending struct {
	RolloutID    string    `json:"rollout_id"`
	BundleID     string    `json:"bundle_id"`
	Version      string    `json:"version"`
	SHA256       string    `json:"sha256"`
	ExpectedSlot string    `json:"expected_slot"`
	InstalledAt  time.Time `json:"installed_at"`
}

type LastUpdate struct {
	Outcome  string `json:"outcome"` // none|ok|fail|rollback
	BundleID string `json:"bundle_id"`
	Reason   string `json:"reason"`
}

type QueuedEvent struct {
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
}

type State struct {
	DeviceID   string `json:"device_id"`
	LastBootID string `json:"last_boot_id"`
	BootCount  int64  `json:"boot_count"`
	Installed  struct {
		BundleID string `json:"bundle_id"`
		Version  string `json:"version"`
	} `json:"installed"`
	LastUpdate LastUpdate    `json:"last_update"`
	Pending    *Pending      `json:"pending,omitempty"`
	Queue      []QueuedEvent `json:"queue,omitempty"`
}

// Load reads state.json, returning a fresh zero state when absent.
func Load(dataDir string) (*State, error) {
	path := filepath.Join(dataDir, "state.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &State{LastUpdate: LastUpdate{Outcome: "none"}}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		// A corrupt state file must not brick the agent: start fresh.
		return &State{LastUpdate: LastUpdate{Outcome: "none"}}, nil
	}
	if s.LastUpdate.Outcome == "" {
		s.LastUpdate.Outcome = "none"
	}
	return &s, nil
}

// Save writes atomically (tmp + rename) so a power cut never leaves a
// half-written state file.
func (s *State) Save(dataDir string) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dataDir, "state.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("atomic state replace: %w", err)
	}
	return nil
}
