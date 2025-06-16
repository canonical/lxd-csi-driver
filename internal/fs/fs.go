package fs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	kmount "k8s.io/mount-utils"
	kexec "k8s.io/utils/exec"
)

// IsMounted checks whether the given target path is a mount point.
func IsMounted(target string) (bool, error) {
	if target == "" {
		return false, errors.New("target is not specified for checking the mount")
	}

	findmntCmd := "findmnt"
	_, err := exec.LookPath(findmntCmd)
	if err != nil {
		if err == exec.ErrNotFound {
			return false, fmt.Errorf("%q executable not found in $PATH", findmntCmd)
		}

		return false, err
	}

	findmntArgs := []string{"-o", "TARGET,PROPAGATION,FSTYPE,OPTIONS", "-M", target, "-J"}

	// Check if mount already exists.
	out, err := exec.Command(findmntCmd, findmntArgs...).CombinedOutput()

	// The findmnt exits with non-zero exit status if it couldn't find anything.
	// If there is no response, it means the target is not mounted. In such case
	// return without an error.
	if strings.TrimSpace(string(out)) == "" {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("Failed checking if target %q is mounted (%v): %s", target, err, string(out))
	}

	type fileSystem struct {
		Target      string `json:"target"`
		Propagation string `json:"propagation"`
		FsType      string `json:"fstype"`
		Options     string `json:"options"`
	}

	type findmntResponse struct {
		FileSystems []fileSystem `json:"filesystems"`
	}

	var resp *findmntResponse
	err = json.Unmarshal(out, &resp)
	if err != nil {
		return false, fmt.Errorf("Failed to unmarshal data %q: %s", string(out), err)
	}

	// Try to find a matching mount.
	for _, fs := range resp.FileSystems {
		if fs.Target == target {
			return true, nil
		}
	}

	return false, nil
}

// Mount mounts a volume to a target path.
func Mount(sourcePath string, targetPath string, contentType string, mountOptions []string) error {
	mountCmd := "mount"
	mountArgs := []string{}

	if sourcePath == "" {
		return errors.New("Volume mount source path is not specified")
	}

	if targetPath == "" {
		return errors.New("Volume mount target path is not specified")
	}

	switch contentType {
	case "filesystem":
		err := os.MkdirAll(targetPath, 0750)
		if err != nil {
			return err
		}
	case "block":
		// Mount a raw block device.
		// Create the mount point as a file since bind mount device node
		// requires it to be a file.
		err := os.MkdirAll(filepath.Dir(targetPath), 0750)
		if err != nil {
			return fmt.Errorf("Failed to create target directory for bind mount: %v", err)
		}

		file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_RDWR, 0660)
		if err != nil {
			return fmt.Errorf("Failed to create target file for bind mount: %v", err)
		}

		_ = file.Close()
	default:
		return fmt.Errorf("Invalid content type %q", contentType)
	}

	if len(mountOptions) > 0 {
		mountArgs = append(mountArgs, "-o", strings.Join(mountOptions, ","))
	}

	mountArgs = append(mountArgs, sourcePath)
	mountArgs = append(mountArgs, targetPath)

	out, err := exec.Command(mountCmd, mountArgs...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("Failed to bind mount volume from %q to %q (%v): %s", sourcePath, targetPath, err, string(out))
	}

	return nil
}

// Unmount unmounts a volume from a target path.
func Unmount(targetPath string) error {
	mounter := &kmount.SafeFormatAndMount{
		Interface: kmount.New(""),
		Exec:      kexec.New(),
	}

	return kmount.CleanupMountPoint(targetPath, mounter, true)
}
