package bundles

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"

	"atomic-updater/server/internal/model"
	"atomic-updater/server/internal/storage"
)

const goodSHA = "d0b425e00e15a0d36ffc2b0c1a1f3b3c4d5e6f708192a3b4c5d6e7f8091a2b3c"

func TestParseMeta(t *testing.T) {
	good := fmt.Sprintf(`{"version":"v1.2.0","hardware_type":"rpi-cm4","sha256":"%s","size_bytes":10,"signed_by":"x"}`, goodSHA)
	m, err := ParseMeta([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "1.2.0" {
		t.Errorf("version not normalized: %q", m.Version)
	}

	bad := []string{
		`{`,
		`{"version":"nope","hardware_type":"rpi-cm4","sha256":"` + goodSHA + `","size_bytes":10}`,
		`{"version":"1.0.0","hardware_type":"toaster","sha256":"` + goodSHA + `","size_bytes":10}`,
		`{"version":"1.0.0","hardware_type":"rpi-cm4","sha256":"zz","size_bytes":10}`,
		`{"version":"1.0.0","hardware_type":"rpi-cm4","sha256":"` + goodSHA + `","size_bytes":0}`,
	}
	for i, b := range bad {
		if _, err := ParseMeta([]byte(b)); err == nil {
			t.Errorf("bad meta %d accepted", i)
		}
	}
}

// --- poller ---

type memObjects struct {
	objects map[string][]byte
}

func (m *memObjects) ListPrefix(ctx context.Context, prefix string) ([]storage.ObjectStat, error) {
	var out []storage.ObjectStat
	for k, v := range m.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, storage.ObjectStat{Key: k, Size: int64(len(v))})
		}
	}
	return out, nil
}

func (m *memObjects) GetObjectBytes(ctx context.Context, key string, maxSize int64) ([]byte, error) {
	data, ok := m.objects[key]
	if !ok {
		return nil, storage.ErrNoObject
	}
	return data, nil
}

type memRegistry struct {
	byKey map[string]*model.Bundle
}

func (m *memRegistry) BundleExistsByKey(ctx context.Context, key string) (bool, error) {
	_, ok := m.byKey[key]
	return ok, nil
}

func (m *memRegistry) InsertBundle(ctx context.Context, b *model.Bundle) error {
	for _, ex := range m.byKey {
		if ex.Version == b.Version && ex.HardwareType == b.HardwareType {
			return storage.ErrDuplicateBundle
		}
	}
	m.byKey[b.ObjectKey] = b
	return nil
}

func TestPollerRegistersValidBundles(t *testing.T) {
	payload := []byte("bundle-bytes")
	meta := fmt.Sprintf(`{"version":"1.1.0","hardware_type":"rpi-cm4","sha256":"%s","size_bytes":%d,"signed_by":"x"}`, goodSHA, len(payload))
	objects := &memObjects{objects: map[string][]byte{
		"signed/a-1.1.0.raucb":           payload,
		"signed/a-1.1.0.raucb.meta.json": []byte(meta),
		"signed/orphan.raucb":            payload, // no sidecar → skipped
		"signed/bad.raucb":               payload,
		"signed/bad.raucb.meta.json":     []byte(`{"version":"?"}`), // invalid → skipped
		"staging/not-signed.raucb":       payload,                   // wrong prefix
	}}
	registry := &memRegistry{byKey: map[string]*model.Bundle{}}
	p := &Poller{Objects: objects, Registry: registry,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))}

	n, err := p.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 registration, got %d", n)
	}
	b := registry.byKey["signed/a-1.1.0.raucb"]
	if b == nil || b.Version != "1.1.0" || b.SHA256 != goodSHA {
		t.Fatalf("registered bundle wrong: %+v", b)
	}

	// Second pass is a no-op (idempotent).
	n, err = p.RunOnce(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("second pass: n=%d err=%v", n, err)
	}
}

func TestPollerRejectsSizeMismatch(t *testing.T) {
	meta := fmt.Sprintf(`{"version":"1.1.0","hardware_type":"rpi-cm4","sha256":"%s","size_bytes":999,"signed_by":"x"}`, goodSHA)
	objects := &memObjects{objects: map[string][]byte{
		"signed/a.raucb":           []byte("short"),
		"signed/a.raucb.meta.json": []byte(meta),
	}}
	registry := &memRegistry{byKey: map[string]*model.Bundle{}}
	p := &Poller{Objects: objects, Registry: registry,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))}
	n, err := p.RunOnce(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("size mismatch must be skipped: n=%d err=%v", n, err)
	}
}
