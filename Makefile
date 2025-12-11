REGISTRY=ghcr.io
IMAGE=canonical/lxd-csi-driver
VERSION?=dev
SNAPSHOT_CRD_VERSION=8.4.0

build:
	@echo "> Building LXD CSI ...";
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w -X github.com/canonical/lxd-csi-driver/internal/driver.driverVersion=${VERSION}" -trimpath -o lxd-csi ./cmd/lxd-csi

image-build: build
	@echo "> Building image $(REGISTRY)/$(IMAGE):$(VERSION) ...";
	docker build . -t $(REGISTRY)/$(IMAGE):$(VERSION)

image-export: image-build
	docker save $(REGISTRY)/$(IMAGE):$(VERSION) -o lxd-csi-driver.tar

install-helm:
	@set -e
	@command -v helm >/dev/null || { \
		echo "Installing Helm..."; \
		curl -fsSL https://raw.githubusercontent.com/helm/helm/master/scripts/get-helm-3 | bash; \
		helm version; \
	}
	@echo "Installing Helm plugin unittest ..."
	@helm plugin install https://github.com/helm-unittest/helm-unittest > /dev/null || true
	@echo "Done."

update-crds:
	@echo "> Updating Helm custom resource definitions (CRDs) ..."
	@rm -f charts/files/crd_*.yaml
	@mkdir -p charts/files
	@CRD_BASE_URL=https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/refs/tags/v$(SNAPSHOT_CRD_VERSION)/client/config/crd; \
	wget -q "$$CRD_BASE_URL/snapshot.storage.k8s.io_volumesnapshotclasses.yaml" -O charts/files/crd_volume-snapshot-classes.yaml; \
	wget -q "$$CRD_BASE_URL/snapshot.storage.k8s.io_volumesnapshotcontents.yaml" -O charts/files/crd_volume-snapshot-contents.yaml; \
	wget -q "$$CRD_BASE_URL/snapshot.storage.k8s.io_volumesnapshots.yaml" -O charts/files/crd_volume-snapshots.yaml; \
	echo "Done."

static-analysis:
	@echo "Running gofmt check ..."
	@BAD_FORMAT="$$(gofmt -s -d .)"; \
	if [ -n "$$BAD_FORMAT" ]; then \
		echo "Formatting issues found in Go file(s):"; \
		echo "$$BAD_FORMAT"; \
		exit 1; \
	fi
	@echo "Running go vet ..."
	@go vet ./...
	@echo "Running shell check ..."
	@find . -type f -name '*.sh' -print0 | xargs -0 shellcheck
	@echo "Done."
