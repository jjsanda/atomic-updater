// Wire types for the device API — a deliberate hand-written duplicate of
// server/internal/api/wire.go (device subset). docs/api.md is the contract;
// contract_test.go pins these structs to the same golden fixtures the server
// uses. The duplication keeps this module's dependency graph at stdlib +
// golang.org/x/mod.
package client

import "encoding/json"

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
