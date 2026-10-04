# CI runner classes

The pipeline (`.gitlab-ci.yml` + `ci/*.yml`) targets three classes of
GitLab runners on org infrastructure. Nothing in `ci/` is expected to run
in a development workspace.

| Class | Tag(s) | Executor | Used by |
|---|---|---|---|
| docker | `docker` (+ `yocto-cache` subset) | docker | `lint:*`, `unit:*`, `device:bundle`, `publish:*` |
| dind | `dind` | docker, privileged | `server:image`, `integration:*` |
| signer | `signer` | shell | `sign:dev`, `sign:stable` |

## `docker` class

Plain docker-executor runners for lint, unit tests and publish jobs.

```toml
[[runners]]
  name = "atomic-docker-N"
  executor = "docker"
  [runners.docker]
    image = "alpine:3.20"
    privileged = false
    pull_policy = ["if-not-present"]
```

### `yocto-cache` subset

`device:bundle` (Yocto/kas builds) additionally requires the tag
`yocto-cache`: large builders (≥ 16 vCPU, ≥ 32 GB RAM, ≥ 500 GB NVMe
scratch) whose host maintains an S3-backed mount at `/mnt/yocto-cache`
(s3fs or rclone mount against `YOCTO_CACHE_S3_ENDPOINT` /
`YOCTO_CACHE_S3_BUCKET`, see `ci/shared/sstate-cache.yml`) and passes it
into every job container:

```toml
[[runners]]
  name = "atomic-yocto-N"
  executor = "docker"
  # tags: docker, yocto-cache
  [runners.docker]
    image = "ghcr.io/siemens/kas/kas:4.4"
    privileged = false
    volumes = ["/mnt/yocto-cache:/mnt/yocto-cache:rw"]
```

The job sets `SSTATE_DIR`/`DL_DIR` beneath that mount; warm-cache builds
stay under ~15 min (design.md §6.5). The cache bucket needs a lifecycle
rule (expire objects untouched for 90 days) so it does not grow without
bound.

## `dind` class

Privileged docker-executor runners providing the `docker:dind` service for
buildx (`server:image`) and the compose-based OTA e2e suite
(`integration:ota-e2e`).

```toml
[[runners]]
  name = "atomic-dind-N"
  executor = "docker"
  [runners.docker]
    image = "docker:27"
    privileged = true          # required for the dind service
    volumes = ["/certs/client"]
```

Notes:

- Jobs here set `DOCKER_HOST=tcp://docker:2375` and
  `DOCKER_TLS_CERTDIR=""` (in-network dind, no TLS).
- If the org network uses a non-1500 MTU, set `dockerd` MTU inside the
  dind service (`command: ["--mtu=1400"]`) or compose networking will
  stall on large layer pushes.
- Once the Yocto runner bring-up finishes, `integration:qemu-system` also
  runs here and needs KVM: add `devices = ["/dev/kvm"]`.

## `signer` class

Exactly one runner, on a hardened VM. This is the **only** place release
bundle-signing key material exists (design.md §9.2); treat the host as
security-critical infrastructure.

Required host configuration:

- shell executor, dedicated non-admin `gitlab-runner` user
- `rauc` (≥ 1.11), `openssl`, `jq`, `mc`, `curl` installed
- key material at `/etc/atomic-signing/` — `release.cert.pem`,
  `release.key.pem`, `release-keyring.pem`; mode `0640`,
  group-readable by the runner user only
- runner registered with `--locked` (this project only),
  `run_untagged = false`, and **protected** — it only picks up jobs from
  protected refs (`main` + `v*` tags)
- no docker daemon, no inbound network except GitLab; outbound only to
  GitLab and the bundle store
- auditd rule logging every read of `/etc/atomic-signing/*`

```toml
[[runners]]
  name = "atomic-signer"
  executor = "shell"
  # tags: signer ; locked to project; protected refs only
```

Key rotation procedure (90-day bundle cert): docs/security.md §5.

## Project CI/CD variables

Set in GitLab project settings (all protected; secrets masked):

| Variable | Used by | Meaning |
|---|---|---|
| `BUNDLE_STORE_URL` | build, sign | S3 endpoint URL of the bundle store |
| `BUNDLE_STORE_ACCESS_KEY` | build, sign | store credentials (masked) |
| `BUNDLE_STORE_SECRET_KEY` | build, sign | store credentials (masked) |
| `BUNDLE_STORE_BUCKET` | build, sign | bucket name (`bundles` in dev) |
| `ATOMIC_ADMIN_URL` | publish | base URL of the admin listener |
| `ATOMIC_ADMIN_USER` | publish | operator username |
| `ATOMIC_ADMIN_PASSWORD` | publish | operator password (masked) |

The CI store credentials should be scoped: write-only to `staging/` for
the build jobs' key, read `staging/` + write `signed/` for the signer's
key. The publish credentials are an admin-API login, not store
credentials.
