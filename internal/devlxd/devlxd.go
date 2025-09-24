package devlxd

import (
	"fmt"
	"os"

	"k8s.io/klog/v2"

	"github.com/canonical/lxd-csi-driver/internal/utils"
	lxdClient "github.com/canonical/lxd/client"
)

const (
	// devLXDUserAgent is the User-Agent header used when connecting to devLXD.
	devLXDUserAgent = "lxd-csi-driver"
)

// Connect establishes a connection to the devLXD server at the specified endpoint.
func Connect(endpoint string, bearerToken string) (lxdClient.DevLXDServer, error) {
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
		UserAgent:   devLXDUserAgent,
		BearerToken: bearerToken,
	}

	client, err := lxdClient.ConnectDevLXD(socket, &connArgs)
	if err != nil {
		return nil, err
	}

	klog.InfoS("Connected to devLXD", "endpoint", socket)

	return client, nil
}
