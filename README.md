# LXD CSI driver for Kubernetes

The LXD CSI driver is an open source implementation of the Container Storage Interface (CSI) that integrates LXD storage backends with Kubernetes. It allows dynamic storage volume provisioning using storage drivers supported by LXD.

> [!WARNING]
> The LXD CSI driver is still in the early stages, and backwards compatibility is not guaranteed.

## Documentation

The LXD CSI driver is documented as part of the official LXD documentation.

Relevant sections include:
+ An overview of the [LXD CSI driver architecture and lifecycle](https://documentation.ubuntu.com/lxd/latest/explanation/csi/)
+ Instructions on how to [install and use the LXD CSI driver](https://documentation.ubuntu.com/lxd/latest/howto/storage_csi/)
+ The [reference documentation](https://documentation.ubuntu.com/lxd/latest/reference/driver_csi/) for the LXD CSI driver CLI and Helm chart

## Quick start

This guide explains how to deploy the LXD CSI driver in your Kubernetes cluster.

> [!IMPORTANT]
> If you’re installing the LXD CSI driver for the first time, we recommend first reviewing the [LXD CSI driver explanation](https://documentation.ubuntu.com/lxd/latest/explanation/csi/) to understand its functionality, and then following the [installation and usage guide](https://documentation.ubuntu.com/lxd/latest/howto/storage_csi/).

### Prerequisites

You need a Kubernetes cluster (of any size) that is running on LXD instances within a dedicated LXD project.
This guide assumes the LXD project is named `lxd-csi-project`.

### Authorization

Enable DevLXD volume management on all LXD instances where CSI will be running:
```sh
lxc config set <instance> --project lxd-csi-project security.devlxd.management.volumes=true
```

> [!NOTE]
> LXD CSI is limited to Kubernetes clusters that are running within a single LXD project.

Create a new authorization group `csi-group` with the permissions to view the project, manage storage volumes, and edit instances:
```sh
lxc auth group create csi-group
lxc auth group permission add csi-group project lxd-csi-project can_view
lxc auth group permission add csi-group project lxd-csi-project storage_volume_manager
lxc auth group permission add csi-group project lxd-csi-project can_edit_instances
```

Next, create an identity `devlxd/csi` and assign the previously created group `csi-group` to it:
```sh
lxc auth identity create devlxd/csi
lxc auth identity group add devlxd/csi csi-group
```

Finally, issue a new bearer token to be used by the CSI driver:
```sh
token=$(lxc auth identity token issue devlxd/csi --quiet)
```

### Deploying CSI driver

Create a namespace `lxd-csi`:
```sh
kubectl create namespace lxd-csi --save-config
```

Create a Kubernetes secret `lxd-csi-secret` containing a previously created bearer token:
```sh
kubectl create secret generic lxd-csi-secret \
    --namespace lxd-csi \
    --from-literal=token="${token}"
```

Deploy LXD CSI driver using Helm:
```sh
helm install lxd-csi-driver oci://ghcr.io/canonical/charts/lxd-csi-driver \
  --version v0 \
  --namespace lxd-csi
```

### Using CSI driver

To use the CSI driver, create a Kubernetes StorageClass that points to the LXD storage pool you want to manage. See [LXD CSI driver usage examples](https://documentation.ubuntu.com/lxd/latest/howto/storage_csi/#usage-examples) in the LXD documentation.
