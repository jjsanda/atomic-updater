# atomic-updater — Security

How the threat model from [design.md §11](design.md) is actually
implemented, what the PKI looks like on disk, and the runbooks for keys,
rotation and revocation. Wire-level details follow
[docs/api.md](api.md).

Contents: [1. Threats → mechanisms](#1-threat-model--implemented-mechanisms) ·
[2. mTLS listener](#2-mtls-listener-in-detail) ·
[3. Downgrade & retry guards](#3-downgrade-guard-and-offerno-retry-semantics) ·
[4. PKI on disk](#4-pki-structure-as-implemented-scriptspki) ·
[5. Key ceremony](#5-key-ceremony-runbook-production) ·
[6. Revocation / CRL](#6-revocation--crl-procedure) ·
[7. Documented deviations](#7-documented-deviations-from-the-design-doc)

---

## 1. Threat model → implemented mechanisms

| Threat (design §11.1) | Implemented mechanism |
|---|---|
| Network attacker forges an update | Device listener is mTLS-only (§2); bundles carry a CMS signature verified by RAUC against the root-CA keyring baked into the image — the server is never trusted for bundle content, only for pointing at it |
| Compromised CI runner ships a bad bundle | Build stage signs with an **ephemeral per-pipeline dev key** that production devices do not trust; only the isolated `signer` runner holds the release cert and re-signs (`rauc resign`) after verifying staged bytes + dev signature (ci/sign-and-publish.yml, §7 below) |
| Rollback attack (forced downgrade) | Semver guard on **both** sides: the server never offers `version <= current` unless the rollout has `allow_downgrade` (api.md §1.2 step 4), and the agent independently rejects such offers (§3) |
| Compromised device certificate | Per-request fingerprint check against `revoked_certs` → `403` (§2); operator revocation via `POST /admin/v1/devices/{id}/revoke`; CRL artifact for external consumers (§6) |
| MITM on bundle download | Presigned URL is TLS-only with a 15-minute TTL; the offer carries the expected `sha256`, and the bundle signature is verified by RAUC before any partition write regardless of transport |

Out of scope for MVP (unchanged from design §11.2): physical attackers,
signing-runner side channels, bootloader compromise — the operational
consequences of the frozen bootloader are in
[operations.md §6](operations.md#6-bootloader-corruption--what-frozen-bootloader-means).

## 2. mTLS listener in detail

The device listener (`:8443`) is configured with
`ClientAuth = RequireAndVerifyClientCert` and
`ClientCAs = {device-issuing CA, manufacturing CA}` — nothing without a
client cert chaining to one of those two CAs completes a handshake
(api.md §1).

Per request, middleware computes the **SHA-256 fingerprint of the leaf
certificate DER** (lowercase hex, no colons) and:

1. Rejects `403` if the fingerprint appears in `revoked_certs` — checked
   on **every** request, so revocation takes effect at the next poll, not
   at the next handshake or CRL refresh.
2. Classifies by issuer:
   - leaf issued by the **manufacturing CA** → may call
     `POST /api/v1/enroll` and nothing else;
   - leaf issued by the **device CA** *and* the fingerprint matches a
     `devices` row → may call `/api/v1/checkin` and `/api/v1/events`;
   - anything else → `403`.
3. Retired devices (`status = 'retired'`) get `410 Gone` — a retired
   device cannot silently rejoin the fleet without an operator flipping
   its status back.

The fingerprint→device mapping is the device identity; there is no
bearer-token or password path on the device API. Enrollment issues the
device cert (CN = device UUID, 2 years) from the **online device CA**
whose key the server holds — deliberately separate from the offline
bundle-signing hierarchy, so a server compromise cannot sign bundles
(design R3): it could only mint device identities, which does not let an
attacker feed code to devices.

## 3. Downgrade guard and offer/no-retry semantics

Server side, an active rollout results in an offer only if all of
(api.md §1.2):

1. newest `active` rollout matches the device's channel + hardware_type;
2. `fnv1a32(deviceID + "|" + rolloutID) % 100 < percentage` —
   deterministic, so ramping never flips a device out of the cohort;
3. bundle is not deprecated and differs from `current_bundle_id`;
4. **downgrade guard**: `bundle.version > device.current_version`
   (semver), unless the rollout explicitly sets `allow_downgrade` —
   which is an operator decision recorded on the rollout row;
5. **no-retry**: the device's most recent `install-fail`/`rollback` event
   must not reference this bundle — a failed bundle is never re-offered
   to that device automatically. Recovery from a bad release is always a
   *new version*, never a silent retry (this also stops rollback loops
   from hammering the fleet).

The agent enforces (4) independently: an offer whose `version` is `<=`
the running version is rejected client-side unless the offer carries
`allow_downgrade: true`. A compromised or misconfigured server therefore
cannot roll devices back by itself — it would additionally need a validly
signed old bundle *and* the device to accept the downgrade.

`offer` events are recorded at most once per device/rollout/hour, which
keeps the auto-pause denominator honest (api.md §3): fail-rate > 2%/h
over a cohort of ≥ 25 offered devices pauses the rollout stickily.

## 4. PKI structure as implemented (`scripts/pki/`)

`scripts/pki/bootstrap-dev-pki.sh` builds the full **development**
hierarchy under `./pki/` (all keys EC P-256, openssl 3.0):

```text
Root CA                                   pki/root/            10 y  self-signed
├── Signing CA        [prod: OFFLINE]     pki/signing/          2 y
│   └── Bundle signing cert               pki/bundle/          90 d  per release
├── Device-issuing CA [ONLINE, on server] pki/device-ca/        5 y
│   └── Device certs (issued at enroll)   — server-side —       2 y  CN = device UUID
├── Manufacturing CA                      pki/manufacturing/    5 y
│   └── Fleet manufacturing cert (shared) pki/manufacturing/    1 y  clientAuth
└── Server TLS cert (device listener)     pki/server/           1 y  serverAuth

Outside the hierarchy (negative tests):   pki/badkey/          self-signed
```

File map (see `scripts/pki/lib.sh` header for the authoritative list):

| Path | Contents | Consumer |
|---|---|---|
| `pki/root/ca.{cert,key}.pem` | root CA | trust anchor for everything |
| `pki/signing/ca.{cert,key}.pem` | bundle-signing CA | signs bundle certs |
| `pki/bundle/bundle.{cert,key}.pem` | 90-day bundle cert | `rauc bundle`/`resign` |
| `pki/device-ca/ca.{cert,key}.pem` | online device CA | `ATOMIC_DEVICE_CA_CERT/KEY` |
| `pki/manufacturing/ca.cert.pem` | manufacturing CA | `ATOMIC_MFG_CA_CERT` |
| `pki/manufacturing/mfg-fleet.{cert,key}.pem` | shared factory cert | device first boot |
| `pki/server/server.{cert,key}.pem` | device-listener TLS | `ATOMIC_TLS_CERT/KEY` |
| `pki/keyring.pem` | root cert copy | RAUC keyring + agent server pin |
| `pki/ca-chain.pem` | device CA + root | `ATOMIC_CA_CHAIN` (returned on enroll) |
| `pki/badkey/bad.{cert,key}.pem` | untrusted signer | e2e negative tests |
| `pki/crl/device-ca.crl.pem` | CRL artifact | external consumers (§6) |

Devices pin exactly one trust anchor — the root (`keyring.pem`) — for
both bundle verification and the server TLS connection. The signing CA is
an intermediate so it can be rotated without re-provisioning devices
(design §9.1); the same holds for the device CA and manufacturing CA.

The bootstrap script also self-checks: every chain must verify against
the root, and the `badkey` cert must **fail** verification, otherwise the
script aborts — a dev PKI in which the negative-test key verifies would
make the e2e "reject untrusted signature" tests meaningless.

## 5. Key ceremony runbook (production)

MVP key handling per design §9.2: the root CA and signing CA private keys
exist **only** inside a sealed KeePassXC database on a dedicated,
never-networked laptop. Custody: named owner + named deputy (design open
question Q5); the KeePassXC passphrase is split between them (owner knows
part A, deputy part B), so every ceremony takes two people.

**Every ceremony**: two people present, laptop stays offline, material
moves on a dedicated USB stick that is wiped afterwards
(public material only — private keys never leave the laptop), and an
entry is appended to the ceremony log (date, attendees, commands run,
fingerprints of artifacts produced).

### 5.1 One-time: create the hierarchy

On the ceremony laptop, run the production equivalents of
`make-root-ca.sh` (10-year root) and `make-signing-ca.sh` (2-year signing
CA), with keys generated into the KeePassXC-managed store. Export **only**
the certificates: the root cert goes into the Yocto layer (device keyring)
and `release-keyring.pem` on the signer runner; the signing CA cert is
needed for chain assembly at bundle-cert issuance.

### 5.2 Quarterly: signing-CA verification

Calendar-driven, every quarter (design §9.2 "quarterly re-signing"):

1. Open the KeePassXC DB (two-person rule), verify DB integrity and
   last-modified time against the ceremony log.
2. `openssl verify -CAfile root/ca.cert.pem signing/ca.cert.pem` and
   check `notAfter`: if less than ~6 months remain, issue the successor
   signing CA now, sign it with the root, and plan the rollover — old and
   new signing CAs are both root-signed, so devices need no change.
3. Log the check.

### 5.3 Every ≤ 90 days: bundle-cert rotation

The bundle signing cert deliberately expires after 90 days, so this is
routine, not exceptional:

1. On the **signer runner**, generate a fresh keypair + CSR (the bundle
   key never exists on the laptop):

   ```sh
   openssl ecparam -name prime256v1 -genkey -noout -out release.key.pem.new
   openssl req -new -key release.key.pem.new \
     -subj "/O=Acme Devices/CN=Acme Bundle Signer $(date -u +%Y-%m)" -out bundle.csr.pem
   ```

2. Carry the CSR to the ceremony laptop; sign it with the signing CA for
   90 days with `keyUsage=digitalSignature` — i.e. the production
   equivalent of `scripts/pki/make-bundle-cert.sh` (which does exactly
   this against the dev CAs).
3. Carry the new cert back; install cert + key as
   `/etc/atomic-signing/release.{cert,key}.pem` on the signer runner
   (see `ci/shared/runners.md`).
4. Re-run the `sign` stage of the current release pipeline (`sign:dev`
   auto on `main`, `sign:stable` manual on the tag) so the next published
   bundles carry the new signature. Bundles already in `signed/` remain
   valid — their certs were valid at signing time and chain to the same
   root — so nothing needs re-publishing.
5. Log the rotation; record the new cert's SHA-256 fingerprint (it will
   appear as `signed_by` in bundle metadata).

If a bundle cert (or the signer runner) is **compromised**: revoke by
removing it from the signer, rotate immediately per above, deprecate all
bundles whose `signed_by` matches the compromised fingerprint
(`PATCH /admin/v1/bundles/{id} {"status":"deprecated"}`), and ship a new
release. Device keyrings trust the root, not the leaf, so there is no
device-side action — which is also why leaked bundle keys matter until
their 90-day expiry and why deprecation + short validity is the actual
containment.

## 6. Revocation / CRL procedure

Two mechanisms, one authoritative:

1. **Authoritative — server DB check.** `POST
   /admin/v1/devices/{id}/revoke` inserts the device cert fingerprint
   into `revoked_certs`; every subsequent request from that cert is
   `403`, effective within one poll interval. This is the mechanism the
   fleet actually depends on (design §11.1 "server-side CRL check on
   every request").
2. **Secondary — standard CRL artifact.** For external consumers that
   want X.509 semantics (corporate TLS inspection, partner systems),
   `scripts/pki/gen-crl.sh` synthesizes an openssl CA database from
   `pki/device-ca/revoked.csv` (lines: `<hex_serial>,<subject CN>`) and
   emits `pki/crl/device-ca.crl.pem` (30-day validity).

Revocation procedure:

```sh
# 1. Authoritative: block the device at the API
curl -sb "$COOKIES" -X POST $ADMIN/admin/v1/devices/$ID/revoke \
  -H 'Content-Type: application/json' -d '{"reason":"lost device"}'
# 2. Record for the CRL artifact (serial as printed by `openssl x509 -serial`)
echo "4F3A...,<device-uuid>" >> pki/device-ca/revoked.csv
scripts/pki/gen-crl.sh
# 3. Distribute pki/crl/device-ca.crl.pem to whatever consumes it
```

Keep `revoked.csv` in sync with `revoked_certs` (step 2 whenever you do
step 1). If they ever diverge, the DB wins — the CRL is best-effort and
nothing in atomic-updater itself consumes it. Note that restoring an old
Postgres backup shrinks `revoked_certs`; re-apply recent revocations
after any restore ([operations.md §4](operations.md#4-postgres--minio-backup--restore)).

## 7. Documented deviations from the design doc

Differences between design.md and what is actually built — deliberate,
with rationale:

1. **"Reject unsigned bundle" is implemented as "reject untrusted
   signature".** RAUC cannot emit unsigned bundles at all (mandatory
   signing is why we chose it, design §6.1), so the design-doc test
   `TestRejectUnsignedBundle` and success criterion "any tampered or
   unsigned bundle rejected" are exercised with a bundle signed by
   `pki/badkey/` — a key outside the org hierarchy. Verification failure
   path and timing are identical; the label is just honest about what
   RAUC allows us to produce.
2. **CI produces a *dev-signed* bundle, not an unsigned one** (adaptation
   of design §9.2). The build stage signs with an ephemeral per-pipeline
   dev key; the signer runner verifies the staged bytes (sha256 manifest)
   and the dev signature, then `rauc resign`s with the release cert. The
   trust boundary is unchanged — release keys exist only on the signer
   runner, and production devices trust only the production root, so
   staging bundles are inert in the field.
3. **The dev PKI is NOT air-gapped and must never leave development.**
   `scripts/pki/` writes plaintext private keys to the working tree
   (git-ignored `pki/`) for `make bundles`/`make e2e` convenience. It is
   a *shape-compatible* stand-in for the production hierarchy, nothing
   more: never flash a dev keyring onto field hardware, never point a
   production server at dev CA material, and never check `pki/` in. The
   production root/signing keys follow §5 and exist only on the ceremony
   laptop; the production device CA key only on the server host; the
   release bundle key only on the signer runner.
4. **The device-issuing CA is online by design** (§11.3): it must sign
   enrollment CSRs interactively. Compromise of the server therefore
   compromises device *identity issuance* but never *bundle signing* —
   the hierarchies share only the offline root.
