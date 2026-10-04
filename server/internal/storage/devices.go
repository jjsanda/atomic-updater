package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"atomic-updater/server/internal/model"
)

const deviceColumns = `id, cert_fingerprint, pubkey_fingerprint, hardware_type, serial,
	channel, status, current_bundle_id, current_version, current_slot, boot_count,
	last_update_outcome, healthy, last_seen_at, created_at`

func scanDevice(row pgx.Row) (*model.Device, error) {
	var d model.Device
	err := row.Scan(&d.ID, &d.CertFingerprint, &d.PubkeyFingerprint, &d.HardwareType,
		&d.Serial, &d.Channel, &d.Status, &d.CurrentBundleID, &d.CurrentVersion,
		&d.CurrentSlot, &d.BootCount, &d.LastUpdateOutcome, &d.Healthy,
		&d.LastSeenAt, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (db *DB) GetDevice(ctx context.Context, id string) (*model.Device, error) {
	return scanDevice(db.Pool.QueryRow(ctx,
		`SELECT `+deviceColumns+` FROM devices WHERE id = $1`, id))
}

func (db *DB) GetDeviceByFingerprint(ctx context.Context, fp string) (*model.Device, error) {
	return scanDevice(db.Pool.QueryRow(ctx,
		`SELECT `+deviceColumns+` FROM devices WHERE cert_fingerprint = $1`, fp))
}

func (db *DB) GetDeviceByPubkey(ctx context.Context, fp string) (*model.Device, error) {
	return scanDevice(db.Pool.QueryRow(ctx,
		`SELECT `+deviceColumns+` FROM devices WHERE pubkey_fingerprint = $1`, fp))
}

func (db *DB) CreateDevice(ctx context.Context, d *model.Device) error {
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO devices (id, cert_fingerprint, pubkey_fingerprint, hardware_type, serial, channel)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		d.ID, d.CertFingerprint, d.PubkeyFingerprint, d.HardwareType, d.Serial, d.Channel)
	return err
}

// UpdateDeviceCert swaps the leaf fingerprint after re-enrollment.
func (db *DB) UpdateDeviceCert(ctx context.Context, id, certFingerprint string) error {
	_, err := db.Pool.Exec(ctx,
		`UPDATE devices SET cert_fingerprint = $2 WHERE id = $1`, id, certFingerprint)
	return err
}

// UpdateDeviceCheckin persists the state a device reported. The bundle id is
// cross-checked against the bundles table so an unknown id degrades to NULL
// instead of failing the FK.
func (db *DB) UpdateDeviceCheckin(ctx context.Context, id, slot, version string,
	bundleID *string, bootCount int64, healthy bool, outcome string) error {
	_, err := db.Pool.Exec(ctx,
		`UPDATE devices SET
			current_slot = $2,
			current_version = $3,
			current_bundle_id = (SELECT b.id FROM bundles b WHERE b.id = $4::uuid),
			boot_count = $5,
			healthy = $6,
			last_update_outcome = $7,
			last_seen_at = now()
		 WHERE id = $1`,
		id, slot, version, bundleID, bootCount, healthy, outcome)
	return err
}

func (db *DB) SetDeviceOutcome(ctx context.Context, id, outcome string) error {
	_, err := db.Pool.Exec(ctx,
		`UPDATE devices SET last_update_outcome = $2 WHERE id = $1`, id, outcome)
	return err
}

type DeviceFilter struct {
	Channel      string
	HardwareType string
	Query        string // matches serial or id prefix
	Limit        int
	Offset       int
}

func (db *DB) ListDevices(ctx context.Context, f DeviceFilter) ([]model.Device, int64, error) {
	var where []string
	var args []any
	add := func(cond string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.Channel != "" {
		add("channel = $%d", f.Channel)
	}
	if f.HardwareType != "" {
		add("hardware_type = $%d", f.HardwareType)
	}
	if f.Query != "" {
		args = append(args, f.Query)
		where = append(where, fmt.Sprintf(
			"(serial ILIKE '%%' || $%d || '%%' OR id::text ILIKE $%d || '%%')",
			len(args), len(args)))
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int64
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM devices`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	args = append(args, limit, f.Offset)
	rows, err := db.Pool.Query(ctx,
		`SELECT `+deviceColumns+` FROM devices`+clause+
			fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args)),
		args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []model.Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *d)
	}
	return out, total, rows.Err()
}

// UpdateDeviceAdmin applies operator changes (channel and/or status).
func (db *DB) UpdateDeviceAdmin(ctx context.Context, id string, channel, status *string) (*model.Device, error) {
	return scanDevice(db.Pool.QueryRow(ctx,
		`UPDATE devices SET
			channel = COALESCE($2, channel),
			status  = COALESCE($3, status)
		 WHERE id = $1
		 RETURNING `+deviceColumns, id, channel, status))
}

func (db *DB) Summary(ctx context.Context) (*model.Summary, error) {
	s := &model.Summary{
		ByChannel: map[string]int64{},
		ByVersion: map[string]int64{},
	}
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM devices`).Scan(&s.DevicesTotal); err != nil {
		return nil, err
	}
	rows, err := db.Pool.Query(ctx, `SELECT channel, COUNT(*) FROM devices GROUP BY channel`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ch string
		var n int64
		if err := rows.Scan(&ch, &n); err != nil {
			rows.Close()
			return nil, err
		}
		s.ByChannel[ch] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = db.Pool.Query(ctx,
		`SELECT current_version, COUNT(*) FROM devices WHERE current_version <> '' GROUP BY current_version`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v string
		var n int64
		if err := rows.Scan(&v, &n); err != nil {
			rows.Close()
			return nil, err
		}
		s.ByVersion[v] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Trailing 24h of install-fail/rollback events, hourly buckets, gaps
	// filled with zeroes so charts are continuous.
	buckets := map[time.Time]int64{}
	rows, err = db.Pool.Query(ctx,
		`SELECT date_trunc('hour', created_at) AS b, COUNT(*)
		 FROM device_events
		 WHERE event_type IN ('install-fail', 'rollback')
		   AND created_at > now() - interval '24 hours'
		 GROUP BY b`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var b time.Time
		var n int64
		if err := rows.Scan(&b, &n); err != nil {
			rows.Close()
			return nil, err
		}
		buckets[b.UTC()] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	start := time.Now().UTC().Truncate(time.Hour).Add(-23 * time.Hour)
	for i := 0; i < 24; i++ {
		b := start.Add(time.Duration(i) * time.Hour)
		s.MarkBad1h = append(s.MarkBad1h, model.MarkBadBucket{Bucket: b, Count: buckets[b]})
	}
	return s, nil
}
