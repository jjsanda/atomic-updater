package bundles

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"atomic-updater/server/internal/model"
	"atomic-updater/server/internal/storage"
)

const (
	SignedPrefix = "signed/"
	maxMetaSize  = 64 * 1024
)

type ObjectStore interface {
	ListPrefix(ctx context.Context, prefix string) ([]storage.ObjectStat, error)
	GetObjectBytes(ctx context.Context, key string, maxSize int64) ([]byte, error)
}

type Registry interface {
	BundleExistsByKey(ctx context.Context, key string) (bool, error)
	InsertBundle(ctx context.Context, b *model.Bundle) error
}

// Poller scans signed/ for *.raucb objects with valid sidecars and registers
// unknown ones ("the Go server observes new objects under signed/", design
// doc §9.2).
type Poller struct {
	Objects  ObjectStore
	Registry Registry
	Log      *slog.Logger
}

// RunOnce returns how many new bundles were registered. Individual bad
// objects are logged and skipped — one malformed upload must not wedge
// registration of the rest.
func (p *Poller) RunOnce(ctx context.Context) (int, error) {
	objs, err := p.Objects.ListPrefix(ctx, SignedPrefix)
	if err != nil {
		return 0, err
	}
	sizes := make(map[string]int64, len(objs))
	for _, o := range objs {
		sizes[o.Key] = o.Size
	}
	registered := 0
	for _, o := range objs {
		if !strings.HasSuffix(o.Key, ".raucb") {
			continue
		}
		exists, err := p.Registry.BundleExistsByKey(ctx, o.Key)
		if err != nil {
			return registered, err
		}
		if exists {
			continue
		}
		metaKey := o.Key + ".meta.json"
		if _, ok := sizes[metaKey]; !ok {
			p.Log.Debug("bundle without sidecar, skipping", "key", o.Key)
			continue
		}
		raw, err := p.Objects.GetObjectBytes(ctx, metaKey, maxMetaSize)
		if err != nil {
			if errors.Is(err, storage.ErrNoObject) {
				continue
			}
			return registered, err
		}
		meta, err := ParseMeta(raw)
		if err != nil {
			p.Log.Warn("invalid bundle sidecar, skipping", "key", metaKey, "err", err)
			continue
		}
		if meta.SizeBytes != o.Size {
			p.Log.Warn("sidecar size mismatch, skipping",
				"key", o.Key, "sidecar", meta.SizeBytes, "actual", o.Size)
			continue
		}
		b := &model.Bundle{
			ID:           uuid.NewString(),
			Version:      meta.Version,
			HardwareType: meta.HardwareType,
			ObjectKey:    o.Key,
			SHA256:       meta.SHA256,
			SizeBytes:    meta.SizeBytes,
			SignedBy:     meta.SignedBy,
		}
		if err := p.Registry.InsertBundle(ctx, b); err != nil {
			if errors.Is(err, storage.ErrDuplicateBundle) {
				p.Log.Warn("duplicate bundle version, skipping", "key", o.Key, "version", meta.Version)
				continue
			}
			return registered, err
		}
		p.Log.Info("registered bundle", "key", o.Key, "version", meta.Version, "hw", meta.HardwareType)
		registered++
	}
	return registered, nil
}

// Run polls until the context ends.
func (p *Poller) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := p.RunOnce(ctx); err != nil {
				p.Log.Error("bundle poll failed", "err", err)
			}
		}
	}
}
