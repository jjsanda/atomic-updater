# rauc-tools: RAUC built from source with -Dservice=false so the CLI works
# standalone (no D-Bus daemon) inside containers. Used for bundle creation/
# signing and as the base of the e2e device simulator.
#
# Version note: 1.15.2 rather than the Ubuntu-packaged 1.11.3 because kernels
# ≥ 6.x append fields to the dm-verity status line and 1.11–1.14 parse it
# with an exact string match (fails "Unexpected dm-verity status 'V -'").
# Target devices get their RAUC from meta-rauc (scarthgap) — see
# device/meta-atomic/.
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential meson ninja-build pkg-config \
        libglib2.0-dev libssl-dev libcurl4-openssl-dev libnl-genl-3-dev \
        squashfs-tools e2fsprogs openssl curl ca-certificates xz-utils \
        cryptsetup-bin dmsetup kmod uuid-runtime jq iproute2 \
    && rm -rf /var/lib/apt/lists/*

ARG RAUC_VERSION=1.15.2
RUN curl -fsSL -o /tmp/rauc.tar.xz \
        "https://github.com/rauc/rauc/releases/download/v${RAUC_VERSION}/rauc-${RAUC_VERSION}.tar.xz" \
    && mkdir -p /tmp/rauc-src \
    && tar -xJf /tmp/rauc.tar.xz -C /tmp/rauc-src --strip-components=1 \
    && cd /tmp/rauc-src \
    && meson setup build -Dservice=false -Dnetwork=false -Dstreaming=false \
         -Djson=disabled -Dgpt=disabled \
    && ninja -C build \
    && ninja -C build install \
    && ldconfig \
    && rauc --version \
    && rm -rf /tmp/rauc.tar.xz /tmp/rauc-src

COPY make-rootfs.sh make-bundle.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/make-rootfs.sh /usr/local/bin/make-bundle.sh

WORKDIR /work
