import { FormEvent, useState } from 'react';
import { createRollout, getRolloutStats, listBundles, listRollouts, patchRollout } from '../api';
import { usePoll } from '../hooks';
import { CHANNELS, Channel, Rollout, RolloutStats } from '../types';
import { absoluteTime, shortId } from '../format';
import Badge from '../components/Badge';

const REFRESH_MS = 10_000;

interface RolloutRow {
  rollout: Rollout;
  stats: RolloutStats | null;
}

async function fetchRolloutsWithStats(): Promise<RolloutRow[]> {
  const { rollouts } = await listRollouts();
  const stats = await Promise.all(
    rollouts.map((r) => getRolloutStats(r.id).catch(() => null)),
  );
  return rollouts.map((rollout, i) => ({ rollout, stats: stats[i] }));
}

export default function Rollouts() {
  const rollouts = usePoll(fetchRolloutsWithStats, REFRESH_MS);
  const bundles = usePoll(listBundles, 30_000);

  const [bundleId, setBundleId] = useState('');
  const [channel, setChannel] = useState<Channel>('dev');
  const [percentage, setPercentage] = useState(1);
  const [allowDowngrade, setAllowDowngrade] = useState(false);
  const [ramp, setRamp] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const activeBundles = (bundles.data?.bundles ?? []).filter((b) => b.status === 'active');

  const act = async (fn: () => Promise<string | null>) => {
    setBusy(true);
    setActionError(null);
    setNotice(null);
    try {
      const msg = await fn();
      if (msg) setNotice(msg);
      rollouts.refresh();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const onCreate = (e: FormEvent) => {
    e.preventDefault();
    if (!bundleId) return;
    void act(async () => {
      const r = await createRollout({
        bundle_id: bundleId,
        channel,
        percentage,
        allow_downgrade: allowDowngrade,
      });
      setBundleId('');
      setPercentage(1);
      setAllowDowngrade(false);
      return `Draft rollout created for ${r.bundle_version} → ${r.channel} at ${r.percentage}%.`;
    });
  };

  const setStatus = (r: Rollout, status: string, confirmText?: string) => {
    if (confirmText && !window.confirm(confirmText)) return;
    void act(async () => {
      await patchRollout(r.id, { status });
      return null;
    });
  };

  const onRamp = (r: Rollout) => {
    const raw = ramp[r.id] ?? String(r.percentage);
    const pct = Number(raw);
    if (!Number.isInteger(pct) || pct < 0 || pct > 100) {
      setActionError('Percentage must be an integer between 0 and 100.');
      return;
    }
    void act(async () => {
      await patchRollout(r.id, { percentage: pct });
      return `Rollout ${shortId(r.id)} ramped to ${pct}%.`;
    });
  };

  return (
    <div>
      <h1 className="page-title">Rollouts</h1>
      <p className="page-sub">Phased rollouts per channel · refreshes every 10 s</p>

      {actionError && <div className="error-banner">{actionError}</div>}
      {notice && <div className="notice-banner">{notice}</div>}
      {rollouts.error && <div className="error-banner">{rollouts.error}</div>}

      <div className="card">
        <h2>Create rollout</h2>
        {bundles.error && <div className="error-banner">{bundles.error}</div>}
        <form className="row" onSubmit={onCreate}>
          <label className="field grow">
            Bundle
            <select value={bundleId} onChange={(e) => setBundleId(e.target.value)} required>
              <option value="">— select a bundle —</option>
              {activeBundles.map((b) => (
                <option key={b.id} value={b.id}>
                  {b.version} · {b.hardware_type}
                </option>
              ))}
            </select>
          </label>
          <label className="field">
            Channel
            <select value={channel} onChange={(e) => setChannel(e.target.value as Channel)}>
              {CHANNELS.map((ch) => (
                <option key={ch} value={ch}>
                  {ch}
                </option>
              ))}
            </select>
          </label>
          <label className="field">
            Percentage
            <input
              type="number"
              min={0}
              max={100}
              value={percentage}
              onChange={(e) => setPercentage(Number(e.target.value))}
              className="inline-input"
            />
          </label>
          <label className="check">
            <input
              type="checkbox"
              checked={allowDowngrade}
              onChange={(e) => setAllowDowngrade(e.target.checked)}
            />
            allow downgrade
          </label>
          <button className="primary" type="submit" disabled={busy || bundleId === ''}>
            Create draft
          </button>
        </form>
      </div>

      {rollouts.loading && !rollouts.data && <div className="empty">Loading…</div>}

      {rollouts.data && (
        <div className={`card ${rollouts.refreshing ? 'refreshing' : ''}`}>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>ID</th>
                  <th>Bundle</th>
                  <th>Hardware</th>
                  <th>Channel</th>
                  <th className="num">%</th>
                  <th>Status</th>
                  <th className="num">Offered</th>
                  <th className="num">OK</th>
                  <th className="num">Failed</th>
                  <th className="num">Fail 1h</th>
                  <th>Activated</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {rollouts.data.length === 0 && (
                  <tr>
                    <td colSpan={12} className="empty">
                      No rollouts yet — create a draft above.
                    </td>
                  </tr>
                )}
                {rollouts.data.map(({ rollout: r, stats }) => (
                  <RolloutRows
                    key={r.id}
                    r={r}
                    stats={stats}
                    busy={busy}
                    rampValue={ramp[r.id] ?? String(r.percentage)}
                    onRampChange={(v) => setRamp((m) => ({ ...m, [r.id]: v }))}
                    onRamp={() => onRamp(r)}
                    setStatus={setStatus}
                  />
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}

interface RowProps {
  r: Rollout;
  stats: RolloutStats | null;
  busy: boolean;
  rampValue: string;
  onRampChange: (v: string) => void;
  onRamp: () => void;
  setStatus: (r: Rollout, status: string, confirmText?: string) => void;
}

function RolloutRows({ r, stats, busy, rampValue, onRampChange, onRamp, setStatus }: RowProps) {
  const failRate = stats ? stats.fail_rate_1h : null;
  const pausedReason = r.paused_reason ?? stats?.paused_reason ?? null;

  return (
    <>
      <tr>
        <td className="mono" title={r.id}>
          {shortId(r.id)}
        </td>
        <td className="mono">{r.bundle_version}</td>
        <td>{r.hardware_type}</td>
        <td>
          <Badge value={r.channel} />
        </td>
        <td className="num">{r.percentage}%</td>
        <td>
          <Badge value={r.status} />
          {r.allow_downgrade && (
            <>
              {' '}
              <span className="badge warn" title="This rollout may downgrade devices">
                downgrade
              </span>
            </>
          )}
        </td>
        <td className="num">{stats ? stats.offered.toLocaleString() : '—'}</td>
        <td className="num">{stats ? stats.installed_ok.toLocaleString() : '—'}</td>
        <td className="num">{stats ? stats.failed.toLocaleString() : '—'}</td>
        <td className="num">
          {failRate === null ? (
            '—'
          ) : (
            <span className={failRate >= 2 ? 'stale' : ''}>{failRate.toFixed(1)}%</span>
          )}
        </td>
        <td>{r.activated_at ? absoluteTime(r.activated_at) : '—'}</td>
        <td>
          <div className="actions">
            {r.status === 'draft' && (
              <button className="primary" disabled={busy} onClick={() => setStatus(r, 'active')}>
                Activate
              </button>
            )}
            {r.status === 'active' && (
              <>
                <input
                  className="inline-input"
                  type="number"
                  min={0}
                  max={100}
                  value={rampValue}
                  onChange={(e) => onRampChange(e.target.value)}
                  aria-label="Ramp percentage"
                />
                <button disabled={busy} onClick={onRamp}>
                  Ramp
                </button>
                <button disabled={busy} onClick={() => setStatus(r, 'paused')}>
                  Pause
                </button>
                <button
                  disabled={busy}
                  onClick={() =>
                    setStatus(r, 'completed', 'Mark this rollout completed? This is final.')
                  }
                >
                  Complete
                </button>
                <button
                  className="danger"
                  disabled={busy}
                  onClick={() => setStatus(r, 'aborted', 'Abort this rollout? This is final.')}
                >
                  Abort
                </button>
              </>
            )}
            {r.status === 'paused' && (
              <>
                <button className="primary" disabled={busy} onClick={() => setStatus(r, 'active')}>
                  Resume
                </button>
                <button
                  className="danger"
                  disabled={busy}
                  onClick={() => setStatus(r, 'aborted', 'Abort this rollout? This is final.')}
                >
                  Abort
                </button>
              </>
            )}
          </div>
        </td>
      </tr>
      {r.status === 'paused' && pausedReason && (
        <tr className="detail-row">
          <td colSpan={12}>⚠ Paused: {pausedReason}</td>
        </tr>
      )}
    </>
  );
}
