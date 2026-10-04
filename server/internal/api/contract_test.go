package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The agent module carries byte-identical copies of these fixtures
// (device/agent/internal/client/testdata) pinned by its own contract test.
// Round-tripping through the wire structs must preserve every field — a
// struct/tag drift from docs/api.md fails here, not in the field.
func roundtrip[T any](t *testing.T, fixture string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	var typed T
	if err := json.Unmarshal(raw, &typed); err != nil {
		t.Fatalf("%s does not unmarshal into %T: %v", fixture, typed, err)
	}
	remarshaled, err := json.Marshal(typed)
	if err != nil {
		t.Fatal(err)
	}
	var want, got map[string]any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(remarshaled, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("%s: wire type drops or renames fields\n fixture: %v\n roundtrip: %v",
			fixture, want, got)
	}
}

func TestContractCheckinRequest(t *testing.T) {
	roundtrip[CheckinRequest](t, "checkin_request.json")
}

func TestContractCheckinResponseUpdate(t *testing.T) {
	roundtrip[CheckinResponse](t, "checkin_response_update.json")
}

func TestContractEventsRequest(t *testing.T) {
	roundtrip[EventsRequest](t, "events_request.json")
}

func TestContractEnrollResponse(t *testing.T) {
	roundtrip[EnrollResponse](t, "enroll_response.json")
}
