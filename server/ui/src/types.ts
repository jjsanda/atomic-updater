// Wire types for the Admin API (docs/api.md §2). These mirror the JSON
// shapes exactly; docs/api.md is the authoritative contract.

export type Channel = 'dev' | 'beta' | 'stable';
export type HardwareType = 'rpi-cm4' | 'x86_64-generic';
export type DeviceStatus = 'active' | 'retired';
export type Slot = 'A' | 'B';
export type UpdateOutcome = 'ok' | 'fail' | 'rollback' | 'none';
export type BundleStatus = 'active' | 'deprecated';
export type RolloutStatus = 'draft' | 'active' | 'paused' | 'completed' | 'aborted';

export const CHANNELS: Channel[] = ['dev', 'beta', 'stable'];

// GET /admin/v1/summary
export interface MarkbadBucket {
  bucket: string; // RFC 3339 hour bucket
  count: number;
}

export interface Summary {
  devices_total: number;
  by_channel: Record<string, number>;
  by_version: Record<string, number>;
  markbad_rate_1h: MarkbadBucket[];
}

// GET /admin/v1/devices
export interface Device {
  id: string;
  hardware_type: HardwareType;
  serial: string;
  channel: Channel;
  status: DeviceStatus;
  current_version: string;
  current_bundle_id: string | null;
  current_slot: Slot;
  boot_count: number;
  last_update_outcome: UpdateOutcome;
  healthy: boolean;
  last_seen_at: string;
  created_at: string;
}

export interface DeviceList {
  devices: Device[];
  total: number;
}

// GET /admin/v1/devices/{id}/events
export interface DeviceEvent {
  id: number;
  event_type: string;
  payload: unknown;
  created_at: string;
}

export interface DeviceEventList {
  events: DeviceEvent[];
}

// GET /admin/v1/bundles
export interface Bundle {
  id: string;
  version: string;
  hardware_type: HardwareType;
  object_key: string;
  sha256: string;
  size_bytes: number;
  signed_by: string;
  status: BundleStatus;
  uploaded_at: string;
}

export interface BundleList {
  bundles: Bundle[];
}

// POST /admin/v1/bundles/scan
export interface ScanResult {
  registered: number;
}

// GET /admin/v1/rollouts
export interface Rollout {
  id: string;
  bundle_id: string;
  bundle_version: string;
  hardware_type: HardwareType;
  channel: Channel;
  percentage: number;
  allow_downgrade: boolean;
  status: RolloutStatus;
  paused_reason: string | null;
  created_at: string;
  activated_at: string | null;
}

export interface RolloutList {
  rollouts: Rollout[];
}

// POST /admin/v1/rollouts
export interface CreateRolloutRequest {
  bundle_id: string;
  channel: Channel;
  percentage: number;
  allow_downgrade: boolean;
}

// GET /admin/v1/rollouts/{id}/stats
export interface RolloutStats {
  offered: number;
  installed_ok: number;
  failed: number;
  fail_rate_1h: number;
  paused_reason: string | null;
}
