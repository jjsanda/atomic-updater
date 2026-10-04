# atomic-updater

A/B (dual-slot) over-the-air update system for embedded Linux fleets:
a signed RAUC rootfs bundle is pushed from a central Go server to devices,
installed atomically into the inactive slot, and rolled back automatically
by U-Boot/GRUB + a post-boot health check if the new slot fails. The
server tracks per-device state and drives phased rollouts (dev/beta/stable
channels, percentage ramps, sticky auto-pause on failure spikes). Targets:
Raspberry Pi CM4 (ARM64) and generic x86-64, built with Yocto (Scarthgap).
Full rationale and architecture: [docs/design.md](docs/design.md).

```text
  ops ──► admin API + dashboard (:8080)
                 │
          atomic-server (Go) ──► Postgres (devices, rollouts, events)
                 │        └────► S3 store (signed RAUC bundles)
            mTLS │  ▲ presigned bundle URL
                 ▼  │
   agent ─► RAUC ─► U-Boot / GRUB          ← device: rootfs.A | rootfs.B
     └─ atomic-health: mark good/bad → auto-rollback
```

## Repo map

| Path | Contents |
|---|---|
| `server/` | atomic-server: device + admin APIs, rollout controller, embedded React dashboard (`server/ui/`), Postgres migrations |
| `device/agent/` | check-in agent (static Go binary) — poll, download, `rauc install`, report |
| `device/health/` | `atomic-health` (C) — post-boot probes, mark-good/mark-bad |
| `device/meta-atomic/` | Yocto layer + kas configs for `raspberrypi-cm4` / `qemux86-64` |
| `bundle/` | rauc-tools image + bundle helper scripts |
| `e2e/` | Docker-compose OTA suite: server + Postgres + MinIO + simulated RAUC devices |
| `scripts/pki/` | development PKI (root/signing/device/manufacturing CAs, see [docs/security.md](docs/security.md)) |
| `scripts/dev/` | dev compose stack + helper scripts |
| `ci/` | GitLab CI stage definitions (org runners only, see `ci/shared/runners.md`) |
| `docs/` | design, API contract, operations, security |

## Quickstart

Prerequisites: Go 1.22+, Docker (with compose v2), Node 22, GNU make.

```sh
make pki               # 1. development PKI under ./pki/
make test              # 2. all host-runnable unit + property tests
make images bundles    # 3. rauc-tools + device-sim images, demo signed bundles
make e2e               # 4. full OTA end-to-end suite (compose stack)
```

Run `make help` for every target.

### Dev stack + dashboard

The interactive dev stack (server + Postgres + MinIO) lives in
`scripts/dev/compose.yaml` (see Makefile):

```sh
# bcrypt hash for the admin login (any bcrypt tool works):
docker run --rm httpd:2.4-alpine htpasswd -nbBC 10 admin 'change-me' | cut -d: -f2
# put it in the stack's ATOMIC_ADMIN_PASSWORD_HASH, then:
docker compose -f scripts/dev/compose.yaml up -d
```

Dashboard: <http://localhost:8080> (login `admin` / your password).
Device API: `https://localhost:8443` (mTLS — enroll a first device with
the curl walkthrough in
[docs/operations.md §5](docs/operations.md#5-enrolling-the-first-device-end-to-end)).
Server configuration reference: [docs/api.md §4](docs/api.md).

## What is validated where

**Runs anywhere** (a laptop, this repo's CI `test` stages): the Go unit +
property tests (`make test`) and the Docker-based e2e OTA suite
(`make images bundles e2e`) — real RAUC installs, signature verification,
rollback, downgrade-rejection and rollout logic against **simulated**
devices in containers.

**Runs only on org infrastructure**: Yocto image builds (kas), U-Boot
integration, QEMU **system** boots of the built images (design §13.2),
the GitLab CI pipeline itself (`.gitlab-ci.yml` + `ci/` — org runner
classes, signing runner), and anything touching real hardware. None of
that is validated by a local checkout, and this repository makes no claim
that it was — the `ci/` definitions are written for the org's runner
fleet and the qemu-system job is explicitly stubbed pending Yocto runner
bring-up.

## Documentation

| Document | What's in it |
|---|---|
| [docs/design.md](docs/design.md) | authoritative design & build plan (architecture, update flow, milestones) |
| [docs/api.md](docs/api.md) | HTTP API contract (device mTLS API + admin API), golden fixtures |
| [docs/operations.md](docs/operations.md) | runbooks: dead-slot recovery, cert expiry, rollout triage, backup/restore, first enrollment |
| [docs/security.md](docs/security.md) | threat model → implemented mechanisms, PKI layout, key ceremony, revocation |
| [ci/shared/runners.md](ci/shared/runners.md) | CI runner classes (docker / dind / signer) and required config |

## License

Licensed under the [Apache License, Version 2.0](LICENSE).
