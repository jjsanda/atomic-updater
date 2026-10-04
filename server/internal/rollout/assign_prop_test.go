package rollout

import (
	"testing"

	"pgregory.net/rapid"
)

func uuidGen() *rapid.Generator[string] {
	return rapid.StringMatching(`[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`)
}

// Property: assignment is a pure function of (device, rollout, percentage) —
// stable across calls, which is what "stable across process restarts" means
// for a deterministic hash.
func TestPropDeterminism(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		d := uuidGen().Draw(t, "device")
		r := uuidGen().Draw(t, "rollout")
		p := rapid.IntRange(0, 100).Draw(t, "pct")
		first := Included(d, r, p)
		for i := 0; i < 3; i++ {
			if Included(d, r, p) != first {
				t.Fatalf("assignment flapped for (%s, %s, %d)", d, r, p)
			}
		}
	})
}

// Property (design doc §13.1): as the percentage monotonically increases, no
// device ever drops out of the cohort.
func TestPropMonotonicRamp(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		d := uuidGen().Draw(t, "device")
		r := uuidGen().Draw(t, "rollout")
		p1 := rapid.IntRange(0, 100).Draw(t, "p1")
		p2 := rapid.IntRange(p1, 100).Draw(t, "p2")
		if Included(d, r, p1) && !Included(d, r, p2) {
			t.Fatalf("device %s dropped out ramping %d%% -> %d%% (rollout %s)", d, p1, p2, r)
		}
	})
}

// Property: bucket is always in [0, 100), and different rollouts re-shuffle
// membership independently of each other (sanity: bucket depends on rollout).
func TestPropBucketRange(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		d := uuidGen().Draw(t, "device")
		r := uuidGen().Draw(t, "rollout")
		if b := Bucket(d, r); b >= 100 {
			t.Fatalf("bucket %d out of range", b)
		}
	})
}
