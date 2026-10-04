// Package model holds the domain types shared by storage, rollout logic and
// the HTTP layer. IDs are canonical lowercase UUID strings; versions are
// canonical semver without a leading 'v'.
package model

import (
	"encoding/json"
	"time"
)

type Device struct {
	ID                string
	CertFingerprint   string
	PubkeyFingerprint string
	HardwareType      string
	Serial            string
	Channel           string
	Status            string
	CurrentBundleID   *string
	CurrentVersion    string
	CurrentSlot       string
	BootCount         int64
	LastUpdateOutcome string
	Healthy           bool
	LastSeenAt        *time.Time
	CreatedAt         time.Time
}

type Bundle struct {
	ID           string
	Version      string
	HardwareType string
	ObjectKey    string
	SHA256       string
	SizeBytes    int64
	SignedBy     string
	Status       string
	UploadedAt   time.Time
}

type Rollout struct {
	ID             string
	BundleID       string
	Channel        string
	Percentage     int
	AllowDowngrade bool
	Status         string
	PausedReason   *string
	CreatedAt      time.Time
	ActivatedAt    *time.Time

	// Denormalized for API listings (joined from bundles).
	BundleVersion string
	HardwareType  string
}

type Event struct {
	ID        int64
	DeviceID  string
	EventType string
	Payload   json.RawMessage
	CreatedAt time.Time
}

type RolloutStats struct {
	Offered     int64
	InstalledOK int64
	Failed      int64
	FailRate1h  float64
}

type Summary struct {
	DevicesTotal int64
	ByChannel    map[string]int64
	ByVersion    map[string]int64
	MarkBad1h    []MarkBadBucket
}

type MarkBadBucket struct {
	Bucket time.Time
	Count  int64
}

// Enumerations (mirrored by CHECK constraints in the schema).
var (
	HardwareTypes    = map[string]bool{"rpi-cm4": true, "x86_64-generic": true}
	Channels         = map[string]bool{"dev": true, "beta": true, "stable": true}
	DeviceEventTypes = map[string]bool{
		"download": true, "install-started": true, "install-ok": true,
		"install-fail": true, "rollback": true,
	}
	UpdateOutcomes = map[string]bool{"none": true, "ok": true, "fail": true, "rollback": true}
)
