import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { listDevices } from '../api';
import { useDebounced, usePoll } from '../hooks';
import { CHANNELS, Channel } from '../types';
import { isStale, relativeTime, shortId } from '../format';
import Badge from '../components/Badge';

const LIMIT = 50;
const REFRESH_MS = 15_000;

export default function Devices() {
  const navigate = useNavigate();
  const [channel, setChannel] = useState<Channel | ''>('');
  const [q, setQ] = useState('');
  const [offset, setOffset] = useState(0);
  const debouncedQ = useDebounced(q, 300);

  const { data, error, loading, refreshing } = usePoll(
    () => listDevices({ channel, q: debouncedQ, limit: LIMIT, offset }),
    REFRESH_MS,
    [channel, debouncedQ, offset],
  );

  const total = data?.total ?? 0;
  const from = total === 0 ? 0 : offset + 1;
  const to = Math.min(offset + (data?.devices.length ?? 0), offset + LIMIT);

  return (
    <div>
      <h1 className="page-title">Devices</h1>
      <p className="page-sub">Enrolled fleet · click a row for detail</p>

      <div className="row" style={{ marginBottom: 12 }}>
        <label className="field">
          Channel
          <select
            value={channel}
            onChange={(e) => {
              setChannel(e.target.value as Channel | '');
              setOffset(0);
            }}
          >
            <option value="">all</option>
            {CHANNELS.map((ch) => (
              <option key={ch} value={ch}>
                {ch}
              </option>
            ))}
          </select>
        </label>
        <label className="field grow">
          Search
          <input
            type="search"
            placeholder="id, serial, version…"
            value={q}
            onChange={(e) => {
              setQ(e.target.value);
              setOffset(0);
            }}
          />
        </label>
      </div>

      {error && <div className="error-banner">{error}</div>}
      {loading && !data && <div className="empty">Loading…</div>}

      {data && (
        <div className={`card ${refreshing ? 'refreshing' : ''}`}>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>ID</th>
                  <th>Serial</th>
                  <th>Hardware</th>
                  <th>Channel</th>
                  <th>Status</th>
                  <th>Version</th>
                  <th>Slot</th>
                  <th>Last update</th>
                  <th>Last seen</th>
                </tr>
              </thead>
              <tbody>
                {data.devices.length === 0 && (
                  <tr>
                    <td colSpan={9} className="empty">
                      No devices match.
                    </td>
                  </tr>
                )}
                {data.devices.map((d) => (
                  <tr
                    key={d.id}
                    className="clickable"
                    onClick={() => navigate(`/devices/${d.id}`)}
                  >
                    <td className="mono">
                      <Link to={`/devices/${d.id}`} onClick={(e) => e.stopPropagation()}>
                        {shortId(d.id)}
                      </Link>
                    </td>
                    <td>{d.serial}</td>
                    <td>{d.hardware_type}</td>
                    <td>
                      <Badge value={d.channel} />
                    </td>
                    <td>
                      <Badge value={d.status} />
                    </td>
                    <td className="mono">{d.current_version || '—'}</td>
                    <td>{d.current_slot}</td>
                    <td>
                      <Badge value={d.last_update_outcome} />
                    </td>
                    <td className={isStale(d.last_seen_at) ? 'stale' : ''}>
                      {relativeTime(d.last_seen_at)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="pager">
            <span>
              {from.toLocaleString()}–{to.toLocaleString()} of {total.toLocaleString()}
            </span>
            <button disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - LIMIT))}>
              Prev
            </button>
            <button disabled={offset + LIMIT >= total} onClick={() => setOffset(offset + LIMIT)}>
              Next
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
