import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { getDevice, getDeviceEvents, patchDevice, revokeDevice } from '../api';
import { usePoll } from '../hooks';
import { CHANNELS, Channel } from '../types';
import { absoluteTime, isStale, relativeTime, shortHash } from '../format';
import Badge from '../components/Badge';

const REFRESH_MS = 10_000;

export default function DeviceDetail() {
  const { id = '' } = useParams();
  const [actionError, setActionError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const device = usePoll(() => getDevice(id), REFRESH_MS, [id]);
  const events = usePoll(() => getDeviceEvents(id), REFRESH_MS, [id]);

  const d = device.data;

  const act = async (fn: () => Promise<unknown>, doneMsg: string | null = null) => {
    setBusy(true);
    setActionError(null);
    setNotice(null);
    try {
      await fn();
      if (doneMsg) setNotice(doneMsg);
      device.refresh();
      events.refresh();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const onChannelChange = (channel: Channel) => {
    void act(() => patchDevice(id, { channel }), `Channel changed to ${channel}.`);
  };

  const onRetire = () => {
    if (!window.confirm('Retire this device? It will receive 410 Gone on its next check-in.'))
      return;
    void act(() => patchDevice(id, { status: 'retired' }), 'Device retired.');
  };

  const onReactivate = () => {
    void act(() => patchDevice(id, { status: 'active' }), 'Device reactivated.');
  };

  const onRevoke = () => {
    const reason = window.prompt('Reason for revoking this device certificate:');
    if (reason === null) return;
    if (reason.trim() === '') {
      setActionError('A revocation reason is required.');
      return;
    }
    if (
      !window.confirm(
        'Revoke this device certificate? The device will be rejected (403) on every subsequent request. This cannot be undone from the dashboard.',
      )
    )
      return;
    void act(() => revokeDevice(id, reason.trim()), 'Certificate revoked.');
  };

  return (
    <div>
      <p className="page-sub" style={{ marginBottom: 8 }}>
        <Link to="/devices">← Devices</Link>
      </p>
      <h1 className="page-title mono">{id}</h1>
      <p className="page-sub">Device detail · refreshes every 10 s</p>

      {device.error && <div className="error-banner">{device.error}</div>}
      {actionError && <div className="error-banner">{actionError}</div>}
      {notice && <div className="notice-banner">{notice}</div>}
      {device.loading && !d && <div className="empty">Loading…</div>}

      {d && (
        <div className={device.refreshing ? 'refreshing' : ''}>
          <div className="card">
            <h2>Current state</h2>
            <dl className="kv">
              <dt>Serial</dt>
              <dd>{d.serial}</dd>
              <dt>Hardware</dt>
              <dd>{d.hardware_type}</dd>
              <dt>Status</dt>
              <dd>
                <Badge value={d.status} />
              </dd>
              <dt>Channel</dt>
              <dd>
                <Badge value={d.channel} />
              </dd>
              <dt>Version</dt>
              <dd className="mono">{d.current_version || '—'}</dd>
              <dt>Bundle</dt>
              <dd className="mono">{d.current_bundle_id ? shortHash(d.current_bundle_id, 36) : '—'}</dd>
              <dt>Slot</dt>
              <dd>{d.current_slot}</dd>
              <dt>Boot count</dt>
              <dd>{d.boot_count.toLocaleString()}</dd>
              <dt>Healthy</dt>
              <dd>
                <Badge value={d.healthy ? 'ok' : 'fail'} />
              </dd>
              <dt>Last update</dt>
              <dd>
                <Badge value={d.last_update_outcome} />
              </dd>
              <dt>Last seen</dt>
              <dd className={isStale(d.last_seen_at) ? 'stale' : ''}>
                {relativeTime(d.last_seen_at)} ({absoluteTime(d.last_seen_at)})
              </dd>
              <dt>Enrolled</dt>
              <dd>{absoluteTime(d.created_at)}</dd>
            </dl>
          </div>

          <div className="card">
            <h2>Actions</h2>
            <div className="actions">
              <label className="field">
                Channel
                <select
                  value={d.channel}
                  disabled={busy}
                  onChange={(e) => onChannelChange(e.target.value as Channel)}
                >
                  {CHANNELS.map((ch) => (
                    <option key={ch} value={ch}>
                      {ch}
                    </option>
                  ))}
                </select>
              </label>
              <span className="spacer" />
              {d.status === 'active' ? (
                <button className="danger" disabled={busy} onClick={onRetire}>
                  Retire device
                </button>
              ) : (
                <button disabled={busy} onClick={onReactivate}>
                  Reactivate device
                </button>
              )}
              <button className="danger" disabled={busy} onClick={onRevoke}>
                Revoke certificate
              </button>
            </div>
          </div>
        </div>
      )}

      <div className={`card ${events.refreshing ? 'refreshing' : ''}`}>
        <h2>Event timeline</h2>
        {events.error && <div className="error-banner">{events.error}</div>}
        {events.data && events.data.events.length === 0 && (
          <div className="empty">No events recorded.</div>
        )}
        {events.data && events.data.events.length > 0 && (
          <ul className="timeline">
            {events.data.events.map((ev) => (
              <li key={ev.id}>
                <span className="when" title={ev.created_at}>
                  {absoluteTime(ev.created_at)}
                </span>
                <span className="etype">
                  <Badge value={ev.event_type} />
                </span>
                <pre>{JSON.stringify(ev.payload ?? {}, null, 2)}</pre>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
