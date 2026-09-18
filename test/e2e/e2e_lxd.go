package e2e

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/canonical/lxd-csi-driver/internal/driver"
	"github.com/canonical/lxd-csi-driver/test/testutils"
	lxd "github.com/canonical/lxd/client"
	lxdConfig "github.com/canonical/lxd/lxc/config"
	"github.com/canonical/lxd/shared/api"
)

var lxdClient lxd.InstanceServer

const defaultClusteredStoragePool = "default"

func getLXDClient() lxd.InstanceServer {
	if lxdClient != nil {
		return lxdClient
	}

	var configDir string

	// Determine LXD configuration directory. First check for the presence
	// of the /var/snap/lxd directory. If the directory exists, use snap's
	// config path. Otherwise fallback to non-snap config path.
	_, err := os.Stat("/var/snap/lxd")
	if err == nil || os.IsExist(err) {
		configDir = "$HOME/snap/lxd/common/config"
	} else {
		configDir = "$HOME/.config/lxc"
	}

	configDir = os.ExpandEnv(configDir)
	configPath := filepath.Join(configDir, "config.yml")

	// Try to load client config from determined configDir.
	// Otherwise load default config.
	config, err := lxdConfig.LoadConfig(configPath)
	if err != nil {
		config = lxdConfig.DefaultConfig()
	}

	lxdClient, err = config.GetInstanceServer(config.DefaultRemote)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to connect to LXD using default remote: %v", err)

	return lxdClient
}

func requiresStandaloneLXD() {
	if getLXDClient().IsClustered() {
		ginkgo.Skip("SKIP: Test requires standalone LXD")
	}
}

// requiresResizableBlockVolumes skips the test when the given LXD storage driver
// does not support block volumes or volume size.
func requiresResizableBlockVolumes(storageDriver string) {
	switch storageDriver {
	case "dir":
		ginkgo.Skip("SKIP: Driver dir does not support volume size")
	case "cephfs":
		ginkgo.Skip("SKIP: Driver cephfs does not support block volumes")
	}
}

// requiresMultiNodeVolumes skips the test when the given LXD storage driver does not
// support attaching a volume to multiple nodes at once.
func requiresMultiNodeVolumes(storageDriver string) {
	if !driver.IsMultiNodeStorageDriver(storageDriver) {
		ginkgo.Skip("SKIP: Driver " + storageDriver + " does not support multi-node volumes")
	}
}

// requiresSingleNodeVolumes skips the test when the given LXD storage driver
// supports attaching a volume to multiple nodes at once.
func requiresSingleNodeVolumes(storageDriver string) {
	if driver.IsMultiNodeStorageDriver(storageDriver) {
		ginkgo.Skip("SKIP: Driver " + storageDriver + " supports multi-node volumes")
	}
}

// getTestLXDStorageDrivers returns the list of LXD storage drivers to be used for testing.
// It reads the TEST_LXD_STORAGE_DRIVERS environment variable, which should contain a comma-separated
// list of drivers. If the variable is not set, it defaults to ["dir"].
func getTestLXDStorageDrivers() []ginkgo.TableEntry {
	driversStr := os.Getenv("TEST_LXD_STORAGE_DRIVERS")
	drivers := strings.Split(driversStr, ",")
	entries := make([]ginkgo.TableEntry, 0, len(drivers))

	for _, driver := range drivers {
		driver = strings.TrimSpace(driver)
		if driver == "" {
			continue
		}

		entries = append(entries, ginkgo.Entry("Driver "+driver, driver))
	}

	if len(entries) == 0 {
		// Default to "dir" driver if no drivers are specified.
		entries = append(entries, ginkgo.Entry("Driver dir", "dir"))
	}

	return entries
}

// getTestLXDStoragePool creates a new LXD storage pool with the given driver for testing purposes.
// It returns the name of the created storage pool and a cleanup function to delete it after use.
func getTestLXDStoragePool(driver string) (poolName string, cleanup func()) {
	lxdClient := getLXDClient()

	if lxdClient.IsClustered() {
		// XXX: Clustered LXD is tested only with the default storage pool.
		return defaultClusteredStoragePool, func() {}
	}

	poolName = "lxd-csi-" + driver + "-" + testutils.GenerateStringN(5)

	config := make(map[string]string)
	if driver != "dir" {
		config["volume.size"] = "128MiB"
	}

	// Pool size is not configurable for dir and cephfs pools.
	if driver != "dir" && driver != "cephfs" {
		config["size"] = "512MiB"
	}

	if driver == "lvm" {
		config["lvm.use_thinpool"] = "false"
	}

	if driver == "cephfs" {
		// Each pool uses its own directory within the "cephfs" file system.
		config["cephfs.path"] = "cephfs/" + poolName
	}

	req := api.StoragePoolsPost{
		Name:   poolName,
		Driver: driver,
		StoragePoolPut: api.StoragePoolPut{
			Config:      config,
			Description: "LXD CSI Driver E2E Test Storage Pool",
		},
	}

	op, err := lxdClient.CreateStoragePool(req)
	if err == nil {
		err = op.Wait()
	}

	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to create storage pool %q with driver %q: %v", req.Name, req.Driver, err)

	cleanup = func() {
		op, err := lxdClient.DeleteStoragePool(req.Name)
		if err == nil {
			_ = op.Wait()
		}
	}

	return poolName, cleanup
}
