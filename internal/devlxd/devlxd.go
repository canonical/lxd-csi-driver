package devlxd

import (
	"fmt"
	"os"

	"k8s.io/klog/v2"

	"github.com/canonical/lxd-csi-driver/internal/utils"
	lxdClient "github.com/canonical/lxd/client"
)

const (
	devLXDUserAgent = "lxd-csi-driver"
	devLXDTokenFile = "/etc/lxd-csi-driver/token"
)

// Connect establishes a connection to the devLXD server at the specified endpoint.
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

	tokenBytes, err := os.ReadFile(devLXDTokenFile)
	if err != nil {
		return nil, fmt.Errorf("Failed reading DevLXD bearer token file: %w", err)
	}

	// Connect to devLXD.
	connArgs := lxdClient.ConnectionArgs{
		UserAgent:   devLXDUserAgent,
		BearerToken: string(tokenBytes),
	}

	client, err := lxdClient.ConnectDevLXD(socket, &connArgs)
	if err != nil {
		return nil, err
	}

	klog.InfoS("Connected to devLXD", "endpoint", socket)

	return client, nil
}
