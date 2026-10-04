import { getSummary } from '../api';
import { usePoll } from '../hooks';
import { CHANNELS } from '../types';
import StatTile from '../components/StatTile';
import BarList from '../components/BarList';
import TimeSeriesChart from '../components/TimeSeriesChart';

const REFRESH_MS = 10_000;

export default function Fleet() {
  const { data, error, loading, refreshing } = usePoll(getSummary, REFRESH_MS);

  return (
    <div>
      <h1 className="page-title">Fleet</h1>
      <p className="page-sub">Live overview · refreshes every 10 s</p>

      {error && <div className="error-banner">{error}</div>}
      {loading && !data && <div className="empty">Loading…</div>}

      {data && (
        <div className={refreshing ? 'refreshing' : ''}>
          <div className="tiles">
            <StatTile hero label="Devices total" value={data.devices_total} />
            {CHANNELS.map((ch) => (
              <StatTile
                key={ch}
                label={`Channel ${ch}`}
                value={data.by_channel[ch] ?? 0}
                hint="devices"
              />
            ))}
          </div>

          <div className="card">
            <h2>Version distribution</h2>
            <BarList items={data.by_version} emptyText="No versions reported yet." />
          </div>

          <div className="card">
            <h2>Mark-bad rate — install-fail + rollback events per hour, trailing 24 h</h2>
            <TimeSeriesChart buckets={data.markbad_rate_1h} seriesLabel="mark-bad events" />
          </div>
        </div>
      )}
    </div>
  );
}
