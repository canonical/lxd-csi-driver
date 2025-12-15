#!/bin/bash
#
# The script creates and configures a LXD cluster with the specified number of members.
# It creates a bridge network named "br0", that can be used to interconnect instances
# running within the cluster. It also creates a storage pool named "default" that can
# be used for volume provisioning.
#
set -euxo pipefail

#================================================
# Variables
#================================================

# Cluster name and size.
CLUSTER_NAME="${CLUSTER_NAME:-cls}"
CLUSTER_SIZE="${CLUSTER_SIZE:-3}"

# Image to use for cluster instances.
INSTANCE_IMAGE="${INSTANCE_IMAGE:-ubuntu-minimal-daily:24.04}"

# Type of cluster instances (container or virtual-machine).
INSTANCE_TYPE="${INSTANCE_TYPE:-container}"

# Version of LXD to install.
VERSION_LXD="${VERSION_LXD:-latest/edge}"

# Other.
INSTANCE="${CLUSTER_NAME}"
LEADER="${CLUSTER_NAME}-1"
STORAGE_POOL="${CLUSTER_NAME}-pool"
STORAGE_DRIVER="${STORAGE_DRIVER:-dir}"
NETWORK_NAME="${CLUSTER_NAME}br0"

# Source bin/helpers from canonical/lxd-ci repository.
# shellcheck source=/dev/null
source <(
  curl -fsSL https://raw.githubusercontent.com/canonical/lxd-ci/refs/heads/main/bin/helpers \
  || { echo "Error: Failed to source bin/helpers from canonical/lxd-ci" >&2; exit 1; }
)

#================================================
# Utils
#================================================

# instanceIPv4 returns the IPv4 address of the instance with the given name.
instanceIPv4() {
    local instance="$1"
    local ipv4

    # Try to get any IPv4 address from preconfigured bridge br0.
    ipv4="$(lxc query "/1.0/instances/${instance}/state" | jq --arg ifname "br0" -r '.network[$ifname].addresses[] | select(.family=="inet" and .scope=="global") | .address')"
    if [ "${ipv4}" != "" ]; then
        echo "${ipv4}"
        return 0
    fi


    # Try for enp5s0 (VM) and eth0 (container) interfaces.
    ipv4=$(lxc list "${instance}" -f csv -c 4 | grep -oP "(\d{1,3}\.){3}\d{1,3}(?= \((eth0|enp5s0)\))" || true)
    if [ "${ipv4}" != "" ]; then
        echo "${ipv4}"
        return 0
    fi

    echo "Error: Failed to retrieve IPv4 address for instance ${instance}" >&2
    return 1
}

#========================
# Cluster setup
#========================

# deploy deploys instances required for a LXD cluster.
deploy() {
    local instance

    if ! lxc network show "${NETWORK_NAME}" &>/dev/null; then
        echo "Creating network ${NETWORK_NAME} ..."
        lxc network create "${NETWORK_NAME}" ipv4.address=172.16.20.1/24 ipv4.nat=true
    fi

    # Create storage pool.
    echo "Creating storage pool ${STORAGE_POOL} ..."
    if ! lxc storage show "${STORAGE_POOL}" &>/dev/null; then
        if [ "${STORAGE_DRIVER}" = "zfs" ]; then
            lxc storage create "${STORAGE_POOL}" zfs volume.zfs.delegate=true

            # XXX: Ensure that the ZFS device is accessible within the nested container.
            #      Otherwise, the LXD storage pool creation will fail.
            local zfsPerm
            zfsPerm=$(stat -c '%a' /dev/zfs)

            if [ $((zfsPerm & 7)) -eq 0 ]; then
                # It's udev's job to fix the perms but the rule for it ships in
                # the zfsutils-linux package which might not be installed.
                sudo chmod 0660 /dev/zfs
            fi
        else
            lxc storage create "${STORAGE_POOL}" "${STORAGE_DRIVER}"
        fi
    fi

    # Create container profile capable of running VMs.
    if ! lxc profile show container-kvm &>/dev/null; then
        lxc profile create container-kvm << EOF
description: Makes containers capable of running VMs
config:
  linux.kernel_modules: kvm,vhost_net,vhost_vsock
  security.devlxd.images: "true"
  security.nesting: "true"
devices:
  kvm:
    source: /dev/kvm
    type: unix-char
  vhost-net:
    source: /dev/vhost-net
    type: unix-char
  vhost-vsock:
    source: /dev/vhost-vsock
    type: unix-char
  vsock:
    mode: "0666"
    source: /dev/vsock
    type: unix-char
EOF
    fi

    # Setup cluster instances.
    for i in $(seq 1 "${CLUSTER_SIZE}"); do
        instance="${INSTANCE}-${i}"

        local state
        state=$(lxc list --format csv --columns s "${instance}")

        case "${state}" in
        "RUNNING")
            echo "Instance ${instance} already running."
            continue
            ;;
        "STOPPED")
            echo "Starting instance ${instance}..."
            lxc start "${instance}"
            continue
            ;;
        esac

        local args=(--profile container-kvm)
        local ifName="eth0"
        if [ "${INSTANCE_TYPE}" = "virtual-machine" ]; then
            args=(--vm)
            ifName="enp5s0"
        fi

        echo "Creating instance ${instance} ..."

        lxc init "${INSTANCE_IMAGE}" "${instance}" \
            --storage "${STORAGE_POOL}" \
            --network "${NETWORK_NAME}" \
            --config limits.cpu=4 \
            --config limits.memory=4GiB \
            --config security.devlxd.images="true" \
            "${args[@]}"

        # Apply network bridge configuration.
        lxc config set "${instance}" cloud-init.network-config - <<EOF
version: 2
ethernets:
  ${ifName}:
    dhcp4: false
bridges:
  br0:
    interfaces: [${ifName}]
    dhcp4: true
EOF

        lxc start "${instance}"

        if [ "${STORAGE_DRIVER}" = "zfs" ] && [ "${INSTANCE_TYPE}" = "container" ]; then
            # Attach additional disk to each container to allow creating delegated ZFS storage pool
            # within the cluster.
            local disk="${instance}-disk"
            lxc storage volume create "${STORAGE_POOL}" "${disk}"
            lxc config device add "${instance}" "${disk}" disk pool="${STORAGE_POOL}" source="${disk}" path=/mnt/disk
        fi
    done

    # Wait for instances to become ready.
    for i in $(seq 1 "${CLUSTER_SIZE}"); do
        instance="${INSTANCE}-${i}"
        waitInstanceReady "${instance}"
        lxc exec "${instance}" -- systemctl is-system-running --wait
    done

    # Install LXD on VMs.
    for i in $(seq 1 "${CLUSTER_SIZE}"); do
        instance="${INSTANCE}-${i}"

        echo "Preparing instance ${instance} ..."

        # Install snap daemon.
        lxc exec "${instance}" --env=DEBIAN_FRONTEND=noninteractive -- apt-get update
        lxc exec "${instance}" --env=DEBIAN_FRONTEND=noninteractive -- apt-get -qq -y install snapd

        # Install LXD snap.
        lxc exec "${instance}" -- snap install lxd --channel "${VERSION_LXD}"
    done

    echo "Cluster instances created."
    lxc list
}

# configure_lxd configures LXD cluster.
configure_lxd() {
    local instance
    local ipv4
    local token

    echo "Creating LXD cluster ..."

    # Create LXD cluster.
    for i in $(seq 1 "${CLUSTER_SIZE}"); do
        instance="${INSTANCE}-${i}"

        # Join instances that are not already clustered.
        local isClustered
        isClustered=$(lxc exec "${instance}" -- lxc cluster list 2> /dev/null || true)
        if [ "${isClustered}" ]; then
            continue
        fi

        # Get IPv4 of the instance.
        ipv4=$(instanceIPv4 "${instance}")

        # On the leader instance, just enable clustering and continue.
        if [ "${instance}" = "${LEADER}" ]; then
            lxc exec "${instance}" -- lxc config set core.https_address "${ipv4}"
            lxc exec "${instance}" -- lxc cluster enable "${instance}"
            continue
        fi

        # Create and extract token for a new cluster member.

        token=$(lxc exec "${LEADER}" -- lxc cluster add -q "${instance}")

        if [ "${token}" = "" ]; then
            echo "Error: Failed retrieving join token for instance ${instance}"
            exit 1
        fi

        # Apply the cluster member configuration.
        lxc exec "${instance}" -- lxd init --preseed << EOF
cluster:
  enabled: true
  server_address: ${ipv4}
  cluster_token: ${token}
EOF
    done

    # Create default storage pool.
    if ! lxc exec "${LEADER}" -- lxc storage show default &>/dev/null; then
        for i in $(seq 1 "${CLUSTER_SIZE}"); do
            instance="${INSTANCE}-${i}"

            local opts=()
            if [ "${STORAGE_DRIVER}" = "zfs" ]; then
                opts+=("source=${STORAGE_POOL}/custom/default_${instance}-disk")
            fi

            lxc exec "${LEADER}" -- lxc storage create default "${STORAGE_DRIVER}" --target "${instance}" "${opts[@]}"
        done

        lxc exec "${LEADER}" -- lxc storage create default "${STORAGE_DRIVER}"
        lxc exec "${LEADER}" -- lxc profile device add default root disk pool=default path=/
    fi

    # Create default managed network (lxdbr0).
    if ! lxc network show lxdbr0 &>/dev/null; then
        for i in $(seq 1 "${CLUSTER_SIZE}"); do
            instance="${INSTANCE}-${i}"
            lxc exec "${LEADER}" -- lxc network create lxdbr0 --target "${instance}"
        done

        lxc exec "${LEADER}" -- lxc network create lxdbr0
        lxc exec "${LEADER}" -- lxc profile device add default eth0 nic nictype=bridged parent=lxdbr0
    fi

    # Configure new cluster remote.
    token=$(lxc exec "${LEADER}" -- lxc config trust add --name host --quiet)
    ipv4=$(instanceIPv4 "${LEADER}")

    lxc remote rm "${CLUSTER_NAME}" 2>/dev/null || true
    lxc remote add "${CLUSTER_NAME}" "${ipv4}" --token "${token}"
    lxc remote switch "${CLUSTER_NAME}"

    # Show final cluster.
    lxc cluster list "${CLUSTER_NAME}:"
}


#================================================
# Cleanup
#================================================

# cleanup removes the deployed resources.
#
cleanup() {
    local instance
    local vol

    # Switch from cluster remote if necessary.
    if [ "$(lxc remote get-default)" = "${CLUSTER_NAME}" ]; then
        lxc remote switch local || true
    fi

    # Remove remote.
    lxc remote rm "${CLUSTER_NAME}" 2>/dev/null || true

    # Remove instances.
    for instance in $(lxc list "${CLUSTER_NAME}" --format csv --columns n); do
        echo "Removing instance ${instance} ..."
        lxc delete "${instance}" --force
    done

    # Remove storage pool.
    if lxc storage show "${STORAGE_POOL}" &>/dev/null; then
        for vol in $(lxc storage volume list "${STORAGE_POOL}" --format csv --columns n); do
            echo "Removing storage volume ${vol} ..."
            lxc storage volume delete "${STORAGE_POOL}" "${vol}" || true
        done

        echo "Removing storage pool ${STORAGE_POOL} ..."
        lxc storage delete "${STORAGE_POOL}"
    fi

    # Remove network.
    if lxc network show "${NETWORK_NAME}" &>/dev/null; then
        echo "Removing network ${NETWORK_NAME} ..."
        lxc network delete "${NETWORK_NAME}"
    fi
}

#================================================
# Script
#================================================

action="${1:-}"
case "${action}" in
    deploy)
        echo "==> Creating LXD cluster ${CLUSTER_NAME} with ${CLUSTER_SIZE} (${INSTANCE_TYPE}) members"

        deploy
        configure_lxd

        echo "==> Done: LXD cluster created"
        ;;
    cleanup)
        echo "==> Removing LXD cluster ${CLUSTER_NAME}"

        cleanup

        echo "==> Done: LXD cluster removed"
        ;;
    *)
        echo "Unknown action: ${action}"
        echo "Valid actions are: [deploy, cleanup]"
        echo "Run: $0 <action>"
        exit 1
        ;;
esac
