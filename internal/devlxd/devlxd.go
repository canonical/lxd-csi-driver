package devlxd

import (
	"fmt"
	"os"

	lxdClient "github.com/canonical/lxd/client"
	"k8s.io/klog/v2"

	"github.com/canonical/lxd-csi-driver/internal/utils"
)

const devLXDUserAgent = "lxd-csi-driver"

func Connect(endpoint string) (lxdClient.DevLXDServer, error) {
	// Parse and verify devLXD address.
	_, socket, err := utils.ParseUnixSocketURL(endpoint)
	if err != nil {
		return nil, err
	}

	socketInfo, err := os.Stat(socket)
	if err != nil {
		return nil, err
	}

	if socketInfo.Mode()&os.ModeSocket == 0 {
		return nil, fmt.Errorf("Invalid devLXD socket path %q: Not a socket", socket)
	}

	// Connect to devLXD.
	connArgs := lxdClient.ConnectionArgs{
		UserAgent: devLXDUserAgent,
	}

	client, err := lxdClient.ConnectDevLXD(socket, &connArgs)
	if err != nil {
		return nil, err
	}

	klog.InfoS("Connected to devLXD", "endpoint", socket)

	return client, nil
}
