# LXD CSI Helm Chart

Helm chart for a LXD CSI Driver.

## Prerequisites

The Helm chart requires an existing Kubernetes Secret containing a DevLXD bearer token. The Secret must include the token under the `token` key.
By default, the chart expects the Secret to be named `lxd-csi-secret`, but this can be changed using the `driver.tokenSecretName` value.
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: lxd-csi-token
  namespace: lxd-csi # Secret must be in the same namespace as LXD CSI driver.
type: Opaque
stringData:
  token: "<devlxd_token>"
```

## Installing the Chart

Install the chart:
```sh
helm install lxd-csi-driver oci://ghcr.io/canonical/charts/lxd-csi-driver \
  --version v0.0.0-latest-edge \
  --namespace lxd-csi \
  --create-namespace \
  -f values.yaml
```

Optionally, you can retrieve default chart [values](/values.yaml) and edit them:
```sh
helm show values oci://ghcr.io/canonical/charts/lxd-csi-driver --version v0.0.0-latest-edge > values.yaml
```

> [!TIP]
> Use `template` command instead of `install` to see the resulting manifests.

## Uninstalling the Chart

```sh
helm delete lxd-csi-driver --namespace lxd-csi
```

## Chart Unit Tests

Install Helm [`unittest`](https://github.com/helm-unittest/helm-unittest) plugin:
```sh
helm plugin install https://github.com/helm-unittest/helm-unittest.git
```

Run unit tests:
```sh
helm unittest charts/
```
