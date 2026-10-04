# atomic-updater — Design & Build Plan

**Type** design document / build plan
**Status** Draft v1 — for team review
**Owner** *TBD*
**Reviewers** *TBD*
**Last updated** 2026-08-13

---

## 1. TL;DR

**atomic-updater** is an A/B ("dual-slot") over-the-air update system for embedded Linux devices deployed in the field. It ships a signed root-filesystem bundle from a central server to a device, atomically switches the device onto it, and rolls back automatically if the new slot fails to boot or fails a post-boot health check. A minimal fleet-management server hosts bundles, tracks device state, and orchestrates phased rollouts.

Built on **RAUC** (device-side update client), **U-Boot with the upstream RAUC bootmeth** (bootloader), **GRUB via `efibootmgr`** (x86 EFI), and a **custom Go server**. Target platforms: Raspberry Pi CM4 (ARM64) and a generic x86-64 mini-PC.

**MVP timeline**: 8 weeks with a 2–3 person team.

---

## 2. Why this exists

**Regulatory pressure.** The EU Cyber Resilience Act (Regulation (EU) 2024/2847) requires products with digital elements sold into the EU to provide security updates throughout their lifecycle, with vulnerability-reporting obligations effective September 2026 and full compliance by December 2027. Assumption: any connected device our organization ships into the EU will require a working OTA mechanism well before that deadline. Compliance details should be confirmed with legal against the actual regulation text before we take dependencies on specific interpretations.

**Product durability.** Field-deployed embedded devices live 5–10 years. Sending a technician to every site with a USB stick does not scale past double-digit fleet sizes. An in-place update mechanism that cannot brick devices is table stakes for anything beyond a pilot.

**Build-vs-buy.** All three dominant OSS A/B update engines — RAUC, Mender, SWUpdate — are production-ready in 2026 (see §6). Writing our own client from scratch is not the value we add. Writing our own **rollout controller and fleet server** is: it lets us keep device state in our own systems, integrate with our identity and audit stack, and avoid vendor lock-in on the server side.

---

## 3. Goals & non-goals

### Goals (MVP)

- Push a **signed** rootfs bundle from server to device
- Device installs bundle into the inactive slot **atomically** (all-or-nothing)
- Bootloader tries the new slot with limited retry attempts; **rolls back on repeated boot failure**
- Post-boot **health check** confirms the slot; on failure, mark slot bad and reboot into the old slot
- Bundle signature validated against an **org-controlled PKI** rooted in an offline CA
- Server-side rollout with per-device state, channel assignment (dev / beta / stable), and **phased rollout percentages** with an auto-pause on failure spike
- Works on Raspberry Pi CM4 (ARM64) and a generic x86-64 target
- Fully reproducible builds under Yocto (Scarthgap LTS)

### Non-goals (MVP)

- Delta / adaptive updates — Phase 2
- Secure boot / verified boot chain — Phase 3
- Container-based application updates — out of scope
- MTD / UBI raw-NAND support — out of scope; eMMC / SD / NVMe only
- Bootloader A/B updates — out of scope (bootloader is frozen at manufacture)
- SLA-grade multi-region server — single region, single node MVP

---

## 4. Success criteria

MVP is considered done when all of the following hold on a release candidate build:

- 100 unattended updates across 10 QEMU instances (10 per instance) without a single bricked instance
- 20 unattended updates on real hardware (10 RPi CM4, 10 x86 mini-PC) without a bricked device
- Median update wall-clock under 90 seconds on stable hardware, excluding download
- Automatic rollback triggers within 5 minutes of a failed health check
- Any tampered or unsigned bundle rejected before any partition write, within 500 ms of parsing
- Server handles 1 000 concurrent device check-ins on a 4 vCPU / 8 GB node under p95 latency of 200 ms

---

## 5. High-level architecture

```
                       ┌───────────────────────────────────┐
                       │           Ops / Web UI            │
                       └───────────────┬───────────────────┘
                                       │ HTTPS + session auth
                                       ▼
   ┌─────────────────────────────────────────────────────────────┐
   │                    atomic-server (Go)                       │
   │  ┌────────────┐  ┌───────────────┐  ┌────────────────────┐  │
   │  │  REST API  │  │Rollout ctrl.  │  │  Web UI (embed.FS) │  │
   │  └─────┬──────┘  └───────┬───────┘  └────────────────────┘  │
   └────────┼─────────────────┼──────────────────────────────────┘
            │ Postgres        │ presigned URLs
            ▼                 ▼
   ┌────────────────┐   ┌──────────────────────┐
   │   Postgres 15  │   │  S3-compatible store │
   │  (devices,     │   │  (bundles, chunks)   │
   │   rollouts,    │   └──────────────────────┘
   │   events)      │
   └────────────────┘

                              mTLS ▲ (client cert per device)
                                   │
        ┌──────────────────────────┴───────────────────────────┐
        │                                                      │
   ┌────┴──────────┐                                    ┌──────┴──────┐
   │  Device #N    │       ... (fleet of N devices)     │  Device #1  │
   │  ┌─────────┐  │                                    │             │
   │  │ agent   │  │  poll / download / install         │             │
   │  ├─────────┤  │                                    │             │
   │  │ RAUC    │  │                                    │             │
   │  ├─────────┤  │                                    │             │
   │  │ health  │  │                                    │             │
   │  ├─────────┤  │                                    │             │
   │  │ U-Boot  │  │                                    │             │
   │  └─────────┘  │                                    │             │
   └───────────────┘                                    └─────────────┘
```

**Device side** runs three pieces of software on top of a Yocto-built image: the RAUC daemon, a custom `atomic-health` service, and a small check-in agent that polls the server.

**Server side** is a Go HTTP service backed by Postgres for state and an S3-compatible object store for bundles.

**Trust root** is an offline organization root CA. A signing CA signs per-build bundle certificates. Devices ship with the root CA public certificate baked into their initramfs so nothing on the network can substitute a trust anchor.

---

## 6. Technology choices & rationale

### 6.1 Update client — RAUC

Chosen over Mender and SWUpdate. Rationale:

- **Mandatory bundle signing is enforced by design.** No accidental unsigned deployments; the daemon refuses.
- **Small footprint** — main binary ~500 KB, minimal runtime dependencies.
- **Best-in-class bootloader integration.** U-Boot ships an upstream RAUC bootmeth; GRUB and EFI are supported first-class.
- **Delta / adaptive updates in open source.** Not gated behind an Enterprise license (unlike Mender's delta feature).
- **Server-agnostic.** RAUC has no bundled server, which is a feature for us — we own the rollout controller.
- **License** LGPL-2.1 for the daemon.

Trade-off: Mender would give us a working server on day one; we would then be stuck with it, and delta updates would cost money.

### 6.2 Bootloader

- **ARM (RPi CM4)** — U-Boot with the upstream **RAUC bootmeth** (`CONFIG_BOOTMETH_RAUC`). Manages `BOOT_ORDER`, `BOOT_A_LEFT`, `BOOT_B_LEFT` environment variables with a configurable per-slot retry count. See U-Boot docs for the exact env-variable semantics.
- **x86 (mini-PC)** — GRUB behind the RAUC **EFI backend**. RAUC drives `efibootmgr --bootorder` and `--bootnext` to switch slots.

### 6.3 Build system — Yocto Project (Scarthgap LTS)

- `meta-rauc` and `meta-rauc-community` provide recipes, machine integrations, and reference bundles.
- Scarthgap is the current LTS with four+ years of upstream security backports — matches our device lifecycle.
- Reproducible builds required (`BB_REPRODUCIBLE_BUILD = "1"`).

### 6.4 Server — Go 1.22+ with Postgres 15

- **Single binary deploy.** Fits on modest hardware; could run at the edge if we ever need a regional relay.
- **Goroutine model** suits long-lived device polling and streaming bundle downloads.
- **Postgres** for structured state (devices, rollouts, events). Boring, reliable, well understood.
- **Bundle storage** — S3-compatible: MinIO in dev, Cloudflare R2 or AWS S3 in prod.

### 6.5 CI — GitLab CI on Docker runners

- Aligns with existing GitLab tooling in the org.
- YAML pipeline; shared SSTATE cache mounted from an NFS or S3 backing store to keep Yocto builds under ~15 minutes warm-cache.

---

## 7. Device-side design

### 7.1 Partition layout (standard 4-partition scheme)

| # | Name | Size (RPi) | Filesystem | Purpose |
|---|------|-----------|------------|---------|
| 1 | `boot` | 128 MB | FAT32 | Kernel + DTB per slot, U-Boot env |
| 2 | `rootfs.A` | 2 GB | ext4 (ro) | System slot A |
| 3 | `rootfs.B` | 2 GB | ext4 (ro) | System slot B |
| 4 | `data` | remainder | ext4 (rw) | Persistent app data, logs, RAUC status file |

Root filesystem is mounted **read-only**. `/etc`, `/var`, and `/home` are bind-mounted from directories under `/data/` so writable state survives updates.

### 7.2 Bootloader integration

U-Boot environment holds three RAUC-related variables:

```
BOOT_ORDER=A B         # slot A tried first
BOOT_A_LEFT=3          # 3 attempts remaining for slot A
BOOT_B_LEFT=3          # 3 attempts remaining for slot B
```

Boot flow:

1. U-Boot reads `BOOT_ORDER`.
2. For each slot in order: if `BOOT_x_LEFT > 0`, decrement it, boot that slot with `rauc.slot=X` appended to the kernel command line.
3. If a slot's counter hits zero, U-Boot skips to the next slot.
4. If both counters hit zero, U-Boot enters a rescue state. **MVP** — halt at the serial console with a documented recovery procedure. **Phase 3** — boot into a dedicated rescue partition.

Once Linux boots successfully and the health check passes, `atomic-health` writes `BOOT_x_LEFT=3` back via `fw_setenv`, marking the slot "good."

### 7.3 RAUC configuration (`/etc/rauc/system.conf`)

Kept minimal for MVP:

```ini
[system]
compatible=acme-device-v1
bootloader=uboot
data-directory=/data/rauc

[keyring]
path=/etc/rauc/ca.cert.pem

[slot.rootfs.0]
device=/dev/mmcblk0p2
type=ext4
bootname=A

[slot.rootfs.1]
device=/dev/mmcblk0p3
type=ext4
bootname=B
```

Certificate material on the device:

- `/etc/rauc/ca.cert.pem` — root CA public cert
- `/etc/rauc/signing-ca.cert.pem` — signing CA public cert (used for chain validation)

Both are shipped in the base image, not writable at runtime, and their integrity is checked by the health service on every boot.

### 7.4 Health check service

`atomic-health.service` (systemd, `Type=oneshot`) runs after `network-online.target` and executes, in order:

1. Verify the application service (`app.service`) is `active`.
2. Run all user-defined probes in `/usr/lib/atomic-updater/health.d/*.sh` — each must exit 0 within its own timeout.
3. Optional HTTP self-check against a device-local health endpoint.
4. Verify system clock is synchronized (chrony `tracking` state).

On success:

```
rauc status mark-good
fw_setenv BOOT_${slot}_LEFT 3
```

On failure:

```
rauc status mark-bad
logger -t atomic-health "slot ${slot} failed health: ${reason}"
systemctl reboot
```

Unit fragment:

```ini
[Unit]
Description=Post-boot health check for atomic-updater
After=network-online.target app.service
Requires=app.service
Before=multi-user.target

[Service]
Type=oneshot
ExecStart=/usr/lib/atomic-updater/atomic-health
TimeoutStartSec=120
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
```

`TimeoutStartSec=120` bounds how long a slot can pretend to be alive without confirming.

### 7.5 Check-in agent

Small Go binary (~5 MB, statically linked, no CGO). Polls the server on a jittered 60 s ±10 s cadence and:

- Reports current slot, installed RAUC bundle version, boot count, uptime, and the outcome of the previous update attempt
- If the server returns an assigned bundle URL, downloads to `/data/atomic-updater/pending.raucb`
- Invokes `rauc install /data/atomic-updater/pending.raucb`
- Schedules a reboot inside the maintenance window configured for that device (default: any time; production devices set 02:00–04:00 local)

**Authentication** mutual TLS. Each device ships with a client certificate issued during first-boot enrollment (see §11).

---

## 8. Server-side design

### 8.1 Components

- **`atomic-server`** — Go binary exposing:
  - Device REST API (`/api/v1/devices/...`) — mTLS
  - Admin REST API (`/admin/v1/...`) — session auth
  - Web dashboard — served from `embed.FS`
- **Postgres 15** — device / bundle / rollout / event state
- **S3-compatible object store** — bundles and, in Phase 2, delta chunks
- **Signing service** — separate machine or HSM; never reachable from the API service. CI publishes unsigned bundles to a staging bucket; a manual step in the signing pipeline signs and copies to the production bucket

### 8.2 Data model (Postgres)

Simplified — full DDL lives in `server/migrations/`:

```
devices
  id (uuid, pk)
  cert_fingerprint (text, unique)
  hardware_type (text)                 -- 'rpi-cm4' | 'x86_64-generic'
  channel (text)                       -- 'dev' | 'beta' | 'stable'
  current_bundle_id (uuid, fk nullable)
  current_slot (text)                  -- 'A' | 'B'
  last_seen_at (timestamptz)
  created_at (timestamptz)

bundles
  id (uuid, pk)
  version (text, semver)
  hardware_type (text)
  storage_url (text)
  sha256 (text)
  signed_by (text)                     -- signing cert fingerprint
  uploaded_at (timestamptz)

rollouts
  id (uuid, pk)
  bundle_id (uuid, fk)
  channel (text)
  percentage (int)                     -- 0..100
  status (text)                        -- 'draft' | 'active' | 'paused' | 'completed'
  created_at (timestamptz)
  activated_at (timestamptz nullable)

device_events                          -- append-only audit log
  id (bigserial, pk)
  device_id (uuid, fk)
  event_type (text)                    -- 'checkin' | 'download' | 'install-ok' | 'install-fail' | 'rollback'
  payload (jsonb)
  created_at (timestamptz)
```

### 8.3 Rollout controller

On every device check-in:

1. Look up the device's `channel` and `hardware_type`.
2. Find the most recent `active` rollout matching both.
3. Compute deterministic assignment:
   `included = fnv1a(device_id ‖ rollout_id) mod 100 < rollout.percentage`
   Deterministic hashing means a device's inclusion decision is stable across restarts and does not flip as the percentage ramps.
4. If `included` and `device.current_bundle_id != rollout.bundle_id`, return the bundle URL (presigned, 15-minute TTL).
5. Otherwise return `{"update": null}`.

Ramp policy (operator-driven, with automatic guard rails):

- Manual ramps 1% → 5% → 25% → 100%
- Automatic pause if `install-fail` events in the last cohort exceed 2% per hour
- Automatic pause is sticky — an operator must inspect and resume

### 8.4 Web dashboard

Deliberately minimal for MVP:

- Fleet overview — device count by channel, current version distribution, mark-bad rate over time
- Device detail — event timeline, current state, force-reassign channel, retire device
- Bundle list — upload, view metadata, verify signature, deprecate
- Rollout view — create draft, activate, ramp, pause, abort

React SPA, served from the Go binary via `embed.FS` so there is exactly one deployable artifact.

---

## 9. Update bundle format & signing

RAUC bundles are squashfs archives containing a manifest, the slot images, and a CMS signature over the manifest. We use the **verity-format bundle** (RAUC 1.7+) for streamable installation and per-chunk verification.

### 9.1 PKI

```
[Offline] Root CA                     — 10-year validity, air-gapped
    └── [Offline] Signing CA          —  2-year validity, air-gapped
             └── Bundle signing cert  — 90-day rotation, generated per release
```

Bundle certificate chain is validated up to the root CA on the device. The signing CA is decoupled from the root so we can rotate the signing CA without re-provisioning devices.

### 9.2 Signing pipeline

- CI produces an **unsigned** RAUC bundle and uploads it to a `staging/` prefix in the bundle store.
- A dedicated GitLab runner tagged `signer` is the only place the signing key material lives.
- The runner pulls the unsigned bundle, signs with the current signing cert, and copies to the `signed/` prefix.
- The Go server observes new objects under `signed/` and inserts a row into `bundles`.

**MVP key handling**: the "offline" root and signing CA private keys live in a sealed KeePassXC database on a dedicated laptop that never touches the internet. Quarterly re-signing of the signing CA. **Phase 3**: move to a real HSM (YubiHSM 2, Nitrokey HSM, or a cloud KMS-backed offline signer).

---

## 10. Update flow (state machine)

### 10.1 Happy path

```
1. Device polls server                              → server: bundle URL
2. Device downloads bundle → pending.raucb
3. rauc install:
     a. Verify signature against installed CA
     b. Write rootfs image to inactive slot
     c. Update U-Boot env:
          BOOT_ORDER="new old"
          BOOT_new_LEFT=3
4. Device reboots
5. U-Boot picks new slot; BOOT_new_LEFT: 3 → 2; boots with rauc.slot=NEW
6. Kernel + userspace start
7. atomic-health.service runs → probes pass
8. rauc status mark-good; fw_setenv BOOT_new_LEFT 3
9. Agent reports install-ok to server
```

### 10.2 Failed boot (kernel panic, systemd fails, etc.)

```
1–5 as above
6. Board resets before health service runs
7. U-Boot: BOOT_new_LEFT=2 → 1, retries
8. Same failure → BOOT_new_LEFT=0
9. Next boot: U-Boot skips new slot, boots old slot
10. Old slot boots, agent reports rollback to server
```

### 10.3 Failed health check (app broken, no network, unhealthy)

```
1–6 as happy path
7. atomic-health: probes fail after 90 s
8. rauc status mark-bad; reboot
9. U-Boot: marked-bad slot has BOOT_LEFT=0 → boots old slot
10. Old slot boots; agent reports rollback with health-fail reason
```

### 10.4 Server response to rollback

- Increment rollout failure counter
- If cohort failure rate > 2% per hour → auto-pause rollout
- Post event to configured webhook for on-call notification
- Device remains on old bundle; will not be reassigned to the same failed bundle without operator action

---

## 11. Security threat model

### 11.1 In scope for MVP

| Threat | Mitigation |
|---|---|
| Network attacker forges an update | mTLS on all device-server traffic; bundle signature required |
| Compromised CI runner ships bad bundle | Signing is a separate stage on an isolated runner; unsigned bundles cannot reach devices |
| Rollback attack (attacker forces old vulnerable version) | Each bundle has a monotonic version; device rejects `version <= current` unless the rollout has `allow_downgrade=true` |
| Compromised device certificate | Server-side CRL check on every request; revoke and reissue |
| Man-in-the-middle on bundle download | Bundle signature validates end-to-end; presigned URLs are TLS-only and short-lived |

### 11.2 Out of scope for MVP (documented for Phase 3)

- Physical attacker with device access (needs secure boot, disk encryption, tamper detection)
- Side-channel attacks on the signing runner
- Bootloader compromise — bootloader is not A/B'd. A corrupted bootloader flash bricks the device. Documented in `docs/operations.md`.

### 11.3 Provisioning (device enrollment)

MVP flow — first-boot enrollment:

1. Device ships with a manufacturing certificate and a preconfigured server endpoint.
2. On first boot, agent generates a device keypair, submits a CSR signed with the manufacturing cert.
3. Server validates the manufacturing cert against the manufacturing CA, issues a device certificate, records the device in the `devices` table.
4. Agent stores the device certificate on the `data` partition.
5. Manufacturing certificate becomes inert after successful enrollment.

**Open question Q2** — see §17 — is whether we can rely on manufacturing to inject a per-device cert instead of a shared manufacturing cert. That would be strictly better and should be pursued.

---

## 12. Delta / adaptive updates (Phase 2)

RAUC's adaptive-update mode uses block-based delta chunks. Server stores per-chunk hashes for each bundle; device requests only chunks it does not already have. Expected reduction for typical application-layer updates: ~500 MB full bundle → ~30 MB delta.

Requires, additive to the MVP:

- Bundle producer emits per-chunk hashes at build time
- Server exposes `/api/v1/chunks/{hash}` (backed by the same object store)
- Device runs `rauc install --adaptive`

Deferred to Phase 2 deliberately — we need stable full-bundle production and confidence in rollback before we start optimizing bandwidth.

---

## 13. Testing strategy

### 13.1 Unit tests

- Server handlers: `net/http/httptest`
- Rollout math: **property-based** with `pgregory.net/rapid` — deterministic assignment must be stable across process restarts and stable as `percentage` monotonically increases (no device drops out)
- Health probes: table-driven, one row per probe outcome combination

### 13.2 Integration tests (per MR)

- Bring up a QEMU instance of each target machine
- Server runs in the same Docker network
- Test scenarios (each is a Go test):
  - `TestHappyPath` — install and boot new slot
  - `TestRollbackOnHealthFail` — inject a failing probe, expect rollback
  - `TestRollbackOnKernelPanic` — inject `panic=1` on the kernel cmdline of the new slot, expect rollback
  - `TestRejectUnsignedBundle`
  - `TestRejectDowngrade`
  - `TestResumeAfterNetworkDrop` — kill network mid-download, expect resume

Runs in GitLab CI on every MR to `main`. Blocking.

### 13.3 Chaos / soak

Nightly job: 100 QEMU instances in parallel, each performs 10 updates in a loop with injected failures (10% kernel panic, 10% health-check fail, 5% network drop mid-download). **Success metric: zero bricks across 1 000 update attempts**.

### 13.4 Hardware-in-the-loop

Weekly on the lab bench:

- 1 × RPi CM4
- 1 × x86 mini-PC (Beelink or similar)

Each runs 5 unattended updates. Blocking for `stable`-channel releases.

---

## 14. Repo layout & CI

```
atomic-updater/
├── device/
│   ├── meta-atomic/              # Yocto layer (custom recipes, machine cfg)
│   │   ├── recipes-core/
│   │   ├── recipes-rauc/
│   │   └── conf/machine/
│   ├── health/                   # atomic-health service (C, ~500 LOC)
│   └── agent/                    # check-in agent (Go)
├── server/
│   ├── cmd/atomic-server/        # main entrypoint
│   ├── internal/
│   │   ├── api/                  # HTTP handlers
│   │   ├── rollout/              # assignment + ramp logic
│   │   └── storage/              # Postgres + S3 wrappers
│   ├── migrations/               # Postgres migrations (golang-migrate)
│   └── ui/                       # React dashboard sources
├── ci/
│   ├── build-image.yml           # per-machine Yocto build
│   ├── qemu-test.yml             # integration tests
│   ├── sign-and-publish.yml      # signing runner
│   └── shared/                   # cache config, base images
├── docs/
│   ├── design.md                 # this document
│   ├── operations.md             # runbooks, recovery
│   └── security.md               # threat model, PKI runbook
└── README.md
```

### CI pipeline stages

1. **`lint`** — `go vet`, `staticcheck`, `shellcheck`, `yamllint`, `pre-commit`
2. **`build:device`** — matrix over `raspberrypi-cm4` and `qemux86-64`; produces unsigned RAUC bundle; uploads to `staging/`
3. **`build:server`** — Go binary + multi-arch Docker image
4. **`test:unit`** — Go unit tests + coverage report (fail below 80%)
5. **`test:integration`** — QEMU-based OTA test in a Docker-in-Docker runner
6. **`sign`** — runs on the `signer` runner; manual trigger for `stable` channel, automatic for `dev`
7. **`publish`** — copies signed bundle to `signed/` bucket; registers in server via admin API

Shared Yocto SSTATE cache mounted from S3 to keep warm-cache builds under ~15 min. Cold-cache builds are only expected on version bumps of the Yocto release, and are OK to run overnight.

---

## 15. Milestones

**Milestone 0 — Foundation (Week 1)**

- Repo scaffolded, CI stages 1–3 green
- Yocto image boots on QEMU for both machines
- Postgres schema migrated, server binary boots and responds to `/healthz`

**Milestone 1 — Local OTA (Weeks 2–3)**

- RAUC installs a bundle produced by CI into the second slot
- U-Boot switches slots correctly on RPi; GRUB switches on x86
- `atomic-health` marks slot good
- No server involvement yet — bundle URL is passed on the kernel cmdline for demo purposes

**Milestone 2 — Server + agent (Weeks 4–5)**

- Check-in agent talks to server over mTLS
- Server assigns bundles to devices based on channel
- Web dashboard shows the fleet
- 10 QEMU devices update themselves unattended, all state tracked in the dashboard

**Milestone 3 — Rollback + signing (Weeks 6–7)**

- Signature verification enforced end-to-end
- Injected failures trigger automatic rollback in both failure modes (§10.2, §10.3)
- Rollout percentages and pause-on-failure logic working
- Chaos test at 100 QEMU devices passes with zero bricks

**Milestone 4 — Hardware + hardening (Week 8)**

- Real RPi CM4 and x86 mini-PC updating end-to-end
- Recovery procedures documented in `docs/operations.md`
- Security review sign-off
- MVP release candidate cut

### Phase 2 (post-MVP)

- Adaptive / delta updates
- Bootloader update flow with recovery partition
- HSM-backed signing
- Regional relay servers for edge deployments

### Phase 3 (later)

- Secure boot chain (fTPM / TPM 2.0)
- Full-disk encryption of the `data` partition
- Fleet-wide policy engine (per-device update windows, feature flags, config)

---

## 16. Team & effort

Suggested split for a 2–3 person team over 8 weeks:

| Role | Focus | Effort |
|---|---|---|
| Embedded engineer | Yocto layer, U-Boot integration, RAUC config, health service, hardware bring-up | 100% |
| Backend engineer | Go server, Postgres, rollout controller, dashboard, mTLS | 100% |
| Shared / floating | CI, signing pipeline, integration tests, documentation | 50% |

If the team is two people, embedded and backend each spend ~20% of their time on the shared CI / test / docs work and the timeline slips by roughly one week.

Assumption: at least one team member has prior Yocto experience. If not, add two weeks to Milestone 0 for onboarding — Yocto's learning curve is real.

---

## 17. Risks & open questions

### Risks

| # | Risk | Mitigation |
|---|---|---|
| R1 | Bootloader is a foot-gun — a bad flash bricks the device permanently | MVP freezes the bootloader; manual recovery via serial console is documented; Phase 3 introduces bootloader A/B on dedicated eMMC boot partitions |
| R2 | Health-check heuristic marks a broken slot as good | Strict `TimeoutStartSec`; probes are version-controlled and reviewed; add a required "canary probe" that stresses the app before mark-good |
| R3 | Server compromise pushes malicious bundle | Signing keys not on the server; devices verify chain to offline root; add signed manifest of "known-good bundle hashes" served separately in Phase 2 |
| R4 | Yocto build times exceed CI runner budget | Shared SSTATE cache from Day 1; parallel builds on dedicated runners; cold builds only on release cuts |
| R5 | Postgres becomes a bottleneck at scale | Not an MVP concern; a single node handles ~10k devices comfortably; Phase 3 introduces a read replica for dashboard queries |
| R6 | mTLS certificate expiry causes fleet-wide outage | Certificates monitored server-side; alerts at 30 / 14 / 7 days; automatic rotation added in Phase 2 |
| R7 | Team underestimates Yocto learning curve | See §16 assumption; front-load Yocto work in Milestone 0 |

### Open questions (resolve in Week 1)

- **Q1** Object storage provider — self-hosted MinIO or Cloudflare R2? Egress cost and latency drive the answer.
- **Q2** Device provisioning at manufacturing — can we get a per-device certificate injected at flash time, or do we live with a shared manufacturing cert and first-boot enrollment?
- **Q3** GDPR — do device events count as personal data if a device is 1:1 with an employee? Talk to legal.
- **Q4** Do we need offline install (USB fallback) in the MVP or is that Phase 2?
- **Q5** What is the operational owner for the signing laptop? Physical custody, quarterly key ceremonies, incident response.

---

## 18. References

**RAUC**

- Official documentation — https://rauc.readthedocs.io/
- `meta-rauc` Yocto layer — https://github.com/rauc/meta-rauc
- `meta-rauc-community` — https://github.com/rauc/meta-rauc-community

**U-Boot**

- RAUC bootmeth — https://docs.u-boot-project.org/en/latest/develop/bootstd/rauc.html

**Comparisons and background**

- FOSDEM 2025, Leon Anavi — "Exploring Open Source Dual A/B Update Solutions for Embedded Linux" — https://archive.fosdem.org/2025/schedule/event/fosdem-2025-6299-exploring-open-source-dual-a-b-update-solutions-for-embedded-linux/ — side-by-side of Mender / RAUC / SWUpdate on RPi and iMX8MP.
- ProteanOS — "OTA Updates in 2026: RAUC vs SWUpdate vs Mender for Robust Fleet Management" — https://proteanos.com/doc/ota-updates-rauc-swupdate-mender-2026/

**Regulatory**

- EU Cyber Resilience Act, Regulation (EU) 2024/2847 — full compliance December 2027; vulnerability-reporting obligations from September 2026. *Verify precise obligations with legal before taking dependencies on specific interpretations.*

---

*End of document. Comments and change requests via MR against `docs/design.md`.*
