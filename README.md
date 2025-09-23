# LXD CSI driver for Kubernetes

This repository contains CSI driver for LXD.

> [!WARNING]
> The LXD CSI driver is still in early stages and backwards compatibility is not guaranteed.

## Quick start

This guide demonstrates how to get LXD CSI running in your Kubernetes cluster.

### Requirements

This guide assumes that you have a Kubernetes cluster (of any size) running on LXD instances within dedicated LXD project named `lxd-csi-project`.
It also assumes that you have admin permission within a Kubernetes cluster.

### Authorization

> [!NOTE]
> LXD CSI is limited to Kubernetes clusters that are running within a single LXD project.

The CSI requires a bearer token issued for DevLXD identity.
The identity must have the permissions on the project where Kubernetes nodes are running to:
- view the project,
- manage (view, create, edit, delete) storage volumes,
- edit instances.

First, create a new authorization group `csi-group` with the required permissions.
```sh
lxc auth group create csi-group
lxc auth group permission add csi-group project lxd-csi-project can_view
lxc auth group permission add csi-group project lxd-csi-project storage_volume_manager
lxc auth group permission add csi-group project lxd-csi-project can_edit_instances
```

Second, create an identity `devlxd/csi` and assign the previously created group `csi-group` to it:
```sh
lxc auth identity create devlxd/csi
lxc auth identity group add devlxd/csi csi-group
```

Finally, issue a new bearer token to be used by the CSI:
```sh
token=$(lxc auth identity token issue devlxd/csi --quiet)
```

### Deploying CSI driver

Create a namespace `lxd-csi`:
```sh
kubectl namespace create lxd-csi --save-config
```

Create a Kubernetes secret `lxd-csi-token` containing a previously created bearer token:
```sh
kubectl create secret generic lxd-csi-token \
    --namespace lxd-csi \
    --from-literal=token="${token}"
```

Deploy LXD CSI controller and node servers from manifests in [deploy](/deploy/) directory.
```sh
kubectl apply -f deploy/
```

### Using CSI driver

To use the CSI driver, the only remaining bit is to create a Kubernete StorageClass that points to the LXD storage pool you want to manage.

You can take the inspiration from an example in [example](/example/) directory.
