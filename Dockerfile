FROM ubuntu:24.04

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    e2fsprogs \
    xfsprogs \
    btrfs-progs \
    util-linux \
    && rm -rf /var/lib/apt/lists/*

COPY lxd-csi /bin/lxd-csi
ENTRYPOINT ["/bin/lxd-csi"]
