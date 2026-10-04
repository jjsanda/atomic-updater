package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"atomic-updater/server/internal/model"
)

func (db *DB) InsertEvent(ctx context.Context, deviceID, eventType string, payload json.RawMessage) error {
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO device_events (device_id, event_type, payload) VALUES ($1, $2, $3)`,
		deviceID, eventType, payload)
	return err
}

func (db *DB) ListDeviceEvents(ctx context.Context, deviceID string, limit int) ([]model.Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := db.Pool.Query(ctx,
		`SELECT id, device_id, event_type, payload, created_at
		 FROM device_events WHERE device_id = $1
		 ORDER BY id DESC LIMIT $2`, deviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Event
	for rows.Next() {
		var e model.Event
		if err := rows.Scan(&e.ID, &e.DeviceID, &e.EventType, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LastFailedBundleID returns the bundle referenced by the device's most
// recent install-fail/rollback event, or "".
func (db *DB) LastFailedBundleID(ctx context.Context, deviceID string) (string, error) {
	var bundleID string
	err := db.Pool.QueryRow(ctx,
		`SELECT COALESCE(payload->>'bundle_id', '') FROM device_events
		 WHERE device_id = $1 AND event_type IN ('install-fail', 'rollback')
		 ORDER BY id DESC LIMIT 1`, deviceID).Scan(&bundleID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return bundleID, err
}

func (db *DB) OfferLoggedWithin(ctx context.Context, deviceID, rolloutID string, window time.Duration) (bool, error) {
	var exists bool
	err := db.Pool.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM device_events
		   WHERE device_id = $1 AND event_type = 'offer'
		     AND payload->>'rollout_id' = $2
		     AND created_at > now() - $3::interval)`,
		deviceID, rolloutID, window.String()).Scan(&exists)
	return exists, err
}

func (db *DB) LogOffer(ctx context.Context, deviceID, rolloutID, bundleID string) error {
	payload, _ := json.Marshal(map[string]string{
		"rollout_id": rolloutID,
		"bundle_id":  bundleID,
	})
	return db.InsertEvent(ctx, deviceID, "offer", payload)
}
