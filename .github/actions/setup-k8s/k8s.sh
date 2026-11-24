#!/bin/bash
#
# This script deploys a Canonical Kubernetes in LXD virtual machines and
# is intended purely for testing LXD CSI driver.
#
# If LXD is running in standalone mode (is not clustered), the script will
# create a bridged network and a storage pool for the Kubernetes nodes.
#
# If LXD is clustered, the script will distribute the Kubernetes nodes
# across the LXD cluster members. It will expect that a bridged network
# named "br0" and storage pool name "default" already exist.
#

set -euo pipefail

# Source bin/helpers from canonical/lxd-ci repository.
# LXD_CI_HELPERS_PATH can be set to a local path to skip the network fetch.
if [ -n "${LXD_CI_HELPERS_PATH:-}" ]; then
    source "${LXD_CI_HELPERS_PATH}"
else
    # shellcheck source=/dev/null
    source <(
        curl -fsSL https://raw.githubusercontent.com/canonical/lxd-ci/refs/heads/main/bin/helpers \
        || { echo "Error: Failed to source bin/helpers from canonical/lxd-ci" >&2; exit 1; }
    )
fi

# Script dir where the script is located.
SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"

# Root dir of the repository.
ROOT_DIR="$(realpath "${SCRIPT_DIR}/../../..")"

# Temporary directory for job logs.
JOB_DIR="$(mktemp -d -t lxd-csi-run.XXXXXX)"

# Remove JOB dir on exit while preserving exit code.
cleanup() {
    rc=$?
    rm -rf "${JOB_DIR}"
    exit $rc
}
trap cleanup EXIT INT TERM

setEnv() {
    # Precheck required binaries are installed.
    for cmd in kubectl helm lxc; do
        if ! command -v "${cmd}" &> /dev/null; then
            echo "Error: ${cmd} is not installed."
            exit 1
        fi
    done

    # Precheck that LXD is accessible and trusts us.
    if lxc query /1.0 | jq --exit-status '.auth != "trusted"'; then
        echo "Error: The LXD server is either not accessible or does not trust the client."
        exit 1
    fi

    K8S_CLUSTER_NAME="${1:-${K8S_CLUSTER_NAME:-}}"
    if [ -z "${K8S_CLUSTER_NAME}" ]; then
        echo "Error: Cluster name is not set."
        echo "You can provide a cluster name as a command argument or an environment variable K8S_CLUSTER_NAME."
        exit 1
    fi

    # LXD Kubernetes cluster configuration.
    : "${K8S_NODE_COUNT:=1}"
    : "${K8S_SNAP_CHANNEL:=latest/edge}"
    : "${K8S_SNAP_CACHE_DIR:=}" # Optional snap cache directory for offline k8s snap installation.
    : "${K8S_IMAGE_CACHE_PATH:=}" # Optional OCI image tarball cache path (imported on hit, exported on miss).
    : "${K8S_IMAGE_CACHE_EXTRA_IMAGES:=}" # Optional space-separated extra OCI images to include in the image cache.
    : "${K8S_KUBECONFIG_PATH:=${ROOT_DIR}/.kube/${K8S_CLUSTER_NAME}.yml}" # Do not use "${HOME}/..." by default to avoid overwriting user's kubeconfig.
    : "${K8S_CSI_IMAGE_PATH:=}" # Path to the custom LXD CSI driver image to import to cluster nodes.
    : "${K8S_CSI_IMAGE_TAG:=latest-edge}"    # Use "dev" to build and use local image from this repository.
    : "${K8S_CSI_CHART_TAG:=v0-latest-edge}" # Ignored when K8S_CSI_IMAGE_TAG is "dev" or K8S_CSI_CHART_PATH is set.
    : "${K8S_CSI_CHART_PATH:=}" # Optional path to a cached Helm chart archive.
    : "${K8S_HELM_TIMEOUT:=300s}"

    # LXD instance, storage, and network configuration.
    : "${LXD_INSTANCE_IMAGE:=ubuntu-minimal-daily:24.04}"
    : "${LXD_INSTANCE_TYPE:=vm}" # [ "vm", "container" ]
    : "${LXD_PROJECT_NAME:=default}"
    : "${LXD_NETWORK_NAME:=${K8S_CLUSTER_NAME}-br0}"
    : "${LXD_STORAGE_POOL_NAME:=${K8S_CLUSTER_NAME}-storage-pool}"
    : "${LXD_STORAGE_POOL_DRIVER:=zfs}"
    : "${LXD_STORAGE_POOL_SIZE:=$(( K8S_NODE_COUNT * 16 ))GiB}"

    # Private GitHub runners currently are 2 vCPU / 8GiB systems; reduce instance
    # sizing to fit those limits while leaving the default values intact elsewhere.
    local cpuCount
    LXD_INSTANCE_CPU_LIMIT="4"
    LXD_INSTANCE_MEMORY_LIMIT="4GB"
    cpuCount="$(nproc 2>/dev/null || echo 4)"
    if [ "${cpuCount}" -lt 4 ]; then
        LXD_INSTANCE_CPU_LIMIT="${cpuCount}"
        LXD_INSTANCE_MEMORY_LIMIT="3GB"
    fi
}

# Arrays for job pids and logs
declare -A pids logs

# jobRun runs a command in the background and captures its output in a log file.
# The PID and log path are stored in global arrays `pids` and `logs`.
jobRun() {
    local name="$1"
    shift
    local log="${JOB_DIR}/${name}.log"

    echo "===> ${name} ..."

    # Run in subshell and redirect output to log.
    (
        set -euo pipefail
        "${@}" >"${log}" 2>&1
    ) &

    # Store PID and log path.
    pids["${name}"]=$!
    logs["${name}"]="${log}"
}

# jobWaitAll waits all background jobs started via fork() to finish.
# If any job fails, its log is printed to stderr, and all other jobs are killed.
jobWaitAll() {
    local name
    for name in "${!pids[@]}"; do
        if ! wait "${pids[${name}]}"; then
            echo "Error: Failed job '${name}'" >&2
            echo "------ JOB LOG ------" >&2
            cat "${logs[${name}]}" >&2
            echo "---------------------" >&2

            # Kill remaining.
            local n
            for n in "${!pids[@]}"; do
                if [ "$n" = "${name}" ]; then
                    continue
                fi

                if kill -0 "${pids[$n]}" 2>/dev/null; then
                    kill "${pids[$n]}" 2>/dev/null || true
                fi
            done

            wait || true
            return 1
        fi

        unset "pids[${name}]" "logs[${name}]"
    done

    # Clear arrays.
    pids=()
    logs=()
}

members=$(lxc cluster ls --format csv | awk -F, '{print $1}' || echo "")
memberCount=$(echo "${members}" | wc -l)

# lxdFindMember returns the LXD cluster member name for the given index.
# It uses modulo arithmetic to wrap around if the index exceeds the number of members.
lxdFindMember() {
    if [ -z "${members}" ]; then
        echo ""
        return 0
    fi

    # Trim non-numeric characters from input.
    local value="${1}"
    local index="${value//[^0-9]/}"

    if [ -z "${index}" ] || [[ "${index}" -lt 1 ]]; then
        echo "Error: lxdFindMember: Invalid input value '${value}' (index: '${index}'): Index must be a positive number" >&2
        return 1
    fi

    local index=$(( ((index - 1) % memberCount) + 1))
    echo "${members}" | sed -n "${index}p"
}

# lxdProjectCreate creates a new LXD project with the name specified in the environment variable LXD_PROJECT_NAME.
lxdProjectCreate() {
    local project="${LXD_PROJECT_NAME}"

    if [ "${project}" != "default" ]; then
        echo "===> Creating LXD project ${project} ..."
        lxc project create "${project}"
    fi
}

lxdNetworkCreate() {
    local network="${LXD_NETWORK_NAME}"

    echo "===> Creating LXD network ${network} ..."

    if [ "${members}" != "" ]; then
        echo "SKIP: Using bridged NIC instead of LXD managed network"
        return 0
    fi

    if lxc network show "${network}" &>/dev/null; then
        echo "SKIP: Network ${network} already exists"
        return 0
    fi

    lxc network create "${network}" ipv4.address=172.16.17.1/24 ipv4.nat=true
}

lxdStorageCreate() {
    local pool="${LXD_STORAGE_POOL_NAME}"
    local size="${LXD_STORAGE_POOL_SIZE}"
    local driver="${LXD_STORAGE_POOL_DRIVER}"

    if [ "${members}" != "" ]; then
        echo "===> LXD is clustered: Using existing storage pool 'default' ..."
        LXD_STORAGE_POOL_NAME="default"

        if ! lxc storage show "${LXD_STORAGE_POOL_NAME}" &>/dev/null; then
            echo "Error: When LXD is clustered, a storage pool 'default' is required to exist." >&2
            exit 1
        fi

        LXD_STORAGE_POOL_DRIVER=$(lxc storage show "${LXD_STORAGE_POOL_NAME}" | awk '/driver/ {print $2}')
        return 0
    fi

    if lxc storage show "${pool}" &>/dev/null; then
        echo "SKIP: Storage pool ${pool} already exists"
        return 0
    fi

    local opts=()
    if [ "${driver}" != "dir" ] && [ "${size}" != "" ]; then
        opts+=("size=${size}")
    fi

    echo "===> Creating LXD storage pool ${pool} (driver: ${driver}) ..."
    lxc storage create "${pool}" "${driver}" "${opts[@]}"
}

lxdInstanceCreate() {
    local instance="$1"
    local image="${LXD_INSTANCE_IMAGE}"
    local instanceType="${LXD_INSTANCE_TYPE}"
    local project="${LXD_PROJECT_NAME}"
    local network="${LXD_NETWORK_NAME}"
    local storage="${LXD_STORAGE_POOL_NAME}"
    local target

    target="$(lxdFindMember "${instance}")"

    if [ -z "${instance}" ]; then
        echo "Usage: lxdInstanceCreate <instance>" >&2
        return 1
    fi

    local opts=()
    if [ "${instanceType}" = "vm" ]; then
        opts+=(--vm)
    fi

    if [ "${members}" = "" ]; then
        # When LXD is not clustered, use created bridge network.
        opts+=(--network "${network}")
    fi

    # Create LXD virtual machine.
    echo "===> Creating LXD instance ${instance} (target: ${target:-none})..."
    lxc init "${image}" "${instance}" \
        --no-profiles \
        --project "${project}" \
        --storage "${storage}" \
        --config limits.cpu="${LXD_INSTANCE_CPU_LIMIT}" \
        --config limits.memory="${LXD_INSTANCE_MEMORY_LIMIT}" \
        --config security.devlxd.management.volumes=true \
        --device root,size=16GiB \
        --target "${target}" \
        "${opts[@]}"

    if [ "${members}" != "" ]; then
        # When LXD is clustered, expect network bridge named "br0" to exist.
        lxc config device add "${instance}" eth0 nic \
            nictype=bridged \
            parent=br0 \
            --project "${project}"
    fi

    lxc start "${instance}" --project "${project}"
}

# lxdInstanceIP retrieves the IP address of the specified LXD instance.
# It assumes the instance has a network interface named "enp5s0" for VMs and "eth0" for containers.
lxdInstanceIP() {
    local instance="$1"
    local instanceType="${LXD_INSTANCE_TYPE}"
    local project="${LXD_PROJECT_NAME}"

    if [ -z "${instance}" ]; then
        echo "Usage: lxdInstanceIP <instance>" >&2
        return 1
    fi

    local ifName="eth0" # Default for containers
    if [ "${instanceType}" = "vm" ]; then
        ifName="enp5s0" # Default for VMs
    fi

    lxc list "${instance}" --project "${project}" --format json | jq --arg ifName "${ifName}" -r '.[0].state.network[$ifName].addresses[] | select(.family=="inet") | .address'
}

# k8sInstall installs Canonical Kubernetes on the specified LXD instance.
k8sInstall() {
    local instance="$1"
    local project="${LXD_PROJECT_NAME}"
    local k8sSnapChannel="${K8S_SNAP_CHANNEL}"

    if [ -z "${instance}" ]; then
        echo "Usage: k8sInstall <instance>" >&2
        return 1
    fi

    echo "===> ${instance}: Installing Canonical Kubernetes ..."

    local success=false
    if [ "${project}" = "default" ] && declare -F install_snap_in_instance >/dev/null; then
        if declare -F waitSnapdSeedInstance >/dev/null; then
            waitSnapdSeedInstance "${instance}" || true
        else
            lxc exec "${instance}" --project "${project}" -- timeout 60 snap wait system seed.loaded || true
        fi

        if SNAP_CACHE_DIR="${K8S_SNAP_CACHE_DIR:-${SNAP_CACHE_DIR:-}}" install_snap_in_instance "${instance}" k8s "${k8sSnapChannel}"; then
            success=true
        fi
    else
        lxc exec "${instance}" --project "${project}" -- timeout 60 snap wait system seed.loaded || true
    fi

    # Fall back to the snap store with retries in case of cache miss or transient network issues.
    if [ "${success}" != "true" ]; then
        for i in $(seq 1 5); do
            if lxc exec "${instance}" --project "${project}" -- snap install k8s --channel="${k8sSnapChannel}" --classic; then
                success=true
                break
            fi

            echo "===> ${instance}: Retry installing Canonical Kubernetes (attempt ${i}/5) ..."
            sleep 3
        done
    fi

    if [ "${success}" != "true" ]; then
        echo "Error: ${instance}: Failed to install Canonical Kubernetes!" >&2
        return 1
    fi

    # As a convenience, setup alias "k" for kubectl within the instance.
    lxc exec "${instance}" --project "${project}" -- bash -c "echo \"alias k='k8s kubectl'\" >> ~/.bashrc"
}

# k8sStageImageCache copies a cached OCI image tarball into the Canonical Kubernetes
# containerd side-load directory so images are imported automatically on startup.
k8sStageImageCache() {
    local instance="$1"
    local imagePath="$2"
    local project="${LXD_PROJECT_NAME}"

    if [ -z "${imagePath}" ] || [ ! -f "${imagePath}" ]; then
        return 0
    fi

    echo "===> ${instance}: Staging cached container images from ${imagePath} ..."
    lxc exec "${instance}" --project "${project}" -- mkdir -p /var/snap/k8s/common/images
    lxc file push --quiet --project "${project}" "${imagePath}" "${instance}/var/snap/k8s/common/images/cache.tar"
}

# k8sWaitImageCacheLoaded waits for containerd's sideload-images plugin to finish
# importing the staged cache tarball on all nodes, then removes the loaded archive
# to reclaim VM disk space.
k8sWaitImageCacheLoaded() {
    local project="${LXD_PROJECT_NAME}"

    if [ -z "${K8S_IMAGE_CACHE_PATH}" ] || [ ! -f "${K8S_IMAGE_CACHE_PATH}" ]; then
        return 0
    fi

    echo "===> Waiting for cached container images to finish importing on all nodes ..."
    for i in $(seq 1 "${K8S_NODE_COUNT}"); do
        local instance="${K8S_CLUSTER_NAME}-node-${i}"
        local deadline=$((SECONDS + 120))
        while (( SECONDS < deadline )); do
            if lxc exec "${instance}" --project "${project}" -- test -f /var/snap/k8s/common/images/cache.tar.loaded; then
                lxc exec "${instance}" --project "${project}" -- rm -f /var/snap/k8s/common/images/cache.tar.loaded
                break
            fi
            if ! lxc exec "${instance}" --project "${project}" -- test -f /var/snap/k8s/common/images/cache.tar; then
                break
            fi
            sleep 2
        done
        if lxc exec "${instance}" --project "${project}" -- test -f /var/snap/k8s/common/images/cache.tar; then
            echo "Warning: ${instance}: Timed out waiting for cached images to load; continuing" >&2
        fi
    done
}

# k8sExportImageCache exports all container images pulled across the cluster into
# a single OCI tarball so subsequent runs can side-load them without pulling from
# upstream registries.
k8sExportImageCache() {
    local outputPath="$1"
    local firstNode="${K8S_CLUSTER_NAME}-node-1"
    local project="${LXD_PROJECT_NAME}"
    local ctr=(/snap/k8s/current/bin/ctr --address /run/containerd/containerd.sock --namespace k8s.io)
    local arch platform

    if [ -z "${outputPath}" ] || [ -f "${outputPath}" ] || [ "${K8S_CSI_IMAGE_TAG}" = "dev" ]; then
        return 0
    fi

    arch="$(dpkg --print-architecture 2>/dev/null || uname -m)"
    case "${arch}" in
        x86_64) arch="amd64" ;;
        aarch64) arch="arm64" ;;
        *) ;;
    esac
    platform="linux/${arch}"

    echo "===> Exporting cluster container images to ${outputPath} ..."

    # Pre-pull any caller-requested extra images (e.g. e2e test pod image) on node 1.
    local img
    for img in ${K8S_IMAGE_CACHE_EXTRA_IMAGES}; do
        if ! lxc exec "${firstNode}" --project "${project}" -- "${ctr[@]}" images pull --platform "${platform}" "${img}" >/dev/null; then
            echo "Warning: Failed to pull extra cache image ${img}; skipping" >&2
        fi
    done

    # Collect any tagged images present only on other nodes (e.g. pods scheduled there)
    # onto the first node so a single export captures the full cluster image set.
    local firstImages nodeImages
    firstImages="$(lxc exec -T -n "${firstNode}" --project "${project}" -- "${ctr[@]}" images list -q | grep -v -e '^sha256:' -e '@sha256:' || true)"
    for i in $(seq 2 "${K8S_NODE_COUNT}"); do
        local node="${K8S_CLUSTER_NAME}-node-${i}"
        nodeImages="$(lxc exec -T -n "${node}" --project "${project}" -- "${ctr[@]}" images list -q | grep -v -e '^sha256:' -e '@sha256:' || true)"
        while IFS= read -r img; do
            [ -n "${img}" ] || continue
            if ! grep -qxF "${img}" <<< "${firstImages}"; then
                echo "Copying ${img} from ${node} to ${firstNode} for cache export ..."
                if lxc exec -T -n "${node}" --project "${project}" -- "${ctr[@]}" images export --platform "${platform}" - "${img}" \
                    | lxc exec -T "${firstNode}" --project "${project}" -- "${ctr[@]}" images import - >/dev/null; then
                    firstImages="${firstImages}"$'\n'"${img}"
                else
                    echo "Warning: Failed to copy ${img} from ${node}; skipping" >&2
                fi
            fi
        done <<< "${nodeImages}"
    done

    local sortedImages
    sortedImages="$(printf '%s\n' "${firstImages}" | sort -u)"

    local imgs=()
    while IFS= read -r img; do
        [ -n "${img}" ] || continue
        imgs+=("${img}")
    done <<< "${sortedImages}"

    if [ "${#imgs[@]}" -eq 0 ]; then
        echo "Warning: No container images found to export" >&2
        return 0
    fi

    mkdir -p "$(dirname "${outputPath}")"
    local tmpPath="${outputPath}.tmp.$$"
    if lxc exec -T -n "${firstNode}" --project "${project}" -- "${ctr[@]}" images export --platform "${platform}" - "${imgs[@]}" > "${tmpPath}"; then
        mv -f "${tmpPath}" "${outputPath}"
        echo "===> Exported ${#imgs[@]} container images to ${outputPath}"
    else
        rm -f "${tmpPath}"
        echo "Warning: Failed to export container images to ${outputPath}" >&2
    fi
}

# k8sSetupNode deploys a single Kubernetes node (master or worker).
# It creates LXD instance and installs Canonical Kubernetes on that
# instance.
k8sSetupNode() {
    local instance="$1"

    if [ -z "${instance}" ]; then
        echo "Usage: k8sDeployNode <instance>" >&2
        return 1
    fi

    waitInstanceReady "${instance}"
    k8sInstall "${instance}"
    k8sStageImageCache "${instance}" "${K8S_IMAGE_CACHE_PATH}"
}

# k8sBootstrap bootstraps the first Canonical Kubernetes node.
k8sBootstrap() {
    local instance="$1"
    local project="${LXD_PROJECT_NAME}"

    if [ -z "${instance}" ]; then
        echo "Usage: k8sBootstrap <instance>" >&2
        return 1
    fi

    echo "===> ${instance}: Bootstrapping Kubernetes cluster ..."
    # Enable only network and dns; leave local-storage and metrics-server disabled
    # from bootstrap so their pods and images are never deployed or pulled.
    lxc exec -T "${instance}" --project "${project}" -- k8s bootstrap --timeout=5m --file - >/dev/null <<'EOF'
cluster-config:
  network:
    enabled: true
  dns:
    enabled: true
  local-storage:
    enabled: false
  metrics-server:
    enabled: false
EOF

    echo "===> ${instance}: Waiting for Kubernetes cluster to be ready..."
    local retry=10
    for i in $(seq 1 "${retry}"); do
        if lxc exec "${instance}" --project "${project}" -- k8s status --timeout=1m --wait-ready > /dev/null; then
            break
        fi

        if [ "${i}" -eq "${retry}" ]; then
            # Print some debug info about failed k8s deployment.
            echo "==> DEBUG: k8s snap logs" >&2
            lxc exec "${instance}" --project "${project}" -- snap logs k8s -n 200 >&2 || true

            echo "==> DEBUG: k8s status" >&2
            lxc exec "${instance}" --project "${project}" -- k8s status --output-format yaml >&2 || true

            echo "==> DEBUG: k8s pods" >&2
            lxc exec "${instance}" --project "${project}" -- k8s kubectl get pods --all-namespaces >&2 || true

            echo "Error: Kubernetes is still not ready after ${retry} minutes!" >&2
            exit 1
        fi

        sleep 5
    done
}

# k8sJoin join an additional node into already bootstraped Kubernetes cluster.
k8sJoin() {
    local instance="$1"
    local type="$2" # ["master", "worker"]
    local masterInstance="$3"
    local project="${LXD_PROJECT_NAME}"
    local clusterName="${K8S_CLUSTER_NAME}"

    if [ -z "${instance}" ] || [ -z "${type}" ] || [ -z "${masterInstance}" ]; then
        echo "Usage: k8sJoin <instance> <nodeType> <masterInstance>" >&2
        return 1
    fi

    if [ "${type}" != "master" ] && [ "${type}" != "worker" ]; then
        echo "k8sJoin: invalid type '${type}': must be one of [master, worker])" >&2
        return 1
    fi

    local opts=()
    if [ "${type}" = "worker" ]; then
        opts+=(--worker)
    fi

    echo "===> ${instance}: Joining to Kubernetes cluster ${clusterName} as ${type} node ..."
    local joinToken
    joinToken=$(lxc exec "${masterInstance}" --project "${project}" -- k8s get-join-token "${instance}" "${opts[@]}")
    lxc exec "${instance}" --project "${project}" -- k8s join-cluster "${joinToken}"
}

# k8sWaitReady waits for the Kubernetes cluster on the specified
k8sWaitReady() {
    local timeout="${TIMEOUT:-600}"
    local kubeconfigPath="${K8S_KUBECONFIG_PATH}"

    if [ -z "${kubeconfigPath}" ]; then
        echo "Error: k8sWaitReady: Kubeconfig path not provided" >&2
        return 1
    fi

    # List nodes and pods on error.
    trap '
        kubectl --kubeconfig "${kubeconfigPath}" get nodes
        kubectl --kubeconfig "${kubeconfigPath}" get pods -A
        echo "Error: Kubernetes cluster is not ready after ${timeout} seconds!" >&2
    ' ERR

    local deadline=$((SECONDS + timeout))
    local nodesReady=0
    local podsReady=0

    echo "===> Waiting for all Kubernetes nodes and pods to be ready ..."
    while (( SECONDS < deadline )); do
        if [ "${nodesReady}" -eq 0 ]; then
            local nodeCount
            nodeCount="$(kubectl --kubeconfig "${kubeconfigPath}" get nodes --no-headers 2>/dev/null | wc -l || echo 0)"
            if [ "${nodeCount}" -ge "${K8S_NODE_COUNT}" ] \
                && kubectl --kubeconfig "${kubeconfigPath}" wait --for=condition=Ready nodes --all --timeout=30s; then
                nodesReady=1
            fi
        fi
        if [ "${nodesReady}" -eq 1 ] && [ "${podsReady}" -eq 0 ]; then
            if kubectl --kubeconfig "${kubeconfigPath}" wait --for=condition=Ready pods --all -A --timeout=30s; then
                podsReady=1
            fi
        fi

        if [ "${nodesReady}" -eq 1 ] && [ "${podsReady}" -eq 1 ]; then
            break
        fi

        sleep 2
    done

    if (( SECONDS >= deadline )); then
        return 1
    fi

    trap - ERR
}

# k8sCopyKubeconfig copies the kubeconfig file from the specified
# instance to the host. It also adjusts the server address in the
# kubeconfig to point to the IP address of the specified instance.
k8sCopyKubeconfig() {
    local instance="$1"
    local project="${LXD_PROJECT_NAME}"
    local kubeconfigPath="${K8S_KUBECONFIG_PATH}"
    local instanceIP
    instanceIP="$(lxdInstanceIP "${instance}")"

    if [ -z "${instance}" ] || [ -z "${kubeconfigPath}" ]; then
        echo "Usage: k8sCopyKubeconfig <instance> <kubeconfigPath>" >&2
        return 1
    fi

    echo "===> Copying kubeconfig from instance ${instance} to ${kubeconfigPath} ..."
    mkdir -p "$(dirname "${kubeconfigPath}")"
    lxc file pull --project "${project}" "${instance}/etc/kubernetes/admin.conf" "${kubeconfigPath}"
    chmod 600 "${kubeconfigPath}"

    # Adjust the server address in kubeconfig.
    echo "===> Adjusting cluster address in Kubeconfig ..."
    kubectl --kubeconfig "${kubeconfigPath}" config set-cluster k8s --server="https://${instanceIP}:6443"
}

k8sImportImageTarball() {
    local imagePath="$1"
    local project="${LXD_PROJECT_NAME}"
    local clusterName="${K8S_CLUSTER_NAME}"

    if [ "${imagePath}" = "" ]; then
        echo "Usage: k8sImportImageTarball <imagePath>" >&2
        return 1
    fi

    if [ ! -f "${imagePath}" ]; then
        echo "Error: k8sImportImageTarball: Image path ${imagePath} not found" >&2
        return 1
    fi

    # Import the image tarball to all cluster nodes.
    for i in $(seq 1 "${K8S_NODE_COUNT}"); do
        instance="${K8S_CLUSTER_NAME}-node-${i}"
        echo "Importing image ${imagePath} to node ${instance} ..."
        lxc exec "${instance}" --project "${project}" -- /snap/k8s/current/bin/ctr \
            --address /run/containerd/containerd.sock \
            --namespace k8s.io \
            images import - < "${imagePath}"
    done
}

# installLXDCSIDriver installs the LXD CSI driver on the Kubernetes cluster.
# It creates the necessary namespace and applies the deployment manifests.
installLXDCSIDriver() {
    local kubeconfigPath="${K8S_KUBECONFIG_PATH}"
    local imagePath="${K8S_CSI_IMAGE_PATH}"
    local chartRepo="oci://ghcr.io/canonical/charts/lxd-csi-driver"
    local project="${LXD_PROJECT_NAME}"
    local name="${K8S_CLUSTER_NAME}-lxd-csi"
    local group="${name}-group"
    local identity="${name}-identity"

    echo "===> Confirguring DevLXD identity for CSI driver ..."
    # Create LXD auth group.
    lxc auth group create "${group}"

    # Assign permissions to manage storage volumes and edit instances.
    lxc auth group permission add "${group}" project "${project}" can_view
    lxc auth group permission add "${group}" project "${project}" storage_volume_manager
    lxc auth group permission add "${group}" project "${project}" can_edit_instances

    # Create LXD auth identity.
    lxc auth identity create "devlxd/${identity}"
    lxc auth identity group add "devlxd/${identity}" "${group}"

    # Create a new token for the identity.
    token=$(lxc auth identity token issue "devlxd/${identity}" --quiet)

    echo "===> Installing LXD CSI driver ..."
    kubectl --kubeconfig "${kubeconfigPath}" create namespace lxd-csi --save-config
    kubectl --kubeconfig "${kubeconfigPath}" create secret generic lxd-csi-secret --namespace lxd-csi --from-literal=token="${token}"

    if [ "${K8S_CSI_IMAGE_PATH}" != "" ]; then
        # Build image from source and import it to cluster nodes.
        k8sImportImageTarball "${imagePath}"
    fi

    local chartVersion=()
    if [ "${K8S_CSI_IMAGE_TAG}" = "dev" ]; then
        # Use local chart from repository.
        chartRepo="${ROOT_DIR}/charts"
    elif [ -n "${K8S_CSI_CHART_PATH}" ] && [ -f "${K8S_CSI_CHART_PATH}" ]; then
        # Use cached chart archive.
        chartRepo="${K8S_CSI_CHART_PATH}"
    else
        chartVersion=(--version "${K8S_CSI_CHART_TAG}")
    fi

    if ! helm install lxd-csi "${chartRepo}" \
        --kubeconfig "${kubeconfigPath}" \
        --namespace lxd-csi \
        --timeout "${K8S_HELM_TIMEOUT}" \
        --wait \
        --debug \
        --set driver.image.tag="${K8S_CSI_IMAGE_TAG}" \
        --set snapshotter.enabled=true \
        "${chartVersion[@]}"; then
        echo "==> DEBUG: lxd-csi pods" >&2
        kubectl --kubeconfig "${kubeconfigPath}" -n lxd-csi get pods -o wide >&2 || true

        echo "==> DEBUG: lxd-csi pod descriptions" >&2
        kubectl --kubeconfig "${kubeconfigPath}" -n lxd-csi describe pods >&2 || true

        echo "==> DEBUG: lxd-csi events" >&2
        kubectl --kubeconfig "${kubeconfigPath}" -n lxd-csi get events --sort-by='.lastTimestamp' >&2 || true

        return 1
    fi
}

# help prints the usage information for this script.
help() {
    echo -e "Usage: $0 <command>\n"
    echo -e "Commands:"
    echo -e "  deploy  [<cluster_name>] - Deploy Kubernetes cluster (with LXD CSI driver installed)"
    echo -e "  cleanup [<cluster_name>] - Clean up Kubernetes cluster"
    echo -e "  help                     - Show this help message"
}

# Entry point.
cmd="${1:-}"
case "${cmd}" in
    deploy)
        setEnv "${2:-}"
        echo "==> Deploying Kubernetes cluster ${K8S_CLUSTER_NAME} ..."

        firstNode="${K8S_CLUSTER_NAME}-node-1"

        if [ "${K8S_NODE_COUNT}" -lt 1 ]; then
            echo "Error: K8S_NODE_COUNT must be at least 1."
            exit 1
        fi

        lxdProjectCreate
        lxdNetworkCreate
        lxdStorageCreate

        # Create LXD instances for Kubernetes nodes.
        for i in $(seq 1 "${K8S_NODE_COUNT}"); do
            instance="${K8S_CLUSTER_NAME}-node-${i}"

            # Create an instance before configuring the node in the background process.
            # This prevents conflicts with image download when LXD is clustered.
            lxdInstanceCreate "${instance}"

            jobRun \
                "Setup node ${instance}" \
                k8sSetupNode "${instance}"
        done

        jobWaitAll

        # Bootstrap the first Kubernetes node.
        k8sBootstrap "${firstNode}"

        # Join additional nodes to the cluster.
        for i in $(seq 1 "${K8S_NODE_COUNT}"); do
            instance="${K8S_CLUSTER_NAME}-node-${i}"
            if [ "${instance}" != "${firstNode}" ]; then
                jobRun \
                    "Join node ${instance}" \
                    k8sJoin "${instance}" "worker" "${firstNode}"
            fi
        done

        jobWaitAll

        # Copy kubeconfig to host and adjust the server address.
        k8sCopyKubeconfig "${firstNode}" "${K8S_KUBECONFIG_PATH}"

        # Ensure cluster is ready.
        k8sWaitReady

        # Ensure any staged container image cache finished loading before deploying CSI pods.
        k8sWaitImageCacheLoaded

        if [ "${K8S_CSI_IMAGE_TAG}" != "" ]; then
            # Install the LXD CSI driver.
            installLXDCSIDriver

            # Wait for the CSI to become ready.
            k8sWaitReady
        fi

        # Populate container image cache on miss so subsequent runs avoid registry pulls.
        k8sExportImageCache "${K8S_IMAGE_CACHE_PATH}"

        echo "==> Done"
        echo -e "\nKubernetes cluster:"
        echo -e "  Name: ${K8S_CLUSTER_NAME}"
        echo -e "  Address: $(lxdInstanceIP "${firstNode}")"
        echo -e "\nTo access the cluster, run:"
        echo -e "  kubectl --kubeconfig=${K8S_KUBECONFIG_PATH} get nodes"
        ;;
    cleanup)
        setEnv "${2:-}"
        echo "==> Cleaning up Kubernetes cluster ${K8S_CLUSTER_NAME} ..."
        echo "NOTE: Volumes created by the LXD CSI driver are not deleted by this script!"

        project="${LXD_PROJECT_NAME}"

        # Delete instances.
        for instance in $(lxc list "${K8S_CLUSTER_NAME}" --format csv --columns n); do
            echo "===> Deleting LXD instance ${instance} ..."
            lxc delete "${instance}" --project "${project}" --force
        done

        # Delete storage.
        storage="${LXD_STORAGE_POOL_NAME}"
        if lxc storage show "${storage}" &>/dev/null; then
            for volume in $(lxc storage volume list "${storage}" --project "${project}" --format csv --columns n); do
                if lxc image show "${volume}" &>/dev/null; then
                    echo "===> Deleting LXD image ${volume} ..."
                    lxc image delete "${volume}" --project "${project}"
                else
                    echo "===> Deleting LXD storage volume ${volume} ..."
                    lxc storage volume delete "${storage}" "${volume}" --project "${project}"
                fi
            done

            echo "===> Deleting LXD storage pool ${storage} ..."
            lxc storage delete "${storage}"
        fi

        # Delete network.
        network="${LXD_NETWORK_NAME}"
        if lxc network show "${network}" &>/dev/null; then
            echo "===> Deleting LXD network ${network} ..."
            lxc network delete "${network}"
        fi

        # Delete auth identities and groups.
        identity="devlxd/${K8S_CLUSTER_NAME}-lxd-csi-identity"
        if lxc auth identity show "${identity}" &>/dev/null; then
            echo "===> Deleting LXD auth identity ${identity} ..."
            lxc auth identity delete "${identity}"
        fi

        group="${K8S_CLUSTER_NAME}-lxd-csi-group"
        if lxc auth group show "${group}" &>/dev/null; then
            echo "===> Deleting LXD auth group ${group} ..."
            lxc auth group delete "${group}"
        fi

        # Delete project.
        if lxc project show "${project}" &>/dev/null && [ "${project}" != "default" ]; then
            echo "===> Deleting LXD project ${project} ..."
            lxc project delete "${project}"
        fi

        # Delete kubeconfig.
        kubeconfigPath="${K8S_KUBECONFIG_PATH}"
        echo "===> Deleting Kubeconfig on path ${kubeconfigPath} ..."
        rm -f "${kubeconfigPath}"

        echo "==> Done"
        ;;
    help)
        help
        ;;
    *)
        if [ "${cmd}" != "" ]; then
            echo -e "Error: Unsupported command '${cmd}'\n" >&2
            help
            exit 1
        fi

        help
        exit 0
        ;;
esac
