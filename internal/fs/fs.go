package fs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	kmount "k8s.io/mount-utils"
	kexec "k8s.io/utils/exec"
)

// PathExists checks if the given path exists in the filesystem.
func PathExists(name string) bool {
	_, err := os.Lstat(name)
	if err != nil && os.IsNotExist(err) {
		return false
	}

	return true
}

// IsMountPoint returns true if path is a mount point.
func IsMountPoint(path string) (bool, error) {
	mounter := kmount.New("")
	mounted, err := mounter.IsMountPoint(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}

	return mounted, nil
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
