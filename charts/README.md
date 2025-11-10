# LXD CSI Helm chart

Helm chart for a LXD CSI Driver.

## Install local Helm chart

Create the namespace and the secret containing DevLXD bearer token.
```sh
kubectl create namespace lxd-csi --save-config
kubectl create secret generic lxd-csi-secret \
    --namespace lxd-csi \
    --from-literal=token="<DEVLXD_TOKEN>"
```

Package and install the chart:
```sh
helm package . --app-version latest-edge
helm install lxd-csi lxd-csi-driver-v0-dev.tgz --namespace lxd-csi --atomic
```

## Uninstall local Helm chart

```sh
helm delete lxd-csi --namespace lxd-csi
kubectl delete namespace lxd-csi
```

## Helm Chart unit tests

Install Helm [`unittest`](https://github.com/helm-unittest/helm-unittest) plugin:
```sh
helm plugin install https://github.com/helm-unittest/helm-unittest.git
```

Run unit tests:
```sh
helm unittest .
```
