package fs

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Usage of an existing path. Exact values depend on the filesystem the test is
// running on, therefore only the relation between the reported values is checked.
func Test_Usage(t *testing.T) {
	dir := t.TempDir()

	stats, err := Usage(dir)
	require.NoError(t, err)

	require.Positive(t, stats.TotalBytes)
	require.GreaterOrEqual(t, stats.UsedBytes, int64(0))
	require.GreaterOrEqual(t, stats.AvailableBytes, int64(0))

	// Blocks reserved for a privileged user are not available, but are not
	// reported as used either. Therefore, the sum of the used and available
	// capacity does not have to match the total capacity.
	require.LessOrEqual(t, stats.UsedBytes+stats.AvailableBytes, stats.TotalBytes)

	// Used and free inodes always add up to the total number of inodes.
	require.Equal(t, stats.TotalInodes, stats.UsedInodes+stats.FreeInodes)

	// A path on the same filesystem must report the same total capacity.
	parentStats, err := Usage(filepath.Dir(dir))
	require.NoError(t, err)
	require.Equal(t, stats.TotalBytes, parentStats.TotalBytes)
}

// Usage of a non-existing path must report an error.
func Test_Usage_NotFound(t *testing.T) {
	_, err := Usage(filepath.Join(t.TempDir(), "non-existing"))
	require.Error(t, err)
}

// waitUntil condition returns true or timeout is reached.
func waitUntil(t *testing.T, d time.Duration, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("Condition not met within %s", d)
}

// Direct write to file.
// Create file, start watching it, modify file, expect handler to be triggered.
func Test_WatchFile_DirectWrite(t *testing.T) {
	var hits int32

	onChange := func(_ string) {
		atomic.AddInt32(&hits, 1)
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "token")

	// Create new file.
	require.NoError(t, os.WriteFile(file, []byte("initial content"), 0o640))

	// Start watching file.
	require.NoError(t, WatchFile(t.Context(), file, onChange))

	// Modify file.
	require.NoError(t, os.WriteFile(file, []byte("modified content"), 0o640))

	// Wait until change is detected and onChange handler triggered (hits >= 1).
	waitUntil(t, time.Second, func() bool { return atomic.LoadInt32(&hits) >= 1 })
}

// Symlink swap:
//
//	File:    dir/subdir1/file
//	File:    dir/subdir2/file
//	Symlink: dir/file -> Symlink to dir/subdir1/file (then swap to subdir2)
func Test_WatchFile_SymlinkSwap(t *testing.T) {
	var hits int32

	onChange := func(_ string) {
		atomic.AddInt32(&hits, 1)
	}

	dir := t.TempDir()
	file1 := filepath.Join(dir, "subdir1", "file")
	file2 := filepath.Join(dir, "subdir2", "file")
	require.NoError(t, os.MkdirAll(filepath.Dir(file1), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Dir(file2), 0o750))
	require.NoError(t, os.WriteFile(file1, []byte("content"), 0o640))
	require.NoError(t, os.WriteFile(file2, []byte("content"), 0o640)) // Same content!

	// Create dir/file symlink to dir/subdir1/file
	symlink := filepath.Join(dir, "file")
	require.NoError(t, os.Symlink(file1, symlink))

	// Start watching dir/file for changes.
	require.NoError(t, WatchFile(t.Context(), symlink, onChange))

	// Atomic symlink swap (similar to how Kubelet does it).
	tmpLink := filepath.Join(dir, "file_tmp")
	require.NoError(t, os.Symlink(file2, tmpLink))
	require.NoError(t, os.Rename(tmpLink, symlink))

	// Wait until change is detected and onChange handler triggered (hits >= 1).
	waitUntil(t, time.Second, func() bool { return atomic.LoadInt32(&hits) >= 1 })
}
