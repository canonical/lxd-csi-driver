REGISTRY=ghcr.io
IMAGE=canonical/lxd-csi
VERSION=0.0.1

build:
	@echo "> Building LXD CSI ...";
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -trimpath -o lxd-csi ./cmd/lxd-csi

image-build: build
	@echo "> Building image $(REGISTRY)/$(IMAGE):$(VERSION) ...";
	docker build . -t $(REGISTRY)/$(IMAGE):$(VERSION)

image-export: image-build
	docker save $(REGISTRY)/$(IMAGE):$(VERSION) -o lxd-csi-driver.tar
