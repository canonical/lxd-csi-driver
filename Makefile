build:
	@echo "> Building LXD CSI ...";
	go build -ldflags "-s -w" -trimpath -o lxd-csi ./cmd/lxd-csi

build-image:
	@echo "> Building LXD CSI Image ...";
	rockcraft pack
