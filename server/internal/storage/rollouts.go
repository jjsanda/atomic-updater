package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"atomic-updater/server/internal/model"
)

const rolloutColumns = `r.id, r.bundle_id, r.channel, r.percentage, r.allow_downgrade,
	r.status, r.paused_reason, r.created_at, r.activated_at, b.version, b.hardware_type`

func scanRollout(row pgx.Row) (*model.Rollout, error) {
	var r model.Rollout
	err := row.Scan(&r.ID, &r.BundleID, &r.Channel, &r.Percentage, &r.AllowDowngrade,
		&r.Status, &r.PausedReason, &r.CreatedAt, &r.ActivatedAt,
		&r.BundleVersion, &r.HardwareType)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (db *DB) GetRollout(ctx context.Context, id string) (*model.Rollout, error) {
	return scanRollout(db.Pool.QueryRow(ctx,
		`SELECT `+rolloutColumns+` FROM rollouts r JOIN bundles b ON b.id = r.bundle_id
		 WHERE r.id = $1`, id))
}

func (db *DB) ListRollouts(ctx context.Context) ([]model.Rollout, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT `+rolloutColumns+` FROM rollouts r JOIN bundles b ON b.id = r.bundle_id
		 ORDER BY r.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Rollout
	for rows.Next() {
		r, err := scanRollout(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (db *DB) CreateRollout(ctx context.Context, r *model.Rollout) (*model.Rollout, error) {
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO rollouts (id, bundle_id, channel, percentage, allow_downgrade)
		 VALUES ($1, $2, $3, $4, $5)`,
		r.ID, r.BundleID, r.Channel, r.Percentage, r.AllowDowngrade)
	if err != nil {
		return nil, err
	}
	return db.GetRollout(ctx, r.ID)
}

func (db *DB) UpdateRolloutPercentage(ctx context.Context, id string, pct int) (*model.Rollout, error) {
	tag, err := db.Pool.Exec(ctx,
		`UPDATE rollouts SET percentage = $2
		 WHERE id = $1 AND status IN ('draft', 'active', 'paused')`, id, pct)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, nil
	}
	return db.GetRollout(ctx, id)
}

// TransitionRollout atomically moves a rollout between states. Returns nil
// (no error) when the current status is not in allowedFrom — the caller maps
// that to 409.
func (db *DB) TransitionRollout(ctx context.Context, id, to string, allowedFrom []string) (*model.Rollout, error) {
	extra := ""
	switch to {
	case "active":
		// Covers both first activation and operator resume of a pause.
		extra = `, activated_at = COALESCE(activated_at, now()), paused_reason = NULL`
	case "paused":
		extra = `, paused_reason = COALESCE(paused_reason, 'operator pause')`
	}
	tag, err := db.Pool.Exec(ctx,
		fmt.Sprintf(`UPDATE rollouts SET status = $2%s WHERE id = $1 AND status = ANY($3)`, extra),
		id, to, allowedFrom)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, nil
	}
	return db.GetRollout(ctx, id)
}

func (db *DB) NewestActiveRollout(ctx context.Context, channel, hardwareType string) (*model.Rollout, error) {
	return scanRollout(db.Pool.QueryRow(ctx,
		`SELECT `+rolloutColumns+` FROM rollouts r JOIN bundles b ON b.id = r.bundle_id
		 WHERE r.status = 'active' AND r.channel = $1 AND b.hardware_type = $2
		 ORDER BY r.activated_at DESC NULLS LAST LIMIT 1`, channel, hardwareType))
}

func (db *DB) RolloutStats(ctx context.Context, id string) (*model.RolloutStats, error) {
	var s model.RolloutStats
	err := db.Pool.QueryRow(ctx, `
		SELECT
		  (SELECT COUNT(DISTINCT device_id) FROM device_events
		    WHERE event_type = 'offer' AND payload->>'rollout_id' = $1),
		  (SELECT COUNT(*) FROM device_events
		    WHERE event_type = 'install-ok' AND payload->>'rollout_id' = $1),
		  (SELECT COUNT(*) FROM device_events
		    WHERE event_type IN ('install-fail', 'rollback') AND payload->>'rollout_id' = $1),
		  (SELECT COALESCE(
		     100.0 * (SELECT COUNT(*) FROM device_events
		               WHERE event_type IN ('install-fail', 'rollback')
		                 AND payload->>'rollout_id' = $1
		                 AND created_at > now() - interval '1 hour')
		     / NULLIF((SELECT COUNT(DISTINCT device_id) FROM device_events
		               WHERE event_type = 'offer' AND payload->>'rollout_id' = $1
		                 AND created_at > now() - interval '1 hour'), 0), 0))`,
		id).Scan(&s.Offered, &s.InstalledOK, &s.Failed, &s.FailRate1h)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// EvaluateAutoPause implements docs/api.md §3 in a single atomic statement.
// Returns true when this call flipped the rollout to paused.
func (db *DB) EvaluateAutoPause(ctx context.Context, rolloutID string, minCohort, failPct int) (bool, error) {
	tag, err := db.Pool.Exec(ctx, `
		WITH stats AS (
		  SELECT
		    (SELECT COUNT(DISTINCT device_id) FROM device_events
		      WHERE event_type = 'offer' AND payload->>'rollout_id' = $1
		        AND created_at > now() - interval '1 hour') AS offered,
		    (SELECT COUNT(*) FROM device_events
		      WHERE event_type IN ('install-fail', 'rollback')
		        AND payload->>'rollout_id' = $1
		        AND created_at > now() - interval '1 hour') AS fails
		)
		UPDATE rollouts r
		SET status = 'paused',
		    paused_reason = 'auto: ' || s.fails || '/' || s.offered || ' failures in last hour'
		FROM stats s
		WHERE r.id = $2::uuid AND r.status = 'active'
		  AND s.offered >= $3 AND s.fails * 100 > s.offered * $4`,
		rolloutID, rolloutID, minCohort, failPct)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
