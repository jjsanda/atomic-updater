// Single fetch wrapper for the Admin API (docs/api.md §2). All calls use
// relative URLs (the SPA is served by the same listener) and same-origin
// credentials so the atomic_session cookie rides along. Any 401 outside the
// login page kicks the user back to /login.

import type {
  Bundle,
  BundleList,
  BundleStatus,
  Channel,
  CreateRolloutRequest,
  Device,
  DeviceEventList,
  DeviceList,
  DeviceStatus,
  Rollout,
  RolloutList,
  RolloutStats,
  ScanResult,
  Summary,
} from './types';

export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body !== undefined) {
    headers.set('Content-Type', 'application/json');
  }

  let res: Response;
  try {
    res = await fetch(path, { ...init, headers, credentials: 'same-origin' });
  } catch {
    throw new ApiError(0, 'network error — server unreachable');
  }

  if (res.status === 401) {
    if (!window.location.pathname.startsWith('/login')) {
      window.location.assign('/login');
    }
    throw new ApiError(401, 'unauthorized');
  }

  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`;
    try {
      const body = (await res.json()) as { error?: unknown };
      if (typeof body.error === 'string' && body.error !== '') {
        message = body.error;
      }
    } catch {
      // non-JSON error body; keep the status line
    }
    throw new ApiError(res.status, message);
  }

  if (res.status === 204) {
    return undefined as T;
  }
  const text = await res.text();
  return (text === '' ? undefined : JSON.parse(text)) as T;
}

function get<T>(path: string): Promise<T> {
  return request<T>(path);
}

function post<T>(path: string, body?: unknown): Promise<T> {
  return request<T>(path, {
    method: 'POST',
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

function patch<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, { method: 'PATCH', body: JSON.stringify(body) });
}

// ---- auth --------------------------------------------------------------

export function login(username: string, password: string): Promise<void> {
  return post<void>('/admin/v1/login', { username, password });
}

export function logout(): Promise<void> {
  return post<void>('/admin/v1/logout');
}

// ---- fleet -------------------------------------------------------------

export function getSummary(): Promise<Summary> {
  return get<Summary>('/admin/v1/summary');
}

export interface DeviceQuery {
  channel?: Channel | '';
  hardware_type?: string;
  q?: string;
  limit?: number;
  offset?: number;
}

export function listDevices(query: DeviceQuery = {}): Promise<DeviceList> {
  const params = new URLSearchParams();
  if (query.channel) params.set('channel', query.channel);
  if (query.hardware_type) params.set('hardware_type', query.hardware_type);
  if (query.q) params.set('q', query.q);
  if (query.limit !== undefined) params.set('limit', String(query.limit));
  if (query.offset !== undefined) params.set('offset', String(query.offset));
  const qs = params.toString();
  return get<DeviceList>(`/admin/v1/devices${qs ? `?${qs}` : ''}`);
}

export function getDevice(id: string): Promise<Device> {
  return get<Device>(`/admin/v1/devices/${encodeURIComponent(id)}`);
}

export function getDeviceEvents(id: string, limit = 100): Promise<DeviceEventList> {
  return get<DeviceEventList>(
    `/admin/v1/devices/${encodeURIComponent(id)}/events?limit=${limit}`,
  );
}

export function patchDevice(
  id: string,
  body: { channel?: Channel; status?: DeviceStatus },
): Promise<Device> {
  return patch<Device>(`/admin/v1/devices/${encodeURIComponent(id)}`, body);
}

export function revokeDevice(id: string, reason: string): Promise<void> {
  return post<void>(`/admin/v1/devices/${encodeURIComponent(id)}/revoke`, { reason });
}

// ---- bundles -----------------------------------------------------------

export function listBundles(): Promise<BundleList> {
  return get<BundleList>('/admin/v1/bundles');
}

export function registerBundle(objectKey: string): Promise<Bundle> {
  return post<Bundle>('/admin/v1/bundles', { object_key: objectKey });
}

export function scanBundles(): Promise<ScanResult> {
  return post<ScanResult>('/admin/v1/bundles/scan');
}

export function patchBundle(id: string, status: BundleStatus): Promise<Bundle> {
  return patch<Bundle>(`/admin/v1/bundles/${encodeURIComponent(id)}`, { status });
}

// ---- rollouts ----------------------------------------------------------

export function listRollouts(): Promise<RolloutList> {
  return get<RolloutList>('/admin/v1/rollouts');
}

export function createRollout(body: CreateRolloutRequest): Promise<Rollout> {
  return post<Rollout>('/admin/v1/rollouts', body);
}

export function patchRollout(
  id: string,
  body: { percentage?: number; status?: string },
): Promise<Rollout> {
  return patch<Rollout>(`/admin/v1/rollouts/${encodeURIComponent(id)}`, body);
}

export function getRolloutStats(id: string): Promise<RolloutStats> {
  return get<RolloutStats>(`/admin/v1/rollouts/${encodeURIComponent(id)}/stats`);
}
