package main

import (
	"flag"
	"fmt"

	"k8s.io/klog/v2"

	"github.com/canonical/lxd-csi-driver/internal/driver"
)

var (
	driverName     = flag.String("driver-name", driver.DefaultDriverName, "Name of the CSI driver")
	endpoint       = flag.String("endpoint", driver.DefaultDriverEndpoint, "CSI endpoint (unix socket path)")
	devLXDEndpoint = flag.String("devlxd-endpoint", driver.DefaultDevLXDEndpoint, "Devlxd endpoint (devlxd unix socket path)")
	nodeID         = flag.String("node-id", "", "Kubernetes node ID")
	showVersion    = flag.Bool("version", false, "Show version and exit")
)

func run() error {
	klog.InitFlags(nil)
	flag.Parse()

	if *showVersion {
		fmt.Println(*driverName, driver.Version)
		return nil
	}

	opts := driver.DriverOptions{
		Name:           *driverName,
		Endpoint:       *endpoint,
		DevLXDEndpoint: *devLXDEndpoint,
		NodeID:         *nodeID,
	}

	return driver.NewDriver(opts).Run()
}

func main() {
	err := run()
	if err != nil {
		klog.Fatal(err)
	}
}
