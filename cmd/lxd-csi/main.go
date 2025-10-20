package main

import (
	"flag"

	"k8s.io/klog/v2"

	"github.com/canonical/lxd-csi-driver/internal/driver"
)

var (
	driverName       = flag.String("driver-name", driver.DefaultDriverName, "Name of the CSI driver")
	endpoint         = flag.String("endpoint", driver.DefaultDriverEndpoint, "CSI endpoint (unix socket path)")
	devLXDEndpoint   = flag.String("devlxd-endpoint", driver.DefaultDevLXDEndpoint, "Devlxd endpoint (devlxd unix socket path)")
	volumeNamePrefix = flag.String("volume-name-prefix", driver.DefaultVolumeNamePrefix, "Prefix used for LXD volume names")
	nodeID           = flag.String("node-id", "", "Kubernetes node ID")
	isController     = flag.Bool("controller", false, "Start LXD CSI driver controller server")
)

func run() error {
	klog.InitFlags(nil)
	flag.Parse()

	opts := driver.DriverOptions{
		Name:             *driverName,
		Endpoint:         *endpoint,
		DevLXDEndpoint:   *devLXDEndpoint,
		VolumeNamePrefix: *volumeNamePrefix,
		NodeID:           *nodeID,
		IsController:     *isController,
	}

	return driver.NewDriver(opts).Run()
}

func main() {
	err := run()
	if err != nil {
		klog.Fatal(err)
	}
}
