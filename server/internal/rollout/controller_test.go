package rollout

import (
	"context"
	"testing"
	"time"

	"atomic-updater/server/internal/model"
)

// fakeStore lets each test pin the exact rollout/bundle/history state.
type fakeStore struct {
	rollout      *model.Rollout
	bundle       *model.Bundle
	lastFailed   string
	offerLogged  bool
	loggedOffers []string
}

func (f *fakeStore) NewestActiveRollout(ctx context.Context, ch, hw string) (*model.Rollout, error) {
	if f.rollout != nil && f.rollout.Channel == ch && f.rollout.HardwareType == hw {
		return f.rollout, nil
	}
	return nil, nil
}
func (f *fakeStore) GetBundle(ctx context.Context, id string) (*model.Bundle, error) {
	if f.bundle != nil && f.bundle.ID == id {
		return f.bundle, nil
	}
	return nil, nil
}
func (f *fakeStore) LastFailedBundleID(ctx context.Context, deviceID string) (string, error) {
	return f.lastFailed, nil
}
func (f *fakeStore) OfferLoggedWithin(ctx context.Context, d, r string, w time.Duration) (bool, error) {
	return f.offerLogged, nil
}
func (f *fakeStore) LogOffer(ctx context.Context, d, r, b string) error {
	f.loggedOffers = append(f.loggedOffers, d+"/"+r)
	return nil
}

type fakePresigner struct{ calls int }

func (f *fakePresigner) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	f.calls++
	return "https://store.example/" + key + "?sig=x", nil
}

// includedDevice was chosen so Bucket(id, rolloutID) = 31 < 50.
const (
	testRolloutID  = "9e8d7c6b-5a49-4838-a7b6-c5d4e3f2a1b0"
	includedDevice = "ffffffff-ffff-ffff-ffff-ffffffffffff" // bucket 31
	excludedDevice = "123e4567-e89b-12d3-a456-426614174000" // bucket 81
)

func fixture() (*fakeStore, *Controller, *model.Device) {
	st := &fakeStore{
		rollout: &model.Rollout{
			ID: testRolloutID, BundleID: "b1", Channel: "dev",
			HardwareType: "rpi-cm4", Percentage: 50, Status: "active",
		},
		bundle: &model.Bundle{
			ID: "b1", Version: "1.1.0", HardwareType: "rpi-cm4",
			ObjectKey: "signed/x.raucb", SHA256: "aa", SizeBytes: 42, Status: "active",
		},
	}
	c := &Controller{Store: st, Presigner: &fakePresigner{}, TTL: 15 * time.Minute}
	dev := &model.Device{
		ID: includedDevice, Channel: "dev", HardwareType: "rpi-cm4",
		CurrentVersion: "1.0.0",
	}
	return st, c, dev
}

func TestEvaluateOffers(t *testing.T) {
	st, c, dev := fixture()
	offer, err := c.Evaluate(context.Background(), dev)
	if err != nil {
		t.Fatal(err)
	}
	if offer == nil {
		t.Fatal("expected an offer")
	}
	if offer.Version != "1.1.0" || offer.RolloutID != testRolloutID {
		t.Errorf("unexpected offer %+v", offer)
	}
	if len(st.loggedOffers) != 1 {
		t.Errorf("expected exactly one offer event, got %v", st.loggedOffers)
	}
}

func TestEvaluateSkips(t *testing.T) {
	cases := []struct {
		name  string
		setup func(st *fakeStore, dev *model.Device)
	}{
		{"device outside percentage", func(st *fakeStore, dev *model.Device) {
			dev.ID = excludedDevice
		}},
		{"no matching rollout channel", func(st *fakeStore, dev *model.Device) {
			dev.Channel = "stable"
		}},
		{"deprecated bundle", func(st *fakeStore, dev *model.Device) {
			st.bundle.Status = "deprecated"
		}},
		{"already on the bundle", func(st *fakeStore, dev *model.Device) {
			id := "b1"
			dev.CurrentBundleID = &id
		}},
		{"downgrade rejected", func(st *fakeStore, dev *model.Device) {
			dev.CurrentVersion = "2.0.0"
		}},
		{"same version rejected", func(st *fakeStore, dev *model.Device) {
			dev.CurrentVersion = "1.1.0"
		}},
		{"previously failed bundle never re-offered", func(st *fakeStore, dev *model.Device) {
			st.lastFailed = "b1"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, c, dev := fixture()
			tc.setup(st, dev)
			offer, err := c.Evaluate(context.Background(), dev)
			if err != nil {
				t.Fatal(err)
			}
			if offer != nil {
				t.Fatalf("expected no offer, got %+v", offer)
			}
		})
	}
}

func TestEvaluateDowngradeAllowed(t *testing.T) {
	_, c, dev := fixture()
	c.Store.(*fakeStore).rollout.AllowDowngrade = true
	dev.CurrentVersion = "2.0.0"
	offer, err := c.Evaluate(context.Background(), dev)
	if err != nil {
		t.Fatal(err)
	}
	if offer == nil || !offer.AllowDowngrade {
		t.Fatal("expected downgrade offer when rollout allows it")
	}
}

func TestEvaluateOfferDedup(t *testing.T) {
	st, c, dev := fixture()
	st.offerLogged = true // an offer event already exists within the window
	offer, err := c.Evaluate(context.Background(), dev)
	if err != nil {
		t.Fatal(err)
	}
	if offer == nil {
		t.Fatal("offer should still be returned")
	}
	if len(st.loggedOffers) != 0 {
		t.Error("offer event must not be duplicated within the window")
	}
}
