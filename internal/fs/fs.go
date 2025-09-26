package fs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/sys/unix"
	"k8s.io/klog/v2"
	kmount "k8s.io/mount-utils"

	"github.com/canonical/lxd/lxd/storage/filesystem"
)

// mountOption represents an individual mount option.
type mountOption struct {
	capture bool
	flag    uintptr
}

// mountFlagTypes represents a list of possible mount flags.
var mountFlagTypes = map[string]mountOption{
	"async":         {false, unix.MS_SYNCHRONOUS},
	"atime":         {false, unix.MS_NOATIME},
	"bind":          {true, unix.MS_BIND},
	"defaults":      {true, 0},
	"dev":           {false, unix.MS_NODEV},
	"diratime":      {false, unix.MS_NODIRATIME},
	"dirsync":       {true, unix.MS_DIRSYNC},
	"exec":          {false, unix.MS_NOEXEC},
	"lazytime":      {true, unix.MS_LAZYTIME},
	"mand":          {true, unix.MS_MANDLOCK},
	"noatime":       {true, unix.MS_NOATIME},
	"nodev":         {true, unix.MS_NODEV},
	"nodiratime":    {true, unix.MS_NODIRATIME},
	"noexec":        {true, unix.MS_NOEXEC},
	"nomand":        {false, unix.MS_MANDLOCK},
	"norelatime":    {false, unix.MS_RELATIME},
	"nostrictatime": {false, unix.MS_STRICTATIME},
	"nosuid":        {true, unix.MS_NOSUID},
	"rbind":         {true, unix.MS_BIND | unix.MS_REC},
	"relatime":      {true, unix.MS_RELATIME},
	"remount":       {true, unix.MS_REMOUNT},
	"ro":            {true, unix.MS_RDONLY},
	"rw":            {false, unix.MS_RDONLY},
	"strictatime":   {true, unix.MS_STRICTATIME},
	"suid":          {false, unix.MS_NOSUID},
	"sync":          {true, unix.MS_SYNCHRONOUS},
}

// PathExists checks if the given path exists in the filesystem.
func PathExists(name string) bool {
	_, err := os.Lstat(name)
	if err != nil && os.IsNotExist(err) {
		return false
	}

	return true
}

// ResolveMountOptions resolves the provided mount options.
func ResolveMountOptions(options []string) (uintptr, string) {
	mountFlags := uintptr(0)
	var mountOptions []string

	for i := range options {
		do, ok := mountFlagTypes[options[i]]
		if !ok {
			mountOptions = append(mountOptions, options[i])
			continue
		}

		if do.capture {
			mountFlags |= do.flag
		} else {
			mountFlags &= ^do.flag
		}
	}

	return mountFlags, strings.Join(mountOptions, ",")
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

	flags, mountOptionsStr := filesystem.ResolveMountOptions(mountOptions)

	// Mount the filesystem
	err := unix.Mount(sourcePath, targetPath, "", uintptr(flags), mountOptionsStr)
	if err != nil {
		return fmt.Errorf("Unable to mount %q at %q: %w", sourcePath, targetPath, err)
	}

	readonly := slices.Contains(mountOptions, "ro")

	// Remount bind mounts in readonly mode if requested
	if readonly && flags&unix.MS_BIND == unix.MS_BIND {
		flags = unix.MS_RDONLY | unix.MS_BIND | unix.MS_REMOUNT
		err = unix.Mount("", targetPath, "", uintptr(flags), "")
		if err != nil {
			return fmt.Errorf("Unable to mount %q in readonly mode: %w", targetPath, err)
		}
	}

	flags = unix.MS_REC | unix.MS_SLAVE
	err = unix.Mount("", targetPath, "", uintptr(flags), "")
	if err != nil {
		return fmt.Errorf("Unable to make mount %q private: %w", targetPath, err)
	}

	return nil
}

// Unmount unmounts and removes the mount path used for disk shares.
func Unmount(path string) error {
	if !PathExists(path) {
		return nil
	}

	mounted, err := IsMountPoint(path)
	if err != nil {
		return err
	}

	if mounted {
		// Try unmounting a filesystem multiple times.
		for range 20 {
			err = unix.Unmount(path, 0)
			if err == nil {
				break
			}

			time.Sleep(500 * time.Millisecond)
		}

		if err != nil {
			return fmt.Errorf("Failed to unmount %q: %w", path, err)
		}
	}

	err = os.Remove(path)
	if err != nil {
		return fmt.Errorf("Failed to remove %q: %w", path, err)
	}

	return nil
}

// WatchFile sets up a file watcher for the file path and calls provided handler on file change.
func WatchFile(ctx context.Context, path string, fileChangeHandler func(path string)) error {
	// Ensure the provided path is clean to avoid potential path mismatch.
	path = filepath.Clean(path)

	// Resolve symlinks in the provided path.
	curRealPath, _ := filepath.EvalSymlinks(path)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("Failed to setup file watcher for path %q: %v", path, err)
	}

	// Watch the directory of the provided path because Kubernetes uses
	// symlink swap trick for updating the mounted files.
	err = watcher.Add(filepath.Dir(path))
	if err != nil {
		_ = watcher.Close()
		return fmt.Errorf("Failed to watch path %q: %v", path, err)
	}

	// Watch for token file changes.
	go func() {
		defer func() { _ = watcher.Close() }()
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					// Event channel closed.
					klog.ErrorS(errors.New("FSNotify event channel closed"), "Stopped watching file", "path", path)
					return
				}

				newRealPath, err := filepath.EvalSymlinks(path)
				if err != nil {
					klog.ErrorS(err, "Failed to resolve symlink for watched file", "path", path)
				}

				// Check if the file was modified or created.
				isFileContentChanged := filepath.Clean(event.Name) == path && (event.Has(fsnotify.Create) || event.Has(fsnotify.Write))

				// Check if the file symlink changed. This occurs when Kubernetes updates
				// the mounted secret/config file using the symlink swap trick.
				isFileSymlinkChanged := newRealPath != "" && newRealPath != curRealPath

				if isFileContentChanged || isFileSymlinkChanged {
					curRealPath = newRealPath
					fileChangeHandler(path)
				}
			case err, ok := <-watcher.Errors:
				if ok && err != nil {
					klog.ErrorS(err, "Error watching file", "path", path)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return nil
}
