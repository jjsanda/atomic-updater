// Package bundles registers signed bundle objects (and their .meta.json
// sidecars) from the object store into Postgres. The server never parses
// RAUC bundles itself — signature truth lives on the devices and in the
// signing pipeline (docs/security.md).
package bundles

import (
	"encoding/json"
	"fmt"
	"regexp"

	"atomic-updater/server/internal/model"
	"atomic-updater/server/internal/rollout"
)

// Meta is the sidecar schema written by bundle/make-bundle.sh and the CI
// signing stage: <object_key>.meta.json next to the .raucb.
type Meta struct {
	Version      string `json:"version"`
	HardwareType string `json:"hardware_type"`
	SHA256       string `json:"sha256"`
	SizeBytes    int64  `json:"size_bytes"`
	SignedBy     string `json:"signed_by"`
}

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

func ParseMeta(data []byte) (*Meta, error) {
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("meta sidecar: %w", err)
	}
	v, err := rollout.NormalizeVersion(m.Version)
	if err != nil {
		return nil, fmt.Errorf("meta sidecar: %w", err)
	}
	m.Version = v
	if !model.HardwareTypes[m.HardwareType] {
		return nil, fmt.Errorf("meta sidecar: unknown hardware_type %q", m.HardwareType)
	}
	if !sha256Re.MatchString(m.SHA256) {
		return nil, fmt.Errorf("meta sidecar: malformed sha256")
	}
	if m.SizeBytes <= 0 {
		return nil, fmt.Errorf("meta sidecar: size_bytes must be positive")
	}
	return &m, nil
}
