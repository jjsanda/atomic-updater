// Package rollout implements deterministic phased-rollout assignment and the
// offer decision (design doc §8.3, §10.4; normative details in docs/api.md).
package rollout

import (
	"hash/fnv"
	"io"
)

// Included reports whether a device participates in a rollout at the given
// percentage.
//
// The hash input encoding is frozen by golden tests and MUST NOT change once
// devices are in the field: FNV-1a 32-bit over the canonical lowercase UUID
// string of the device, a single '|' byte, and the canonical lowercase UUID
// string of the rollout. A device's bucket (hash mod 100) is constant for a
// given rollout, so raising the percentage only ever adds devices — it never
// drops one that was already included.
func Included(deviceID, rolloutID string, percentage int) bool {
	if percentage <= 0 {
		return false
	}
	if percentage >= 100 {
		return true
	}
	return Bucket(deviceID, rolloutID) < uint32(percentage)
}

// Bucket returns the device's stable bucket in [0,100) for a rollout.
func Bucket(deviceID, rolloutID string) uint32 {
	h := fnv.New32a()
	_, _ = io.WriteString(h, deviceID)
	_, _ = h.Write([]byte{'|'})
	_, _ = io.WriteString(h, rolloutID)
	return h.Sum32() % 100
}
