package storage

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"atomic-updater/server/internal/model"
)

const bundleColumns = `id, version, hardware_type, object_key, sha256, size_bytes,
	signed_by, status, uploaded_at`

func scanBundle(row pgx.Row) (*model.Bundle, error) {
	var b model.Bundle
	err := row.Scan(&b.ID, &b.Version, &b.HardwareType, &b.ObjectKey, &b.SHA256,
		&b.SizeBytes, &b.SignedBy, &b.Status, &b.UploadedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (db *DB) GetBundle(ctx context.Context, id string) (*model.Bundle, error) {
	return scanBundle(db.Pool.QueryRow(ctx,
		`SELECT `+bundleColumns+` FROM bundles WHERE id = $1`, id))
}

func (db *DB) GetBundleByKey(ctx context.Context, key string) (*model.Bundle, error) {
	return scanBundle(db.Pool.QueryRow(ctx,
		`SELECT `+bundleColumns+` FROM bundles WHERE object_key = $1`, key))
}

func (db *DB) ListBundles(ctx context.Context) ([]model.Bundle, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT `+bundleColumns+` FROM bundles ORDER BY uploaded_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Bundle
	for rows.Next() {
		b, err := scanBundle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// ErrDuplicateBundle signals a version+hardware_type or object_key collision.
var ErrDuplicateBundle = errors.New("bundle already registered")

func (db *DB) InsertBundle(ctx context.Context, b *model.Bundle) error {
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO bundles (id, version, hardware_type, object_key, sha256, size_bytes, signed_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		b.ID, b.Version, b.HardwareType, b.ObjectKey, b.SHA256, b.SizeBytes, b.SignedBy)
	if err != nil && isUniqueViolation(err) {
		return ErrDuplicateBundle
	}
	return err
}

func (db *DB) UpdateBundleStatus(ctx context.Context, id, status string) (*model.Bundle, error) {
	return scanBundle(db.Pool.QueryRow(ctx,
		`UPDATE bundles SET status = $2 WHERE id = $1 RETURNING `+bundleColumns, id, status))
}

func (db *DB) BundleExistsByKey(ctx context.Context, key string) (bool, error) {
	var exists bool
	err := db.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM bundles WHERE object_key = $1)`, key).Scan(&exists)
	return exists, err
}
