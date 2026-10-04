# atomic-updater — Operations runbooks

Practical procedures for operating the MVP deployment (single server node,
Postgres + MinIO, fleets of RPi CM4 and x86-64 devices). API paths and
payloads follow [docs/api.md](api.md); design references are to
[docs/design.md](design.md).

| # | Runbook |
|---|---|
| 1 | [Both slots dead — serial-console recovery](#1-both-slots-dead--serial-console-recovery) |
| 2 | [Fleet-wide certificate expiry](#2-fleet-wide-certificate-expiry) |
| 3 | [Rollout stuck or auto-paused](#3-rollout-stuck-or-auto-paused) |
| 4 | [Postgres + MinIO backup / restore](#4-postgres--minio-backup--restore) |
| 5 | [Enrolling the first device end-to-end](#5-enrolling-the-first-device-end-to-end) |
| 6 | [Bootloader corruption — what "frozen bootloader" means](#6-bootloader-corruption--what-frozen-bootloader-means) |

---

## 1. Both slots dead — serial-console recovery

Per design §7.2, when `BOOT_A_LEFT` and `BOOT_B_LEFT` both reach 0 the MVP
behaviour is to **halt at the serial console** — there is no rescue
partition until Phase 3. Getting here means both rootfs slots failed to
boot three times each; the device needs hands-on recovery.

You need: physical access, a known-good release (rootfs `.ext4` slot image
+ boot-partition tarball + `.raucb` bundle — all release-pipeline
artifacts), and the platform-specific gear below.

### 1.1 Raspberry Pi CM4 (usbboot / rpiboot)

Gear: carrier/IO board with the `nRPIBOOT` jumper (on the official IO
board: fit the "disable eMMC boot" jumper on J2), USB-C cable, a Linux
host with [usbboot](https://github.com/raspberrypi/usbboot) built.

1. Power off. Fit the nRPIBOOT / eMMC-boot-disable jumper. Connect USB-C
   to the host, power on.
2. Expose the eMMC as USB mass storage:

   ```sh
   sudo ./rpiboot -d mass-storage-gadget64
   lsblk   # eMMC appears as e.g. /dev/sdX (p1 boot, p2 rootfs.A, p3 rootfs.B, p4 data)
   ```

3. Reflash the boot partition (kernel + DTB + U-Boot env) from the
   matching release artifact:

   ```sh
   sudo mount /dev/sdX1 /mnt/cm4-boot
   sudo tar -xf acme-device-v1-rpi-cm4-<ver>-bootfs.tar.gz -C /mnt/cm4-boot
   ```

4. Reset the U-Boot environment counters so slot A is bootable again —
   either restore the shipped default `uboot.env` from the boot tarball,
   or edit in place with `fw_setenv` pointed at the mounted env file
   (16 KiB env, offset 0):

   ```sh
   printf '/mnt/cm4-boot/uboot.env 0x0 0x4000\n' > /tmp/fw_env.config
   sudo fw_setenv -c /tmp/fw_env.config BOOT_ORDER "A B"
   sudo fw_setenv -c /tmp/fw_env.config BOOT_A_LEFT 3
   sudo fw_setenv -c /tmp/fw_env.config BOOT_B_LEFT 3
   sudo umount /mnt/cm4-boot
   ```

5. Write a known-good rootfs image into slot A:

   ```sh
   sudo dd if=acme-device-v1-rpi-cm4-<ver>.rootfs.ext4 of=/dev/sdX2 \
       bs=4M conv=fsync status=progress
   sync
   ```

6. Power off, remove the jumper, boot. Watch the serial console: U-Boot
   picks slot A, `atomic-health` marks it good.
7. Re-sync slot B by installing the current known-good bundle through the
   normal path (server rollout, or locally
   `rauc install /data/atomic-updater/known-good.raucb`).

The device certificate lives on the `data` partition (p4), which is not
touched — **no re-enrollment is needed** and the device reappears in the
dashboard under its old identity on next check-in.

### 1.2 x86-64 mini-PC (USB live stick + efibootmgr + dd)

1. Boot any recent live distro from USB (firmware boot menu). Identify
   the target disk: `lsblk` — p1 EFI system partition, p2 `rootfs.A`,
   p3 `rootfs.B`, p4 `data`.
2. Inspect the EFI boot state:

   ```sh
   efibootmgr -v
   ```

   The RAUC EFI backend (design §6.2) drives two entries, one per slot.
   If they are missing or mangled, recreate them — labels and loader
   paths must match a healthy device's `efibootmgr -v` output (as
   provisioned at manufacture), e.g.:

   ```sh
   efibootmgr --create --disk /dev/sda --part 1 --label "A" --loader '\EFI\A\grubx64.efi'
   efibootmgr --create --disk /dev/sda --part 1 --label "B" --loader '\EFI\B\grubx64.efi'
   efibootmgr --bootorder 0000,0001   # A first — use the numbers just printed
   efibootmgr --delete-bootnext       # clear any stale one-shot BootNext
   ```

3. Write the known-good slot image into slot A:

   ```sh
   sudo dd if=acme-device-v1-x86_64-generic-<ver>.rootfs.ext4 of=/dev/sda2 \
       bs=4M conv=fsync status=progress
   sync
   ```

4. Remove the stick and reboot. GRUB boots slot A; `atomic-health` marks
   it good; then re-sync slot B via a normal bundle install as in §1.1
   step 7.

As with the CM4, `data` (p4) is untouched — identity and enrollment
survive.

---

## 2. Fleet-wide certificate expiry

### Symptoms

- Dashboard summary: `last_seen_at` goes stale across the whole fleet at
  roughly the same time; check-in rate drops to ~0.
- Server log: a wall of TLS handshake failures on `:8443`
  (`tls: bad certificate`, `certificate has expired or is not yet valid`).
- Device journal (`journalctl -u atomic-agent`): `x509: certificate has
  expired` (server cert expired) or handshake rejected by server (device
  cert expired).

Confirm which side expired:

```sh
# Server-side cert as devices see it:
openssl s_client -connect ota.example.com:8443 </dev/null 2>/dev/null \
  | openssl x509 -noout -subject -enddate
# A device cert (on the device):
openssl x509 -in /data/atomic-updater/device.cert.pem -noout -enddate
```

### 2.1 Server TLS certificate expired (1-year validity in dev PKI)

The whole fleet rejects the server. Fix is server-side only, because
devices pin the **root CA** (`keyring.pem`), not the leaf — any new
root-signed leaf is accepted immediately.

1. On the PKI host, move the old cert aside (the script skips existing
   files) and issue a new one with the production SANs:

   ```sh
   mv pki/server/server.cert.pem pki/server/server.cert.pem.expired
   mv pki/server/server.key.pem  pki/server/server.key.pem.expired
   SERVER_CERT_SANS="DNS:ota.example.com,IP:203.0.113.10" \
       scripts/pki/make-server-cert.sh
   ```

2. Deploy `pki/server/server.cert.pem` / `server.key.pem` to the paths
   behind `ATOMIC_TLS_CERT` / `ATOMIC_TLS_KEY` and restart
   `atomic-server`.
3. Devices recover on their next poll (≤ ~70 s) with no device-side
   action. Watch `last_seen_at` come back in the dashboard.

### 2.2 Device certificates expired (2-year validity, issued at enrollment)

The server rejects the handshakes instead. The recovery path is
**re-enrollment**: the agent falls back to its manufacturing certificate
and calls `POST /api/v1/enroll` again. Because the CSR carries the same
device public key, the server treats it as idempotent re-enrollment
(api.md §1.1) — `200`, same `device_id`, fresh 2-year certificate. No
fleet state is lost.

Caveats:

- Re-enrollment only works while the **manufacturing cert** on the device
  is itself valid and non-revoked. If both are expired, the device needs
  physical recovery (new cert material onto the `data` partition).
- Devices whose cert was **revoked** get `403` on re-enroll by design —
  that is not this incident, see api.md §1.1.

### Prevention (risk R6)

Alert at 30/14/7 days before `not_after` of the server cert and of the
oldest device cert. Device cert expiry is visible server-side at
enrollment time + 2 years; track it in monitoring rather than waiting for
the cliff. Phase 2 adds automatic rotation.

---

## 3. Rollout stuck or auto-paused

### Reading the state

```sh
# All rollouts, incl. status + paused_reason:
curl -sb "$COOKIES" $ADMIN/admin/v1/rollouts | jq '.rollouts[]'
# Cohort numbers for one rollout:
curl -sb "$COOKIES" $ADMIN/admin/v1/rollouts/$ID/stats
# → {"offered": 40, "installed_ok": 37, "failed": 1, "fail_rate_1h": 2.5, "paused_reason": null}
```

An auto-paused rollout has `status: "paused"` and a `paused_reason` like
`auto: 3/40 failures in last hour` (api.md §3). Auto-pause is **sticky**:
nothing resumes it except an operator `PATCH`.

### Triage decision tree

1. **`status: paused`, `paused_reason` starts with `auto:`** — a real
   failure spike (> 2%/h over ≥ 25 offered devices). Pull failing
   devices' event timelines:

   ```sh
   curl -sb "$COOKIES" "$ADMIN/admin/v1/devices/$DEV/events?limit=100" \
     | jq '.events[] | select(.event_type == "rollback" or .event_type == "install-fail")'
   ```

   - Failure reasons point at the **bundle** (health probes failing,
     install errors on both hardware types) → **abort**:
     `PATCH /admin/v1/rollouts/{id} {"status":"aborted"}`, deprecate the
     bundle (`PATCH /admin/v1/bundles/{id} {"status":"deprecated"}`),
     fix, and ship a **new version**. Note the no-retry rule: a device
     whose latest `install-fail`/`rollback` references this bundle will
     never be re-offered it anyway (api.md §1.2 step 5) — resuming a bad
     bundle mostly burns devices that haven't tried it yet.
   - Failures were **transient/environmental** (object-store outage,
     upstream network blip — check store and server logs for the same
     window) → verify one device installs cleanly by hand, then
     **resume**: `PATCH {"status":"active"}` (clears `paused_reason`).

2. **`status: active` but nothing is updating** — check `stats.offered`:
   - `offered` ≈ 0 → no devices match: wrong `channel` or
     `hardware_type` on the rollout vs. the fleet, or `percentage` is
     tiny. Fix with `PATCH {"percentage": ...}` or recreate the rollout.
   - `offered` > 0 but installs lag → devices are downloading/waiting for
     their maintenance window (production devices install 02:00–04:00
     local, design §7.5). Confirm via per-device `download` events.
   - Specific devices never get offers → per-device exclusions: already
     on that bundle, semver `<=` current (downgrade guard — needs
     `allow_downgrade` on the rollout), failed this bundle before
     (no-retry), or deterministic hash simply puts them outside the
     current percentage (they join as you ramp).

3. **`status: draft`** — it was never activated:
   `PATCH {"status":"active"}`.

Ramp discipline (design §8.3): 1% → 5% → 25% → 100%, checking
`stats.fail_rate_1h` before each step.

---

## 4. Postgres + MinIO backup / restore

Single-node MVP: one Postgres 15, one MinIO, both colocated with
`atomic-server` (dev stack: `scripts/dev/compose.yaml`). Postgres is the
**security-critical** dataset — it holds device identities and
`revoked_certs`; losing it un-revokes compromised certificates. Treat DB
backups accordingly (encrypt at rest, restrict access).

### Backup (nightly cron on the host)

```sh
# Postgres — custom-format dump (adjust container/name to the deployment;
# dev compose service names shown):
docker compose -f scripts/dev/compose.yaml exec -T postgres \
  pg_dump -U atomic -Fc atomic > /backups/atomic-$(date +%F).dump

# MinIO — mirror the bundle bucket (signed/ prefix is the one that matters;
# staging/ is rebuildable by CI):
mc alias set store http://localhost:9000 "$ACCESS_KEY" "$SECRET_KEY"
mc mirror --overwrite store/bundles /backups/bundles/
```

Keep 30 daily dumps; test-restore monthly.

### Restore (fresh host)

Order matters: **object store first, then Postgres**, so bundle rows never
reference missing objects.

```sh
# 1. Bring up empty Postgres + MinIO (e.g. the dev compose stack).
# 2. Restore bundle objects:
mc mirror --overwrite /backups/bundles/ store/bundles
# 3. Restore the database:
docker compose -f scripts/dev/compose.yaml exec -T postgres \
  pg_restore -U atomic -d atomic --clean --if-exists < /backups/atomic-<date>.dump
# 4. Start atomic-server (migrations are idempotent, run at startup).
# 5. Reconcile store vs. DB — registers any signed object the dump missed:
curl -sb "$COOKIES" -X POST $ADMIN/admin/v1/bundles/scan
```

During the outage devices keep running and simply get no offers; check-ins
buffer nothing on the server side, so recovery is clean — the fleet
reappears within one poll interval.

---

## 5. Enrolling the first device end-to-end

Everything below runs against the dev stack (server on
`https://localhost:8443` for devices, `http://localhost:8080` for admin)
after `make pki` and starting the stack. Payloads match docs/api.md.

```sh
# 0. A manufacturing cert + key (from make pki):
MFG_CERT=pki/manufacturing/mfg-fleet.cert.pem
MFG_KEY=pki/manufacturing/mfg-fleet.key.pem
ROOT=pki/root/ca.cert.pem            # devices' single trust anchor

# 1. Device keypair + CSR (this is what the agent does on first boot):
openssl ecparam -name prime256v1 -genkey -noout -out device.key.pem
openssl req -new -key device.key.pem -subj "/CN=dev-device-1" -out device.csr.pem

# 2. Enroll — authenticated by the MANUFACTURING cert (api.md §1.1):
jq -n --rawfile csr device.csr.pem \
  '{csr_pem: $csr, hardware_type: "x86_64-generic", serial: "SN-0001"}' > enroll.json
curl -sS --cacert $ROOT --cert $MFG_CERT --key $MFG_KEY \
  -H 'Content-Type: application/json' -d @enroll.json \
  https://localhost:8443/api/v1/enroll | tee enroll-resp.json
# → 201 {"device_id": "...", "certificate_pem": "...", "ca_chain_pem": "..."}

jq -r .certificate_pem enroll-resp.json > device.cert.pem

# 3. First check-in — authenticated by the DEVICE cert (api.md §1.2):
curl -sS --cacert $ROOT --cert device.cert.pem --key device.key.pem \
  -H 'Content-Type: application/json' \
  -d '{"slot":"A","version":"1.0.0","bundle_id":null,"boot_count":1,
       "uptime_s":30,"healthy":true,
       "last_update":{"outcome":"none","bundle_id":null,"reason":""}}' \
  https://localhost:8443/api/v1/checkin
# → {"update":null}   (nothing assigned until a rollout is active)

# 4. Verify it in the admin API:
curl -sS -c cookies.txt -X POST http://localhost:8080/admin/v1/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<your password>"}'
curl -sS -b cookies.txt 'http://localhost:8080/admin/v1/devices?q=SN-0001' | jq
```

Notes:

- Repeating step 2 with the **same device key** returns `200` and a fresh
  cert for the same `device_id` (idempotent re-enrollment). A new key =
  a new device row.
- To see an update offered: register a bundle, create a rollout on the
  device's channel/hardware_type, `PATCH` it `active`, and repeat step 3 —
  the response then carries `update.url` (presigned, 15 min) + `sha256`.

---

## 6. Bootloader corruption — what "frozen bootloader" means

Per design §3 and §11.2, the bootloader is **not A/B-updated** — it is
frozen at manufacture, and OTA updates never write to it. Operationally:

- **The A/B safety net does not cover the bootloader.** Runbook 1 works
  because U-Boot/GRUB still runs. A corrupted bootloader flash does not
  boot far enough to fall back — the device is bricked from the fleet's
  point of view and needs bench recovery or RMA.
- **Never ship a bundle that writes to boot media.** RAUC's slot config
  (`system.conf`) only defines the two rootfs slots; keep it that way in
  every release review. On the CM4 keep the eMMC boot partitions
  write-protected (`/sys/block/mmcblk0boot0/force_ro = 1`); nothing in
  the image may touch `/dev/mmcblk0boot*` or the EFI system partition
  contents outside manufacturing.
- **Bench-only bootloader work.** If a bootloader update is ever
  unavoidable (security fix in U-Boot/GRUB), it is a physical-access
  procedure per device, not an OTA: CM4 bootloader EEPROM can be
  reflashed with `rpiboot -d recovery` (the boot ROM is mask ROM, so
  usbboot recovery survives EEPROM corruption); x86 firmware/GRUB-on-ESP
  repair happens from a live stick — and a failed firmware flash there
  usually means RMA.
- **Plan of record**: Phase 2/3 introduces a bootloader update flow with
  a recovery partition (design §15, risk R1). Until then, treat every
  request to "just update U-Boot over the air" as a design change, not an
  ops task.

## Bundle store: `signed/` keys are immutable

A registered bundle's DB row records the sha256 from its `.meta.json` sidecar
at registration time. Overwriting an already-registered `signed/` object with
different bytes poisons the fleet: devices download the new bytes, the offer
still advertises the old hash, and every download fails checksum verification.
Never re-upload a changed bundle under an existing key — bump the version
(new key) instead. Devices report `install-fail` with reason
"bundle object corrupt" after three consecutive checksum mismatches; that
event appearing fleet-wide for one bundle is the signature of this mistake.
