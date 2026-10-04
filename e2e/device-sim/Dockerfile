# Device simulator image. Build context is the REPO ROOT; the rauc-tools
# image must exist first:
#   docker build -t atomic-updater/rauc-tools:dev bundle/
#   docker build -t atomic-updater/device-sim:dev -f e2e/device-sim/Dockerfile .

FROM golang:1.22 AS agentbuild
WORKDIR /src
COPY device/agent/go.mod device/agent/go.sum ./
RUN go mod download
COPY device/agent/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/atomic-agent ./cmd/atomic-agent

FROM atomic-updater/rauc-tools:dev

# Real atomic-health, compiled from the same sources the Yocto recipe uses.
COPY device/health/ /tmp/health/
RUN make -C /tmp/health \
    && install -m 0755 /tmp/health/atomic-health /usr/bin/atomic-health \
    && rm -rf /tmp/health

COPY --from=agentbuild /out/atomic-agent /usr/bin/atomic-agent

COPY e2e/device-sim/system.conf /etc/rauc/system.conf
COPY e2e/device-sim/sim-env.sh \
     e2e/device-sim/boot-sim.sh \
     e2e/device-sim/rauc-install-fallback.sh /usr/lib/atomic-updater/
COPY e2e/device-sim/custom-backend.sh \
     e2e/device-sim/reboot-sim \
     e2e/device-sim/fw_setenv \
     e2e/device-sim/fw_printenv \
     e2e/device-sim/supervisor.sh /usr/bin/
RUN chmod +x /usr/bin/custom-backend.sh /usr/bin/reboot-sim /usr/bin/fw_setenv \
             /usr/bin/fw_printenv /usr/bin/supervisor.sh \
             /usr/lib/atomic-updater/boot-sim.sh \
             /usr/lib/atomic-updater/rauc-install-fallback.sh

# Baseline agent configuration (per-boot values come from boot-sim.sh; the
# harness overrides the rest per container as needed).
ENV ATOMIC_AGENT_SERVER_URL=https://atomic-server:8443 \
    ATOMIC_AGENT_KEYRING=/etc/rauc/ca.cert.pem \
    ATOMIC_AGENT_HARDWARE_TYPE=x86_64-generic \
    ATOMIC_AGENT_MFG_CERT_FILE=/etc/atomic-updater/mfg.cert.pem \
    ATOMIC_AGENT_MFG_KEY_FILE=/etc/atomic-updater/mfg.key.pem \
    ATOMIC_AGENT_POLL_INTERVAL=2s \
    ATOMIC_AGENT_POLL_JITTER=1s \
    ATOMIC_AGENT_DOWNLOAD_STALL_TIMEOUT=5s \
    ATOMIC_AGENT_INSTALL_RETRY_DELAY=3s \
    ATOMIC_AGENT_INSTALL_RETRIES=6

ENTRYPOINT ["/usr/bin/supervisor.sh"]
