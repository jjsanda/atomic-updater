# atomic-server HTTP API contract

**Status**: authoritative. Wire types in `server/internal/api/wire.go` and
`device/agent/internal/client/wire.go` are hand-written duplicates of this
document; the golden JSON fixtures embedded in this file are unmarshalled by
contract tests on **both** sides (`server/internal/api/contract_test.go`,
`device/agent/internal/client/contract_test.go`). If you change a wire type,
change this document and both tests will tell you where else to look.

Two listeners:

| Listener | Default addr | Auth | Serves |
|---|---|---|---|
| Device API | `:8443` | mutual TLS | `/api/v1/*`, `/healthz` |
| Admin API | `:8080` | session cookie | `/admin/v1/*`, SPA at `/`, `/healthz` |

All bodies are JSON (`Content-Type: application/json`). Errors use:

```json
{"error": "human readable message"}
```

with an appropriate 4xx/5xx status.

---

## 1. Device API (mTLS, `:8443`)

TLS server config: `ClientAuth = RequireAndVerifyClientCert`, `ClientCAs` =
{device-issuing CA, manufacturing CA}. Middleware computes the **SHA-256
fingerprint of the leaf certificate DER**, lowercase hex, no colons. Requests
whose fingerprint appears in `revoked_certs` are rejected `403`.

Issuer classification:

- Leaf issued by the **manufacturing CA** → may call `/api/v1/enroll` only.
- Leaf issued by the **device CA** and fingerprint matches a `devices` row →
  may call `/api/v1/checkin` and `/api/v1/events`.
- Anything else → `403`.

Retired devices (`devices.status = 'retired'`) receive `410 Gone` on checkin
and events.

### 1.1 `POST /api/v1/enroll`

Client cert: manufacturing. Request:

```json
{
  "csr_pem": "-----BEGIN CERTIFICATE REQUEST-----\n...",
  "hardware_type": "rpi-cm4",
  "serial": "SN-0001"
}
```

- `hardware_type` ∈ {`rpi-cm4`, `x86_64-generic`} (validated).
- CSR signature must verify; the CSR's public key is the device key.
- If a **non-revoked** device already exists with the same public-key
  fingerprint (SHA-256 of PKIX DER), respond `200` and **re-issue** a
  certificate for that existing device (idempotent re-enrollment after cert
  loss). If the existing device's cert is revoked → `403`.

Response `201` (or `200` on re-enrollment):

```json
{
  "device_id": "6f3c63d2-3a3f-4d2e-8f5e-9b1a2c3d4e5f",
  "certificate_pem": "-----BEGIN CERTIFICATE-----\n...",
  "ca_chain_pem": "-----BEGIN CERTIFICATE-----\n..."
}
```

The issued certificate: CN = device UUID, 2-year validity, signed by the
device-issuing CA. `ca_chain_pem` = device CA cert + root CA cert.

### 1.2 `POST /api/v1/checkin`

Client cert: device. Request (golden fixture `checkin_request.json`):

```json
{
  "slot": "A",
  "version": "1.0.0",
  "bundle_id": null,
  "boot_count": 7,
  "uptime_s": 123,
  "healthy": true,
  "last_update": {
    "outcome": "none",
    "bundle_id": null,
    "reason": ""
  }
}
```

- `slot` ∈ {`A`,`B`}; `version` semver (optional leading `v` tolerated,
  normalized server-side); `bundle_id` nullable UUID (what the device believes
  it runs); `last_update.outcome` ∈ {`ok`,`fail`,`rollback`,`none`}.
- Server updates the device row (slot, version, boot_count, last_seen_at,
  last_update_outcome) and self-logs a `checkin` event.

Response `200`, no update:

```json
{"update": null}
```

Response `200`, update assigned (golden fixture `checkin_response_update.json`):

```json
{
  "update": {
    "rollout_id": "0d2f4a9e-1111-4222-8333-444455556666",
    "bundle_id": "9e8d7c6b-5a49-4838-a7b6-c5d4e3f2a1b0",
    "version": "1.1.0",
    "url": "https://store.example/bundles/signed/acme-device-v1-1.1.0.raucb?X-Amz-...",
    "sha256": "d0b425e00e15a0d36ffc2b0c1a1f3b3c4d5e6f708192a3b4c5d6e7f8091a2b3c",
    "size_bytes": 34012160,
    "allow_downgrade": false
  }
}
```

`url` is presigned, 15-minute TTL. The server logs an `offer` event with
`{rollout_id, bundle_id}` payload (at most one per device/rollout/hour to keep
the auto-pause denominator meaningful).

Offer algorithm (see design doc §8.3 + §10.4):

1. Newest `active` rollout (by `activated_at` desc) matching device channel +
   hardware_type.
2. `fnv1a32(deviceID.String() + "|" + rolloutID.String()) % 100 < percentage`.
3. Skip if bundle deprecated, or same as `current_bundle_id`.
4. Skip if `bundle.version <= device.current_version` (semver) unless rollout
   `allow_downgrade`.
5. Skip if the device's most recent `install-fail`/`rollback` event references
   this bundle (no auto-retry of a failed bundle).

### 1.3 `POST /api/v1/events`

Client cert: device. Request (golden fixture `events_request.json`):

```json
{
  "events": [
    {
      "event_type": "install-ok",
      "payload": {"rollout_id": "0d2f4a9e-1111-4222-8333-444455556666",
                   "bundle_id": "9e8d7c6b-5a49-4838-a7b6-c5d4e3f2a1b0",
                   "version": "1.1.0"}
    },
    {
      "event_type": "rollback",
      "payload": {"bundle_id": "9e8d7c6b-5a49-4838-a7b6-c5d4e3f2a1b0",
                   "reason": "health: probe 50-http-selfcheck.sh exit 1"}
    }
  ]
}
```

`event_type` ∈ {`download`, `install-started`, `install-ok`, `install-fail`,
`rollback`}; `payload` is free-form JSON (≤ 16 KiB per event, ≤ 64 events per
batch). Response `202`:

```json
{"accepted": 2}
```

Ingesting `install-fail` or `rollback` triggers auto-pause evaluation for the
referenced rollout (fail-rate > 2%/h over ≥ 25 offered devices → status
`paused`, `paused_reason` set, sticky).

### 1.4 `GET /healthz`

No auth (TLS still required on the device listener). `200 {"status":"ok"}`
when Postgres and object store are reachable, else `503`.

---

## 2. Admin API (session cookie, `:8080`)

### 2.1 Auth

- `POST /admin/v1/login` `{"username": "...", "password": "..."}` →
  `204` + `Set-Cookie: atomic_session=<token>; HttpOnly; SameSite=Lax; Path=/`
  (+`Secure` when the listener has TLS). Session 24 h, stored hashed
  (SHA-256) in `admin_sessions`. Wrong credentials → `401`.
- `POST /admin/v1/logout` → `204`, session row deleted.
- Every other `/admin/v1/*` route: valid session required → else `401`.
- Credentials provisioned via env: `ATOMIC_ADMIN_USER`,
  `ATOMIC_ADMIN_PASSWORD_HASH` (bcrypt).

### 2.2 Fleet

- `GET /admin/v1/summary` →

```json
{
  "devices_total": 12,
  "by_channel": {"dev": 5, "beta": 3, "stable": 4},
  "by_version": {"1.0.0": 7, "1.1.0": 5},
  "markbad_rate_1h": [
    {"bucket": "2026-08-13T10:00:00Z", "count": 0},
    {"bucket": "2026-08-13T11:00:00Z", "count": 2}
  ]
}
```

  (`markbad_rate_1h`: hourly buckets over the trailing 24 h of
  `install-fail` + `rollback` events.)

- `GET /admin/v1/devices?channel=&hardware_type=&q=&limit=50&offset=0` →
  `{"devices": [Device...], "total": 123}` where `Device` is:

```json
{
  "id": "6f3c63d2-3a3f-4d2e-8f5e-9b1a2c3d4e5f",
  "hardware_type": "rpi-cm4",
  "serial": "SN-0001",
  "channel": "dev",
  "status": "active",
  "current_version": "1.0.0",
  "current_bundle_id": null,
  "current_slot": "A",
  "boot_count": 7,
  "last_update_outcome": "none",
  "healthy": true,
  "last_seen_at": "2026-08-13T11:41:00Z",
  "created_at": "2026-08-13T09:00:00Z"
}
```

- `GET /admin/v1/devices/{id}` → `Device`; 404 if unknown.
- `GET /admin/v1/devices/{id}/events?limit=100` →
  `{"events": [{"id":1,"event_type":"checkin","payload":{},"created_at":"..."}]}`
  newest first.
- `PATCH /admin/v1/devices/{id}` body may contain `channel` and/or `status`
  (`active`|`retired`) → `200` updated `Device`.
- `POST /admin/v1/devices/{id}/revoke` `{"reason": "lost device"}` → `204`;
  inserts into `revoked_certs`; device's next request fails `403`.

### 2.3 Bundles

- `GET /admin/v1/bundles` → `{"bundles": [Bundle...]}`:

```json
{
  "id": "9e8d7c6b-5a49-4838-a7b6-c5d4e3f2a1b0",
  "version": "1.1.0",
  "hardware_type": "rpi-cm4",
  "object_key": "signed/acme-device-v1-rpi-cm4-1.1.0.raucb",
  "sha256": "d0b425e00e15a0d36ffc2b0c1a1f3b3c4d5e6f708192a3b4c5d6e7f8091a2b3c",
  "size_bytes": 34012160,
  "signed_by": "b1946ac92492d2347c6235b4d2611184b1946ac92492d2347c6235b4d2611184",
  "status": "active",
  "uploaded_at": "2026-08-13T09:30:00Z"
}
```

- `POST /admin/v1/bundles` `{"object_key": "signed/x.raucb"}` — registers an
  object already in the store; server reads `<object_key>.meta.json` sidecar
  (`{"version","hardware_type","sha256","size_bytes","signed_by"}`), verifies
  the object exists and its size matches → `201 Bundle`. Duplicate
  version+hardware_type → `409`.
- `POST /admin/v1/bundles/scan` → `202 {"registered": N}` — forces a poller
  pass over the `signed/` prefix (poller also runs every 30 s).
- `PATCH /admin/v1/bundles/{id}` `{"status": "deprecated"}` (or `"active"`) →
  `200 Bundle`. Deprecated bundles are never offered.

### 2.4 Rollouts

- `GET /admin/v1/rollouts` → `{"rollouts": [Rollout...]}`:

```json
{
  "id": "0d2f4a9e-1111-4222-8333-444455556666",
  "bundle_id": "9e8d7c6b-5a49-4838-a7b6-c5d4e3f2a1b0",
  "bundle_version": "1.1.0",
  "hardware_type": "rpi-cm4",
  "channel": "dev",
  "percentage": 25,
  "allow_downgrade": false,
  "status": "active",
  "paused_reason": null,
  "created_at": "2026-08-13T09:35:00Z",
  "activated_at": "2026-08-13T09:36:00Z"
}
```

- `POST /admin/v1/rollouts`
  `{"bundle_id","channel","percentage","allow_downgrade"}` → `201 Rollout`
  with `status:"draft"`.
- `PATCH /admin/v1/rollouts/{id}` — any of:
  - `{"percentage": 50}` — allowed while `draft`/`active`/`paused`.
  - `{"status": "active"}` — from `draft` (sets `activated_at`) or `paused`
    (operator resume; clears `paused_reason`).
  - `{"status": "paused"}` — from `active`.
  - `{"status": "completed"}` / `{"status": "aborted"}` — from
    `active`/`paused`.
  Invalid transitions → `409`. Response `200 Rollout`.
- `GET /admin/v1/rollouts/{id}/stats` →

```json
{
  "offered": 40,
  "installed_ok": 37,
  "failed": 1,
  "fail_rate_1h": 2.5,
  "paused_reason": null
}
```

  (`offered` = distinct devices with an `offer` event for this rollout;
  `fail_rate_1h` = percentage over the trailing hour.)

### 2.5 SPA + health

- `GET /healthz` → as §1.4, no auth.
- Everything else on `:8080` not under `/admin/v1/` serves the embedded SPA
  (`index.html` fallback for client-side routes, static assets with
  content hashes cached immutable).

---

## 3. Auto-pause semantics (normative)

Evaluated when an `install-fail` or `rollback` event referencing rollout *R*
is ingested, in the same transaction:

```
offered  = COUNT(DISTINCT device_id) offer events for R in last 1h
fails    = COUNT(*) install-fail + rollback events for R in last 1h
IF rollout.status = 'active' AND offered >= min_cohort (default 25)
   AND fails * 100 > offered * fail_pct (default 2)
THEN status := 'paused',
     paused_reason := 'auto: <fails>/<offered> failures in last hour'
```

Sticky: only an explicit admin `PATCH {"status":"active"}` resumes.
Thresholds configurable: `ATOMIC_PAUSE_MIN_COHORT`, `ATOMIC_PAUSE_FAIL_PCT`.

---

## 4. Server configuration (env)

| Var | Default | Meaning |
|---|---|---|
| `ATOMIC_DB_URL` | — (required) | `postgres://user:pass@host:5432/atomic` |
| `ATOMIC_S3_ENDPOINT` | — (required) | `minio:9000` |
| `ATOMIC_S3_PUBLIC_ENDPOINT` | = endpoint | host devices use in presigned URLs |
| `ATOMIC_S3_ACCESS_KEY` / `ATOMIC_S3_SECRET_KEY` | — | credentials |
| `ATOMIC_S3_BUCKET` | `bundles` | bucket name |
| `ATOMIC_S3_USE_SSL` | `false` | TLS to object store |
| `ATOMIC_DEVICE_ADDR` | `:8443` | device listener |
| `ATOMIC_ADMIN_ADDR` | `:8080` | admin listener |
| `ATOMIC_TLS_CERT` / `ATOMIC_TLS_KEY` | — (required) | device-listener server cert/key |
| `ATOMIC_DEVICE_CA_CERT` / `ATOMIC_DEVICE_CA_KEY` | — (required) | online device-issuing CA |
| `ATOMIC_MFG_CA_CERT` | — (required) | manufacturing CA (enrollment trust) |
| `ATOMIC_CA_CHAIN` | — (required) | PEM chain returned to devices on enroll |
| `ATOMIC_ADMIN_USER` | `admin` | operator username |
| `ATOMIC_ADMIN_PASSWORD_HASH` | — (required) | bcrypt hash |
| `ATOMIC_PRESIGN_TTL` | `15m` | presigned URL TTL |
| `ATOMIC_BUNDLE_POLL_INTERVAL` | `30s` | signed/ prefix scan cadence (`0` disables) |
| `ATOMIC_PAUSE_MIN_COHORT` | `25` | auto-pause min offered devices |
| `ATOMIC_PAUSE_FAIL_PCT` | `2` | auto-pause failure threshold (%) |
| `ATOMIC_LOG_LEVEL` | `info` | slog level |

Migrations run automatically at startup (`golang-migrate`, embedded FS).
