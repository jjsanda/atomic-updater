import { FormEvent, useState } from 'react';
import { listBundles, patchBundle, registerBundle, scanBundles } from '../api';
import { usePoll } from '../hooks';
import { absoluteTime, humanBytes, shortHash } from '../format';
import Badge from '../components/Badge';

const REFRESH_MS = 15_000;

export default function Bundles() {
  const { data, error, loading, refreshing, refresh } = usePoll(listBundles, REFRESH_MS);
  const [objectKey, setObjectKey] = useState('');
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const act = async (fn: () => Promise<string | null>) => {
    setBusy(true);
    setActionError(null);
    setNotice(null);
    try {
      const msg = await fn();
      if (msg) setNotice(msg);
      refresh();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const onScan = () => {
    void act(async () => {
      const res = await scanBundles();
      return `Store scan complete — ${res.registered} new bundle${res.registered === 1 ? '' : 's'} registered.`;
    });
  };

  const onRegister = (e: FormEvent) => {
    e.preventDefault();
    const key = objectKey.trim();
    if (!key) return;
    void act(async () => {
      const b = await registerBundle(key);
      setObjectKey('');
      return `Registered ${b.version} (${b.hardware_type}).`;
    });
  };

  const onToggleStatus = (id: string, current: string) => {
    const next = current === 'active' ? 'deprecated' : 'active';
    void act(async () => {
      await patchBundle(id, next);
      return null;
    });
  };

  return (
    <div>
      <h1 className="page-title">Bundles</h1>
      <p className="page-sub">Signed RAUC bundles registered from the object store</p>

      {error && <div className="error-banner">{error}</div>}
      {actionError && <div className="error-banner">{actionError}</div>}
      {notice && <div className="notice-banner">{notice}</div>}

      <div className="card">
        <h2>Register</h2>
        <form className="row" onSubmit={onRegister}>
          <label className="field grow">
            Object key (signed/…)
            <input
              type="text"
              placeholder="signed/acme-device-v1-rpi-cm4-1.1.0.raucb"
              value={objectKey}
              onChange={(e) => setObjectKey(e.target.value)}
            />
          </label>
          <button className="primary" type="submit" disabled={busy || objectKey.trim() === ''}>
            Register by key
          </button>
          <button type="button" disabled={busy} onClick={onScan}>
            Scan store
          </button>
        </form>
      </div>

      {loading && !data && <div className="empty">Loading…</div>}

      {data && (
        <div className={`card ${refreshing ? 'refreshing' : ''}`}>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Version</th>
                  <th>Hardware</th>
                  <th>SHA-256</th>
                  <th className="num">Size</th>
                  <th>Status</th>
                  <th>Uploaded</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {data.bundles.length === 0 && (
                  <tr>
                    <td colSpan={7} className="empty">
                      No bundles registered. Run a store scan or register one by key.
                    </td>
                  </tr>
                )}
                {data.bundles.map((b) => (
                  <tr key={b.id}>
                    <td className="mono">{b.version}</td>
                    <td>{b.hardware_type}</td>
                    <td className="mono" title={b.sha256}>
                      {shortHash(b.sha256)}…
                    </td>
                    <td className="num">{humanBytes(b.size_bytes)}</td>
                    <td>
                      <Badge value={b.status} />
                    </td>
                    <td>{absoluteTime(b.uploaded_at)}</td>
                    <td>
                      {b.status === 'active' ? (
                        <button
                          className="danger"
                          disabled={busy}
                          onClick={() => onToggleStatus(b.id, b.status)}
                        >
                          Deprecate
                        </button>
                      ) : (
                        <button disabled={busy} onClick={() => onToggleStatus(b.id, b.status)}>
                          Reactivate
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}
