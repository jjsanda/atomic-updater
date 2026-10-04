package rollout

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"testing"
)

// Golden vectors freeze the hash-input encoding forever. If any of these
// fail, deployed devices would flip rollout cohorts on upgrade — do NOT
// "fix" the expectation; fix the regression.
func TestBucketGolden(t *testing.T) {
	golden := []struct {
		device  string
		rollout string
		bucket  uint32
	}{
		{"6f3c63d2-3a3f-4d2e-8f5e-9b1a2c3d4e5f", "0d2f4a9e-1111-4222-8333-444455556666", 97},
		{"00000000-0000-0000-0000-000000000000", "0d2f4a9e-1111-4222-8333-444455556666", 77},
		{"ffffffff-ffff-ffff-ffff-ffffffffffff", "9e8d7c6b-5a49-4838-a7b6-c5d4e3f2a1b0", 31},
		{"123e4567-e89b-12d3-a456-426614174000", "9e8d7c6b-5a49-4838-a7b6-c5d4e3f2a1b0", 81},
		{"c56a4180-65aa-42ec-a945-5fd21dec0538", "f47ac10b-58cc-4372-a567-0e02b2c3d479", 80},
	}
	for _, g := range golden {
		if got := Bucket(g.device, g.rollout); got != g.bucket {
			t.Errorf("Bucket(%s, %s) = %d, want %d — HASH ENCODING CHANGED",
				g.device, g.rollout, got, g.bucket)
		}
		// Boundary semantics: strict less-than.
		if Included(g.device, g.rollout, int(g.bucket)) {
			t.Errorf("Included at percentage == bucket (%d) must be false", g.bucket)
		}
		if !Included(g.device, g.rollout, int(g.bucket)+1) {
			t.Errorf("Included at percentage == bucket+1 (%d) must be true", g.bucket+1)
		}
	}
}

func TestIncludedEdges(t *testing.T) {
	d, r := "6f3c63d2-3a3f-4d2e-8f5e-9b1a2c3d4e5f", "0d2f4a9e-1111-4222-8333-444455556666"
	if Included(d, r, 0) {
		t.Error("0%% must include nothing")
	}
	if !Included(d, r, 100) {
		t.Error("100%% must include everything")
	}
	if Included(d, r, -5) {
		t.Error("negative percentage must include nothing")
	}
	if !Included(d, r, 250) {
		t.Error("percentage >100 behaves as 100")
	}
}

// The cohort share should track the percentage closely across many devices.
func TestDistribution(t *testing.T) {
	const n = 10000
	rolloutID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	for _, pct := range []int{1, 5, 25, 50, 75} {
		included := 0
		for i := 0; i < n; i++ {
			if Included(randomUUIDString(t), rolloutID, pct) {
				included++
			}
		}
		share := 100 * float64(included) / n
		if diff := share - float64(pct); diff < -3 || diff > 3 {
			t.Errorf("pct=%d: observed %.2f%%, drift > 3 points", pct, share)
		}
	}
}

// randomUUIDString builds a canonical v4-shaped UUID from crypto/rand
// (avoids importing uuid into this package just for tests).
func randomUUIDString(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		binary.BigEndian.Uint32(b[0:4]),
		binary.BigEndian.Uint16(b[4:6]),
		binary.BigEndian.Uint16(b[6:8]),
		binary.BigEndian.Uint16(b[8:10]),
		b[10:16])
}
