-- atomic-updater initial schema (design doc §8.2 + implementation additions
-- documented in docs/api.md).

CREATE TABLE bundles (
    id            uuid PRIMARY KEY,
    version       text NOT NULL,              -- canonical semver, no leading 'v'
    hardware_type text NOT NULL CHECK (hardware_type IN ('rpi-cm4', 'x86_64-generic')),
    object_key    text NOT NULL UNIQUE,       -- key under signed/ in the object store
    sha256        text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    size_bytes    bigint NOT NULL CHECK (size_bytes > 0),
    signed_by     text NOT NULL DEFAULT '',   -- signing cert fingerprint (sidecar meta)
    status        text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deprecated')),
    uploaded_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (version, hardware_type)
);

CREATE TABLE devices (
    id                  uuid PRIMARY KEY,
    cert_fingerprint    text NOT NULL UNIQUE,   -- sha256 of leaf DER, lowercase hex
    pubkey_fingerprint  text NOT NULL UNIQUE,   -- sha256 of PKIX pubkey DER; stable across reissue
    hardware_type       text NOT NULL CHECK (hardware_type IN ('rpi-cm4', 'x86_64-generic')),
    serial              text NOT NULL DEFAULT '',
    channel             text NOT NULL DEFAULT 'dev' CHECK (channel IN ('dev', 'beta', 'stable')),
    status              text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'retired')),
    current_bundle_id   uuid REFERENCES bundles (id),
    current_version     text NOT NULL DEFAULT '',
    current_slot        text NOT NULL DEFAULT '' CHECK (current_slot IN ('', 'A', 'B')),
    boot_count          bigint NOT NULL DEFAULT 0,
    last_update_outcome text NOT NULL DEFAULT 'none'
                        CHECK (last_update_outcome IN ('none', 'ok', 'fail', 'rollback')),
    healthy             boolean NOT NULL DEFAULT true,
    last_seen_at        timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE rollouts (
    id              uuid PRIMARY KEY,
    bundle_id       uuid NOT NULL REFERENCES bundles (id),
    channel         text NOT NULL CHECK (channel IN ('dev', 'beta', 'stable')),
    percentage      int NOT NULL CHECK (percentage BETWEEN 0 AND 100),
    allow_downgrade boolean NOT NULL DEFAULT false,
    status          text NOT NULL DEFAULT 'draft'
                    CHECK (status IN ('draft', 'active', 'paused', 'completed', 'aborted')),
    paused_reason   text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    activated_at    timestamptz
);

-- Append-only audit log. 'offer' rows are server-generated and form the
-- denominator of the auto-pause calculation (docs/api.md §3).
CREATE TABLE device_events (
    id         bigserial PRIMARY KEY,
    device_id  uuid NOT NULL REFERENCES devices (id),
    event_type text NOT NULL CHECK (event_type IN
        ('checkin', 'offer', 'enroll', 'revoke', 'download',
         'install-started', 'install-ok', 'install-fail', 'rollback')),
    payload    jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX device_events_device_idx ON device_events (device_id, created_at DESC);
CREATE INDEX device_events_offer_rollout_idx
    ON device_events ((payload ->> 'rollout_id'), created_at)
    WHERE event_type = 'offer';
CREATE INDEX device_events_fail_rollout_idx
    ON device_events ((payload ->> 'rollout_id'), created_at)
    WHERE event_type IN ('install-fail', 'rollback');
CREATE INDEX device_events_markbad_idx
    ON device_events (created_at)
    WHERE event_type IN ('install-fail', 'rollback');

CREATE TABLE revoked_certs (
    cert_fingerprint text PRIMARY KEY,
    serial           text NOT NULL DEFAULT '',
    reason           text NOT NULL DEFAULT '',
    revoked_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE admin_sessions (
    token_hash text PRIMARY KEY,      -- sha256(cookie token), hex
    username   text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
