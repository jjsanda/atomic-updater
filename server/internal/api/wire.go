// Wire types for the device and admin APIs. docs/api.md is the authoritative
// contract; contract_test.go pins these structs to its golden fixtures. The
// agent keeps a hand-written duplicate (device/agent/internal/client/wire.go)
// pinned to the same fixtures.
package api

import (
	"encoding/json"
	"time"

	"atomic-updater/server/internal/model"
)

type errorResponse struct {
	Error string `json:"error"`
}

// --- device API ---

type EnrollRequest struct {
	CSRPEM       string `json:"csr_pem"`
	HardwareType string `json:"hardware_type"`
	Serial       string `json:"serial"`
}

type EnrollResponse struct {
	DeviceID       string `json:"device_id"`
	CertificatePEM string `json:"certificate_pem"`
	CAChainPEM     string `json:"ca_chain_pem"`
}

type LastUpdate struct {
	Outcome  string  `json:"outcome"`
	BundleID *string `json:"bundle_id"`
	Reason   string  `json:"reason"`
}

type CheckinRequest struct {
	Slot       string     `json:"slot"`
	Version    string     `json:"version"`
	BundleID   *string    `json:"bundle_id"`
	BootCount  int64      `json:"boot_count"`
	UptimeS    int64      `json:"uptime_s"`
	Healthy    bool       `json:"healthy"`
	LastUpdate LastUpdate `json:"last_update"`
}

type UpdateAssignment struct {
	RolloutID      string `json:"rollout_id"`
	BundleID       string `json:"bundle_id"`
	Version        string `json:"version"`
	URL            string `json:"url"`
	SHA256         string `json:"sha256"`
	SizeBytes      int64  `json:"size_bytes"`
	AllowDowngrade bool   `json:"allow_downgrade"`
}

type CheckinResponse struct {
	Update *UpdateAssignment `json:"update"`
}

type DeviceEvent struct {
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
}

type EventsRequest struct {
	Events []DeviceEvent `json:"events"`
}

type EventsResponse struct {
	Accepted int `json:"accepted"`
}

// --- admin API ---

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type DeviceJSON struct {
	ID                string     `json:"id"`
	HardwareType      string     `json:"hardware_type"`
	Serial            string     `json:"serial"`
	Channel           string     `json:"channel"`
	Status            string     `json:"status"`
	CurrentVersion    string     `json:"current_version"`
	CurrentBundleID   *string    `json:"current_bundle_id"`
	CurrentSlot       string     `json:"current_slot"`
	BootCount         int64      `json:"boot_count"`
	LastUpdateOutcome string     `json:"last_update_outcome"`
	Healthy           bool       `json:"healthy"`
	LastSeenAt        *time.Time `json:"last_seen_at"`
	CreatedAt         time.Time  `json:"created_at"`
}

func toDeviceJSON(d *model.Device) DeviceJSON {
	return DeviceJSON{
		ID:                d.ID,
		HardwareType:      d.HardwareType,
		Serial:            d.Serial,
		Channel:           d.Channel,
		Status:            d.Status,
		CurrentVersion:    d.CurrentVersion,
		CurrentBundleID:   d.CurrentBundleID,
		CurrentSlot:       d.CurrentSlot,
		BootCount:         d.BootCount,
		LastUpdateOutcome: d.LastUpdateOutcome,
		Healthy:           d.Healthy,
		LastSeenAt:        d.LastSeenAt,
		CreatedAt:         d.CreatedAt,
	}
}

type DevicesListResponse struct {
	Devices []DeviceJSON `json:"devices"`
	Total   int64        `json:"total"`
}

type PatchDeviceRequest struct {
	Channel *string `json:"channel"`
	Status  *string `json:"status"`
}

type RevokeRequest struct {
	Reason string `json:"reason"`
}

type EventJSON struct {
	ID        int64           `json:"id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

type EventsListResponse struct {
	Events []EventJSON `json:"events"`
}

type MarkBadBucketJSON struct {
	Bucket time.Time `json:"bucket"`
	Count  int64     `json:"count"`
}

type SummaryResponse struct {
	DevicesTotal int64               `json:"devices_total"`
	ByChannel    map[string]int64    `json:"by_channel"`
	ByVersion    map[string]int64    `json:"by_version"`
	MarkBadRate  []MarkBadBucketJSON `json:"markbad_rate_1h"`
}

type BundleJSON struct {
	ID           string    `json:"id"`
	Version      string    `json:"version"`
	HardwareType string    `json:"hardware_type"`
	ObjectKey    string    `json:"object_key"`
	SHA256       string    `json:"sha256"`
	SizeBytes    int64     `json:"size_bytes"`
	SignedBy     string    `json:"signed_by"`
	Status       string    `json:"status"`
	UploadedAt   time.Time `json:"uploaded_at"`
}

func toBundleJSON(b *model.Bundle) BundleJSON {
	return BundleJSON{
		ID: b.ID, Version: b.Version, HardwareType: b.HardwareType,
		ObjectKey: b.ObjectKey, SHA256: b.SHA256, SizeBytes: b.SizeBytes,
		SignedBy: b.SignedBy, Status: b.Status, UploadedAt: b.UploadedAt,
	}
}

type BundlesListResponse struct {
	Bundles []BundleJSON `json:"bundles"`
}

type RegisterBundleRequest struct {
	ObjectKey string `json:"object_key"`
}

type ScanResponse struct {
	Registered int `json:"registered"`
}

type PatchBundleRequest struct {
	Status string `json:"status"`
}

type RolloutJSON struct {
	ID             string     `json:"id"`
	BundleID       string     `json:"bundle_id"`
	BundleVersion  string     `json:"bundle_version"`
	HardwareType   string     `json:"hardware_type"`
	Channel        string     `json:"channel"`
	Percentage     int        `json:"percentage"`
	AllowDowngrade bool       `json:"allow_downgrade"`
	Status         string     `json:"status"`
	PausedReason   *string    `json:"paused_reason"`
	CreatedAt      time.Time  `json:"created_at"`
	ActivatedAt    *time.Time `json:"activated_at"`
}

func toRolloutJSON(r *model.Rollout) RolloutJSON {
	return RolloutJSON{
		ID: r.ID, BundleID: r.BundleID, BundleVersion: r.BundleVersion,
		HardwareType: r.HardwareType, Channel: r.Channel, Percentage: r.Percentage,
		AllowDowngrade: r.AllowDowngrade, Status: r.Status, PausedReason: r.PausedReason,
		CreatedAt: r.CreatedAt, ActivatedAt: r.ActivatedAt,
	}
}

type RolloutsListResponse struct {
	Rollouts []RolloutJSON `json:"rollouts"`
}

type CreateRolloutRequest struct {
	BundleID       string `json:"bundle_id"`
	Channel        string `json:"channel"`
	Percentage     int    `json:"percentage"`
	AllowDowngrade bool   `json:"allow_downgrade"`
}

type PatchRolloutRequest struct {
	Percentage *int    `json:"percentage"`
	Status     *string `json:"status"`
}

type RolloutStatsResponse struct {
	Offered      int64   `json:"offered"`
	InstalledOK  int64   `json:"installed_ok"`
	Failed       int64   `json:"failed"`
	FailRate1h   float64 `json:"fail_rate_1h"`
	PausedReason *string `json:"paused_reason"`
}
