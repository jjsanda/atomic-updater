# NOTE: authored for the org CI runners; NOT built/validated in this workspace.

# meta-atomic — Yocto layer for atomic-updater devices

**Nothing in this layer has been built or validated in this workspace.**
There is no BitBake here; the layer targets the org's CI runners
(design.md §6.5, §14). Every file carries the same notice, and anything a
reviewer must confirm on the first real build is marked `VERIFY-ON-BUILDER`
in-line. Read the "Validate first on a real builder" section before trusting
any of it.

Implements design.md §6.2 (bootloaders), §6.3 (Yocto Scarthgap LTS),
§7.1 (partitions & writable state), §7.2 (U-Boot integration),
§7.3 (RAUC configuration).

## What the layer provides

| Piece | Path | Purpose |
|---|---|---|
| Layer config | `conf/layer.conf` | scarthgap-compatible, priority 10 |
| kas configs | `kas/atomic-qemux86-64.yml`, `kas/atomic-rpi-cm4.yml` | pinned layer stacks + build config per machine |
| System image | `recipes-core/images/atomic-image.bb` | minimal read-only systemd image: rauc, atomic-agent, atomic-health, rofs-overlays; dev access only behind `ATOMIC_DEV_IMAGE=1` |
| Update bundle | `recipes-core/bundles/atomic-bundle.bb` | verity-format RAUC bundle, compatible `acme-device-v1`, rootfs slot from atomic-image |
| Health service | `recipes-core/atomic-health/atomic-health_1.0.bb` | builds `device/health` C sources via externalsrc; installs `/usr/lib/atomic-updater/atomic-health` + probes + unit |
| Check-in agent | `recipes-core/atomic-agent/atomic-agent_1.0.bb` | builds `device/agent` Go sources (CGO off) via externalsrc; installs `/usr/bin/atomic-agent` + unit |
| Writable state | `recipes-core/rofs-overlays/` | `/data` mount, first-boot tmpfiles seeding, `/etc` overlay, `/var` + `/home` binds, rpi `/boot/firmware` mount |
| RAUC config | `recipes-rauc/rauc/` | machine-specific `system.conf` (§7.3) + keyring install |
| U-Boot | `recipes-bsp/u-boot/` | RAUC bootmeth Kconfig fragment, initial env (BOOT_ORDER/BOOT_x_LEFT), deployed `uboot.env`, `fw_env.config` via libubootenv bbappend |
| Disk layout | `wic/atomic-rpi.wks.in`, `wic/atomic-x86-efi.wks.in` | 4-partition scheme: 128M FAT `boot`, 2G `rootfs.A`, 2G `rootfs.B`, `data` |

## Machine names (documented decision)

* **x86:** `qemux86-64` (CI + integration tests; a real mini-PC gets its own
  machine later — the recipes use the generic `x86-64` override so they carry
  over).
* **CM4:** upstream meta-raspberrypi on scarthgap has **no**
  `raspberrypi-cm4`/`rpi-cm4` machine. The Compute Module 4 (BCM2711) is
  covered by **`raspberrypi4-64`**, which is what `kas/atomic-rpi-cm4.yml`
  sets. The fleet API string `rpi-cm4` (docs/api.md) is an application-level
  identifier, unrelated to the Yocto machine name.

## Building with kas (on a real builder)

From a **git checkout** of the monorepo (kas resolves the url-less repo entry
via git — an unpacked tarball will not work):

```sh
kas build device/meta-atomic/kas/atomic-qemux86-64.yml
kas build device/meta-atomic/kas/atomic-rpi-cm4.yml

# or, as documented for the layer itself:
cd device/meta-atomic && kas build kas/atomic-qemux86-64.yml
```

Development image (ssh, dev user, debug-tweaks — never for the field):

```sh
ATOMIC_DEV_IMAGE=1 kas build device/meta-atomic/kas/atomic-qemux86-64.yml
```

Bundle signing (design.md §9.2): CI exports absolute paths before invoking
kas; the kas `env:` block passes them through to BitBake:

```sh
export RAUC_KEY_FILE=/ci/secrets/dev-signing.key.pem
export RAUC_CERT_FILE=/ci/secrets/dev-signing.cert.pem
export RAUC_KEYRING_FILE=/ci/secrets/ca.cert.pem
kas build device/meta-atomic/kas/atomic-rpi-cm4.yml
```

CI-built bundles are signed with the **dev** key and land in the `staging/`
prefix; the isolated `signer` runner re-signs (`rauc resign`) for the
`signed/` prefix — the release key never touches these builders.

Outputs land in `build/tmp/deploy/images/<machine>/`: `atomic-image-*.wic`
(flashable disk), `atomic-image-*.ext4` (slot image) and
`bundles/atomic-bundle-*.raucb`.

**Pinning:** the kas files intentionally name branches (`scarthgap`), not
commit SHAs — no SHA could be verified from this workspace. The first real
build must pin every repo (`kas dump --lock`, or `commit:` per repo) and
commit the result.

## Validate first on a real builder (ordered)

1. **Parse:** `kas shell ... -c "bitbake -p"`; then
   `bitbake-layers show-layers` / `show-recipes atomic-*`. Confirms
   layer.conf, `LAYERDEPENDS` collection name of meta-rauc ("rauc"), and all
   recipe syntax.
2. **meta-rauc-community branch and layer names** (`meta-rauc-qemux86`,
   `meta-rauc-raspberrypi`): confirm the scarthgap branch exists; check
   `bitbake-layers show-appends rauc u-boot` for conflicts between the
   community appends and ours — drop the community layers from the kas files
   if they fight (our system.conf must win; meta-atomic priority 10).
3. **U-Boot version:** `CONFIG_BOOTMETH_RAUC` needs U-Boot ≥ v2025.01;
   scarthgap defaults to 2024.01. Set `PREFERRED_VERSION_u-boot` (kas rpi
   file has the placeholder) or carry patches, then confirm the .cfg
   fragment is merged (`bitbake u-boot -c configure` + grep .config) and
   the deployed `uboot.env` CRC-checks (`fw_printenv -c` against it).
4. **externalsrc recipes:** `bitbake atomic-health atomic-agent` — the
   relative `EXTERNALSRC` paths (`device/health`, `device/agent`), the
   health unit filename, the agent's `go.mod` module path and
   `./cmd/atomic-agent` main package, vendoring of any non-stdlib deps.
5. **Image + QA:** `bitbake atomic-image`; read-only-rootfs QA, the
   `/data` and `/boot/firmware` mountpoints exist, `ATOMIC_DEV_IMAGE=1`
   variant adds ssh/dev user and the default variant does **not**.
6. **wic layout:** `wic ls` on both .wic images — partition numbering
   p1..p4, GPT part-names on x86 resolve as
   `/dev/disk/by-partlabel/rootfs.A|B|data`, MBR numbering matches
   `/dev/mmcblk0p2|p3|p4` on the CM4.
7. **First boot (QEMU + OVMF for x86):** rofs-overlays ordering
   (`systemctl list-dependencies local-fs.target`), tmpfiles seeding of
   `/data` on first boot, overlay on `/etc`, binds on `/var`/`/home`,
   interplay with volatile-binds.
8. **RAUC end-to-end:** `rauc info` on a signed bundle, `rauc status` on the
   booted system, keyring present at `/etc/rauc/ca.cert.pem` (the bbappend
   warns if `RAUC_KEYRING_FILE` was unset at build time). On x86 create the
   EFI boot entries labeled `A`/`B` once (`efibootmgr -c -L A ...`), then
   verify slot switch; on the CM4 verify `BOOT_ORDER`/`BOOT_x_LEFT`
   handling through a forced failed boot (design.md §10.2).
9. **Kernel-per-slot integration point (open):** with the upstream RAUC
   bootmeth the kernel must be loaded from the *selected slot*
   (atomic-image installs `kernel-image`/`kernel-devicetree` into the rootfs
   on rpi). Wiring the in-slot boot flow (extlinux.conf or bootmeth default)
   is Milestone-1 bring-up work — the design table §7.1 ("kernel per slot on
   the FAT partition") cannot be updated atomically by a rootfs-only bundle
   and was deliberately not implemented that way.
