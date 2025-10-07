# LXD CSI driver for Kubernetes

This repository contains a CSI driver for LXD.

> [!WARNING]
> The LXD CSI driver is still in the early stages, and backwards compatibility is not guaranteed.

## Quick start

This guide demonstrates how to get the LXD CSI driver running in your Kubernetes cluster.

### Requirements

You need a Kubernetes cluster (of any size) that is running on LXD instances within a dedicated LXD project.
This guide assumes the LXD project is named `lxd-csi-project`.
It also assumes you have admin permissions in the Kubernetes cluster.

### Authorization

By default, DevLXD is not allowed to manage storage volumes or attach them to instances.
You must enable this by setting `security.devlxd.management.volumes` to `true` on all LXD instances
where CSI will be running.

For example, to enable DevLXD volume management on instance `node-1`, run:
```sh
lxc config set node-1 --project lxd-csi-project security.devlxd.management.volumes=true
```

You can also use an LXD profile to apply this setting to multiple instances at once.

> [!NOTE]
> LXD CSI is limited to Kubernetes clusters that are running within a single LXD project.

At this point, DevLXD is allowed to access the LXD endpoint for volume management, but CSI still needs to prove it is authorized to perform such actions.
We will create a DevLXD identity with sufficient permissions and issue a bearer token for it.

The identity must have permissions in the project where the Kubernetes nodes are running to:
- view the project,
- manage (view, create, edit, delete) storage volumes,
- edit instances.

First, create a new authorization group `csi-group` with the required permissions:
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

Deploy LXD CSI controller and node servers from manifests in [deploy](/deploy/) directory.
```sh
kubectl apply -f deploy/
```

### Using CSI driver

To use the CSI driver, create a Kubernetes StorageClass that points to the LXD storage pool you want to manage.

You can take the inspiration from an example in [example](/example/) directory.
