// Small formatting helpers shared across pages.

export function shortId(id: string): string {
  return id.length > 8 ? id.slice(0, 8) : id;
}

export function shortHash(hash: string, n = 12): string {
  return hash.length > n ? hash.slice(0, n) : hash;
}

export function humanBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return '—';
  if (n < 1024) return `${n} B`;
  const units = ['KiB', 'MiB', 'GiB', 'TiB'];
  let value = n;
  let unit = 'B';
  for (const u of units) {
    if (value < 1024) break;
    value /= 1024;
    unit = u;
  }
  return `${value >= 100 ? value.toFixed(0) : value.toFixed(1)} ${unit}`;
}

export function relativeTime(iso: string, nowMs = Date.now()): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '—';
  const deltaS = Math.round((nowMs - t) / 1000);
  if (deltaS < 0) return 'in the future';
  if (deltaS < 10) return 'just now';
  if (deltaS < 60) return `${deltaS}s ago`;
  const m = Math.floor(deltaS / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h}h ${m % 60}m ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}

export function isStale(iso: string, thresholdMs = 5 * 60 * 1000, nowMs = Date.now()): boolean {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return true;
  return nowMs - t > thresholdMs;
}

export function absoluteTime(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  return new Date(t).toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
}

export function hourLabel(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  return new Date(t).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
}
