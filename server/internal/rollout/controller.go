package rollout

import (
	"context"
	"fmt"
	"time"

	"atomic-updater/server/internal/model"
)

// Store is the slice of persistence the controller needs. *storage.DB
// implements it; API tests use a fake.
type Store interface {
	// NewestActiveRollout returns the most recently activated 'active'
	// rollout matching channel and hardware type, or nil.
	NewestActiveRollout(ctx context.Context, channel, hardwareType string) (*model.Rollout, error)
	GetBundle(ctx context.Context, id string) (*model.Bundle, error)
	// LastFailedBundleID returns the bundle referenced by the device's most
	// recent install-fail/rollback event ("" if none).
	LastFailedBundleID(ctx context.Context, deviceID string) (string, error)
	// OfferLoggedWithin reports whether an offer event for (device, rollout)
	// exists in the trailing window (offer dedup, docs/api.md §1.2).
	OfferLoggedWithin(ctx context.Context, deviceID, rolloutID string, window time.Duration) (bool, error)
	LogOffer(ctx context.Context, deviceID, rolloutID, bundleID string) error
}

// Presigner mints short-lived download URLs (storage.S3 implements it).
type Presigner interface {
	PresignGet(ctx context.Context, objectKey string, ttl time.Duration) (string, error)
}

type Controller struct {
	Store     Store
	Presigner Presigner
	TTL       time.Duration // presigned URL lifetime (15m per design doc)
}

// Offer is the update assignment returned on check-in.
type Offer struct {
	RolloutID      string
	BundleID       string
	Version        string
	URL            string
	SHA256         string
	SizeBytes      int64
	AllowDowngrade bool
}

// Evaluate implements the offer algorithm of docs/api.md §1.2. A nil Offer
// with nil error means "no update".
func (c *Controller) Evaluate(ctx context.Context, dev *model.Device) (*Offer, error) {
	r, err := c.Store.NewestActiveRollout(ctx, dev.Channel, dev.HardwareType)
	if err != nil {
		return nil, fmt.Errorf("rollout lookup: %w", err)
	}
	if r == nil {
		return nil, nil
	}
	if !Included(dev.ID, r.ID, r.Percentage) {
		return nil, nil
	}
	b, err := c.Store.GetBundle(ctx, r.BundleID)
	if err != nil {
		return nil, fmt.Errorf("bundle lookup: %w", err)
	}
	if b == nil || b.Status != "active" {
		return nil, nil
	}
	if dev.CurrentBundleID != nil && *dev.CurrentBundleID == b.ID {
		return nil, nil
	}
	if dev.CurrentVersion != "" && !r.AllowDowngrade &&
		CompareVersions(b.Version, dev.CurrentVersion) <= 0 {
		return nil, nil
	}
	// §10.4: a device is never re-offered a bundle its last update failed on.
	lastFail, err := c.Store.LastFailedBundleID(ctx, dev.ID)
	if err != nil {
		return nil, fmt.Errorf("failure history lookup: %w", err)
	}
	if lastFail == b.ID {
		return nil, nil
	}
	url, err := c.Presigner.PresignGet(ctx, b.ObjectKey, c.TTL)
	if err != nil {
		return nil, fmt.Errorf("presign %s: %w", b.ObjectKey, err)
	}
	logged, err := c.Store.OfferLoggedWithin(ctx, dev.ID, r.ID, time.Hour)
	if err != nil {
		return nil, fmt.Errorf("offer dedup lookup: %w", err)
	}
	if !logged {
		if err := c.Store.LogOffer(ctx, dev.ID, r.ID, b.ID); err != nil {
			return nil, fmt.Errorf("log offer: %w", err)
		}
	}
	return &Offer{
		RolloutID:      r.ID,
		BundleID:       b.ID,
		Version:        b.Version,
		URL:            url,
		SHA256:         b.SHA256,
		SizeBytes:      b.SizeBytes,
		AllowDowngrade: r.AllowDowngrade,
	}, nil
}
